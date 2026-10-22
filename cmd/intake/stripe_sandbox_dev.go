package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

const sandboxDevMarker = "STRIPE_SANDBOX_DEV_READY\n"

// These are private offline seams, not command options or environment overrides.
// Runtime selection is fixed here; tests never resolve Stripe or read real .env.
type sandboxDevDependencies struct {
	volumePresent func() bool
	resolve       func() (string, error)
	readKey       func() (string, error)
	command       func(string, ...string) *exec.Cmd
	app           func(context.Context, string, string, func([]byte)) error
	probe         func(context.Context, []byte) error
	output        io.Writer
	versionWait   time.Duration
	readyWait     time.Duration
}

func executeStripeSandboxDev(ctx context.Context, args []string, observer failureowner.Observer) error {
	return executeStripeSandboxDevWith(ctx, args, observer, sandboxDevDependencies{
		volumePresent: func() bool { _, present := os.LookupEnv("LEDGER_VOLUME_KEY_B64"); return present },
		resolve: func() (string, error) {
			path, err := exec.LookPath("stripe")
			if err != nil {
				return "", err
			}
			return filepath.Abs(path)
		},
		readKey: func() (string, error) { return readSandboxDevKey(".env") },
		command: exec.Command,
		app: func(ctx context.Context, key, secret string, bound func([]byte)) error {
			return executeStripeSandboxLocalReady(ctx, nil, observer, func(name string) string {
				switch name {
				case "STRIPE_API_KEY":
					return key
				case "STRIPE_WEBHOOK_SECRET":
					return secret
				default:
					return ""
				}
			}, func() bool { return false }, bound)
		},
		probe: sandboxDevProbe, output: os.Stdout,
		versionWait: 5 * time.Second, readyWait: 30 * time.Second,
	})
}

func executeStripeSandboxDevWith(ctx context.Context, args []string, observer failureowner.Observer, dependencies sandboxDevDependencies) (result error) {
	stage, appOwns := "stripe_dev_options", false
	var private sandboxDevPrivate
	var cli *sandboxDevCLI
	defer func() {
		if recover() != nil && result == nil {
			result, appOwns = sandboxDevFailure(stage, "development_lifecycle_cause_unknown"), false
		}
		// superviseSandboxDev always joins app work before control returns here,
		// including panic. The CLI and its private files outlive that drain.
		if cli != nil {
			if err := cli.stop(); result == nil && err != nil {
				result, appOwns = err, false
			}
		}
		if private.root != "" {
			if os.RemoveAll(private.root) != nil && result == nil {
				result, appOwns = sandboxDevFailure("stripe_dev_cleanup", "joined_private_directory_removed"), false
			}
		}
		if result != nil && !appOwns {
			failureowner.Observe(observer, ledger.FailureKind(result, failuretype.RuntimeSetupUnknown))
		}
	}()
	if len(args) != 0 {
		return runtimeFailure(codeSyntheticOptions, stage, "explicit_no_argument_command", "not_attempted", "Use stripe-sandbox-dev without arguments. No input, CLI or application was started.")
	}
	if dependencies.volumePresent() {
		return runtimeFailure(codeVolumeOptions, stage, "no_managed_volume_secret", "not_attempted", "Use this disposable development command without a managed volume key. No input, CLI or application was started.")
	}
	if ctx.Err() != nil {
		return nil
	}
	stage = "stripe_dev_resolve"
	binary, err := dependencies.resolve()
	if err != nil || !filepath.IsAbs(binary) {
		return sandboxDevFailure(stage, "absolute_pinned_devenv_cli")
	}
	stage = "stripe_dev_private"
	private, err = newSandboxDevPrivate()
	if err != nil {
		return err
	}
	stage = "stripe_dev_version"
	if err := sandboxDevVersion(ctx, dependencies.command, binary, private, dependencies.versionWait); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return nil
	}
	stage = "stripe_dev_input"
	key, err := dependencies.readKey()
	if err != nil {
		if diagnostic, ok := err.(*ledger.Diagnostic); ok {
			return diagnostic
		}
		return sandboxDevFailure(stage, "one_literal_nonfixture_test_key")
	}
	// The input seam must not bypass the same inert constructor validation.
	key, err = parseSandboxDevKey("STRIPE_API_KEY=" + key)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return nil
	}
	stage = "stripe_dev_spawn"
	cli, err = startSandboxDevCLI(dependencies.command, binary, private, key)
	if err != nil {
		return err
	}
	stage = "stripe_dev_ready"
	timer := time.NewTimer(dependencies.readyWait)
	defer timer.Stop()
	var ready sandboxDevReady
	select {
	case <-ctx.Done():
		return nil
	case <-timer.C:
		return sandboxDevFailure(stage, "bounded_listener_readiness")
	case ready = <-cli.ready:
		if ready.err != nil {
			return ready.err
		}
	case <-cli.waitDone:
		return cli.companionFailure()
	}
	if ctx.Err() != nil {
		return nil
	}
	if !cli.live() {
		return cli.companionFailure()
	}
	result, appOwns = superviseSandboxDev(ctx, cli, key, ready.secret, dependencies, observer)
	return result
}

// The app's typed primary wins over companion/probe/presentation failures even
// when detected later during drain. Cancellation alone is not a fault.
func superviseSandboxDev(ctx context.Context, cli *sandboxDevCLI, key, secret string, dependencies sandboxDevDependencies, observer failureowner.Observer) (result error, appOwns bool) {
	work, cancel := context.WithCancel(ctx)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		select {
		case <-work.Done():
		case <-cli.waitDone:
			cancel()
		case <-cli.readDone:
			cancel()
		}
	}()
	bound := make(chan []byte, 1)
	appDone := make(chan error, 1)
	go func() {
		var result error
		owned := false
		defer func() {
			if recover() != nil {
				result = sandboxDevFailure("stripe_dev_app", "application_lifecycle_cause_unknown")
			}
			if result != nil && !owned {
				failureowner.Observe(observer, ledger.FailureKind(result, failuretype.RuntimeSetupUnknown))
			}
			appDone <- result
		}()
		result = dependencies.app(work, key, secret, func(der []byte) { bound <- der })
		_, owned = result.(*ledger.Diagnostic)
		if result != nil {
			if work.Err() != nil && ledger.FailureKind(result, failuretype.RuntimeSetupUnknown) == failuretype.Cancelled {
				result = nil
			} else if _, typed := result.(*ledger.Diagnostic); !typed {
				result = sandboxDevFailure("stripe_dev_app", "application_result_cause_unknown")
			}
		}
	}()
	appJoined := false
	defer func() {
		if recover() != nil {
			result = sandboxDevFailure("stripe_dev_supervise", "supervisor_cause_unknown")
		}
		cancel()
		<-monitorDone
		if !appJoined {
			if err := <-appDone; err != nil {
				result, appOwns = err, true
			}
		}
	}()
	interrupted := func() error {
		if ctx.Err() != nil {
			return nil
		}
		return cli.companionFailure()
	}
	var der []byte
	select {
	case <-work.Done():
		return interrupted(), false
	case err := <-appDone:
		appJoined = true
		if err != nil {
			return err, true
		}
		if work.Err() != nil {
			return interrupted(), false
		}
		return sandboxDevFailure("stripe_dev_app", "application_remains_running"), false
	case der = <-bound:
	}
	// The monitor cancels this single bounded request if the companion exits.
	if err := callSandboxDevProbe(work, der, dependencies.probe); err != nil {
		if work.Err() != nil {
			return interrupted(), false
		}
		return err, false
	}
	if work.Err() != nil {
		return interrupted(), false
	}
	if !cli.live() {
		return cli.companionFailure(), false
	}
	if err := writeSandboxDevReady(dependencies.output); err != nil {
		return err, false
	}
	select {
	case <-work.Done():
		return interrupted(), false
	case err := <-appDone:
		appJoined = true
		if err != nil {
			return err, true
		}
		if work.Err() != nil {
			return interrupted(), false
		}
		return sandboxDevFailure("stripe_dev_app", "application_remains_running"), false
	}
}

func callSandboxDevProbe(ctx context.Context, der []byte, probe func(context.Context, []byte) error) (result error) {
	defer func() {
		if recover() != nil {
			result = sandboxDevFailure("stripe_dev_probe", "local_probe_cause_unknown")
		}
	}()
	if err := probe(ctx, der); err != nil {
		if diagnostic, ok := err.(*ledger.Diagnostic); ok {
			return diagnostic
		}
		return sandboxDevFailure("stripe_dev_probe", "local_probe_cause_unknown")
	}
	return nil
}

func writeSandboxDevReady(output io.Writer) (result error) {
	defer func() {
		if recover() != nil {
			result = sandboxDevFailure("stripe_dev_output", "readiness_delivery_cause_unknown")
		}
	}()
	n, err := io.WriteString(output, sandboxDevMarker)
	if err != nil || n != len(sandboxDevMarker) {
		return sandboxDevFailure("stripe_dev_output", "complete_fixed_readiness_marker")
	}
	return nil
}

func sandboxDevReadinessClient(der []byte) (*http.Client, error) {
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, sandboxDevFailure("stripe_dev_probe", "exact_runtime_certificate")
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy: nil, DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12,
				VerifyConnection: func(state tls.ConnectionState) error {
					if len(state.PeerCertificates) == 0 || !bytes.Equal(state.PeerCertificates[0].Raw, der) {
						return sandboxDevFailure("stripe_dev_probe", "exact_runtime_certificate")
					}
					return nil
				}},
			TLSHandshakeTimeout: 2 * time.Second, ResponseHeaderTimeout: 2 * time.Second,
			MaxResponseHeaderBytes: 4096, DisableKeepAlives: true, DisableCompression: true,
		},
	}, nil
}

func sandboxDevProbe(ctx context.Context, der []byte) error {
	client, err := sandboxDevReadinessClient(der)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	return sandboxDevProbeWith(ctx, client)
}

func sandboxDevProbeWith(ctx context.Context, client *http.Client) (result error) {
	defer func() {
		if recover() != nil {
			result = sandboxDevFailure("stripe_dev_probe", "local_probe_cause_unknown")
		}
	}()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://127.0.0.1:8443/login", nil)
	if err != nil {
		return sandboxDevFailure("stripe_dev_probe", "fixed_local_request")
	}
	response, err := client.Do(request)
	if err != nil {
		return sandboxDevFailure("stripe_dev_probe", "verified_local_https_completion")
	}
	defer func() {
		if response.Body.Close() != nil && result == nil {
			result = sandboxDevFailure("stripe_dev_probe", "local_response_closed")
		}
	}()
	const limit = 64 << 10
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || len(body) > limit || response.StatusCode != http.StatusOK {
		return sandboxDevFailure("stripe_dev_probe", "bounded_complete_http_200_required")
	}
	return nil
}
