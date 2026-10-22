package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/httpapi"
	"possums-intake/internal/ledger"
	"possums-intake/internal/payments"
	"possums-intake/internal/siteauth"
	"possums-intake/internal/stripepay"
	"possums-intake/internal/website"
)

type sandboxLocalOptions struct{ website, webhook string }

func parseSandboxLocalOptions(args []string) (sandboxLocalOptions, error) {
	flags := flag.NewFlagSet("stripe-sandbox-local", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var options sandboxLocalOptions
	flags.StringVar(&options.website, "listen", "127.0.0.1:8443", "Numeric loopback HTTPS listener")
	flags.StringVar(&options.webhook, "webhook-listen", "127.0.0.1:8844", "Numeric loopback HTTP webhook listener")
	if flags.Parse(args) != nil || flags.NArg() != 0 || !sandboxLocalAddress(options.website) || !sandboxLocalAddress(options.webhook) || options.website == options.webhook {
		return options, runtimeFailure(codeSyntheticOptions, "stripe_sandbox_options", "distinct_canonical_ipv4_loopback_ports_only", "not_attempted", "Use stripe-sandbox-local with only optional --listen and --webhook-listen, each 127.0.0.1 with a distinct explicit nonzero canonical port. State and credential arguments are forbidden.")
	}
	return options, nil
}

func sandboxLocalAddress(address string) bool {
	host, port, err := net.SplitHostPort(address)
	n, portErr := strconv.ParseUint(port, 10, 16)
	return err == nil && host == "127.0.0.1" && address == net.JoinHostPort(host, port) && portErr == nil && n != 0 && strconv.FormatUint(n, 10) == port
}

func sandboxLocalFailure(stage, constraint string) *ledger.Diagnostic {
	return runtimeFailure("STRIPE_SANDBOX_LOCAL_UNAVAILABLE", stage, constraint, "local_fixture_state_unknown", "Local sandbox runtime cause unknown. Inspect the closed stage/constraint privately; do not replay a payment to diagnose it. No production state is opened.")
}

// Startup adapters return closed types. Preserve them at the CLI's ledger DTO
// presentation boundary rather than losing a known refusal to its fallback.
func sandboxLocalDiagnostic(err error, stage string) error {
	switch e := err.(type) {
	case *unconfirmedHTTPDrain:
		return e
	case *ledger.Diagnostic:
		return e
	case *stripepay.Error:
		kind := failuretype.RuntimeSetupUnknown
		if e.Code == stripepay.CodeSandboxConfiguration {
			kind = failuretype.RequestInvalid
		}
		d := ledger.CanonicalFailure(&ledger.Diagnostic{}, kind)
		d.Code, d.Stage, d.Constraint, d.Outcome, d.NextStep = ledger.Code(e.Code), string(e.Stage), string(e.Constraint), string(e.Outcome), string(e.NextStep)
		return d
	case *payments.Error:
		d := ledger.CanonicalFailure(&ledger.Diagnostic{}, failuretype.RuntimeSetupUnknown)
		d.Code, d.Stage, d.Constraint, d.Outcome, d.NextStep = ledger.Code(e.Code), e.Stage, e.Constraint, e.Outcome, e.NextStep
		return d
	default:
		return sandboxLocalFailure(stage, "local_setup_completion_cause_unknown")
	}
}

func executeStripeSandboxLocal(ctx context.Context, args []string, observer failureowner.Observer) error {
	return executeStripeSandboxLocalInputs(ctx, args, observer, os.Getenv, func() bool {
		_, present := os.LookupEnv("LEDGER_VOLUME_KEY_B64")
		return present
	})
}

// The input seam permits authored offline credentials without reading inherited
// private inputs in tests. Only this explicit command reads these two secrets.
func executeStripeSandboxLocalInputs(ctx context.Context, args []string, observer failureowner.Observer, secret func(string) string, volumeKeyPresent func() bool) error {
	return executeStripeSandboxLocalReady(ctx, args, observer, secret, volumeKeyPresent, nil)
}

// The development supervisor receives only the public ephemeral certificate,
// after both binds. The callback must not block; serving starts immediately after it.
func executeStripeSandboxLocalReady(ctx context.Context, args []string, observer failureowner.Observer, secret func(string) string, volumeKeyPresent func() bool, bound func([]byte)) (result error) {
	stage, owned := "stripe_sandbox_options", false
	defer func() {
		if recover() != nil {
			fault := sandboxLocalFailure(stage, "completed_local_fixture_lifecycle")
			failureowner.Observe(observer, fault.Kind())
			if result == nil {
				result = fault
			}
			owned = true
		}
		if result != nil && !owned {
			result = sandboxLocalDiagnostic(result, stage)
			failureowner.Observe(observer, ledger.FailureKind(result, failuretype.RuntimeSetupUnknown))
		}
		if drain, ok := result.(*unconfirmedHTTPDrain); ok {
			result = drain.Diagnostic
		}
	}()
	options, err := parseSandboxLocalOptions(args)
	if err != nil {
		return err
	}
	if volumeKeyPresent() {
		return runtimeFailure(codeVolumeOptions, stage, "no_managed_volume_secret", "not_attempted", "Use this disposable local mode without a managed volume key. It never opens persistent production state or unlocks storage.")
	}
	stage = "stripe_sandbox_credentials"
	client, err := stripepay.NewSandbox(stripepay.SandboxConfig{ReturnOrigin: "https://" + options.website, APIKey: secret("STRIPE_API_KEY"), WebhookSecret: secret("STRIPE_WEBHOOK_SECRET")})
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return nil
	}
	stage = "stripe_sandbox_scratch"
	scratch, err := os.MkdirTemp("", "possums-intake-stripe-sandbox-local-")
	if err != nil {
		return sandboxLocalFailure(stage, "private_temporary_state")
	}
	removeScratch := false
	defer func() {
		if err := cleanupScratch(scratch, removeScratch); err != nil {
			failureowner.Observe(observer, ledger.FailureKind(err, failuretype.RuntimeShutdownUnknown))
			if result == nil {
				result, owned = err, true
			}
		}
	}()
	stage = "stripe_sandbox_init"
	if err := ledger.Init(ctx, scratch); err != nil {
		return err
	}
	service, err := ledger.Open(ctx, scratch)
	if err != nil {
		return err
	}
	defer func() {
		panicked := recover() != nil
		if panicked {
			fault := sandboxLocalFailure(stage, "completed_local_fixture_lifecycle")
			failureowner.Observe(observer, fault.Kind())
			if result == nil {
				result = fault
			}
			owned = true
		}
		// An unconfirmed drain (or unexpected escape) cannot authorize closing
		// storage beneath handlers. Keep state/resources for process teardown.
		if panicked {
			return
		}
		var closeErr error
		removeScratch, closeErr = closeSandboxLocal(service, result, observer)
		if closeErr != nil && result == nil {
			result, owned = closeErr, true
		}
	}()
	stage = "stripe_sandbox_payment_admission"
	if err := service.InitializeSyntheticPayments(ctx); err != nil {
		return err
	}
	paymentService, err := payments.NewSandbox(ctx, service, client)
	if err != nil {
		return err
	}
	stage = "stripe_sandbox_fixture"
	handler, err := newSandboxLocalWebsite(ctx, service, paymentService)
	if err != nil {
		return err
	}
	tlsConfig, err := localWebsiteTLS()
	if err != nil {
		return err
	}
	stage = "stripe_sandbox_bind"
	closeListener := func(listener net.Listener) {
		if err := closeSandboxLocalListener(listener); err != nil {
			failureowner.Observe(observer, ledger.FailureKind(err, failuretype.RuntimeShutdownUnknown))
			if result == nil {
				result, owned = err, true
			} else if !httpDrainUnconfirmed(result) {
				result = &unconfirmedHTTPDrain{sandboxLocalDiagnostic(result, stage).(*ledger.Diagnostic)}
			}
		}
	}
	websiteListener, err := net.Listen("tcp", options.website)
	if err != nil {
		return sandboxLocalBindFailure()
	}
	defer closeListener(websiteListener)
	webhookListener, err := net.Listen("tcp", options.webhook)
	if err != nil {
		return sandboxLocalBindFailure()
	}
	defer closeListener(webhookListener)
	if bound != nil {
		bound(append([]byte(nil), tlsConfig.Certificates[0].Certificate[0]...))
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	websiteServer := localServer(work, handler)
	webhookServer := localServer(work, paymentService.WebhookHandler())
	pair := newSandboxLocalServers(websiteServer, webhookServer, webhookListener, observer)
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	stage, owned = "stripe_sandbox_serve", true
	return coordinateServeObserved(ctx, work, cancel, service, pair, tls.NewListener(websiteListener, tlsConfig), ticker.C, time.Now(), observer)
}

func closeSandboxLocalListener(listener net.Listener) (result error) {
	defer func() {
		if recover() != nil {
			result = runtimeFailure(codeShutdown, "stripe_sandbox_listener_close", "closed_loopback_listener", "unknown", "Listener cleanup cause unknown. Preserve local state until process teardown; do not replay a payment.")
		}
	}()
	if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return runtimeFailure(codeShutdown, "stripe_sandbox_listener_close", "closed_loopback_listener", "unknown", "Listener cleanup cause unknown. Preserve local state until process teardown; do not replay a payment.")
	}
	return nil
}

func closeSandboxLocal(service interface {
	CloseObserved(failureowner.Observer) error
}, primary error, observer failureowner.Observer) (bool, error) {
	if httpDrainUnconfirmed(primary) {
		return false, nil
	}
	err := service.CloseObserved(observer)
	return err == nil, err
}

func sandboxLocalBindFailure() *ledger.Diagnostic {
	return runtimeFailure(codeBind, "stripe_sandbox_bind", "two_available_loopback_listeners", "not_attempted", "Local listener unavailable; cause unknown. Check both loopback ports privately. No provider action was attempted.")
}

func newSandboxLocalWebsite(ctx context.Context, service *ledger.Service, actions website.TopupActions) (http.Handler, error) {
	app, err := service.CustomerAuthApp()
	if err != nil {
		return nil, err
	}
	if err := seedWebsiteCustomer(ctx, app); err != nil {
		return nil, err
	}
	auth, err := siteauth.New(app, localWebsiteCollection)
	if err != nil {
		return nil, err
	}
	bundle, err := website.Bundled()
	if err != nil {
		return nil, err
	}
	revision, err := website.Prepare(ctx, bundle)
	if err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, sandboxLocalFailure("stripe_sandbox_fixture", "private_accounting_fallback_key")
	}
	fallback, err := httpapi.NewHandler(service, func() bool { return ctx.Err() == nil && service.Ready() }, hex.EncodeToString(key), nil)
	if err != nil {
		return nil, sandboxLocalFailure("stripe_sandbox_fixture", "private_accounting_fallback")
	}
	return website.NewFixture(revision, siteauth.FixtureKeyAdapter{Adapter: auth, Identity: localWebsiteIdentity}, fallback, website.Config{Lifetime: time.Hour, IdleTimeout: 10 * time.Minute, ConcurrentRequests: 16, Balances: service, Payments: actions})
}
