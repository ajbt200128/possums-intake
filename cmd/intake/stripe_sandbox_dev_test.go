package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"possums-intake/internal/ledger"
)

// Authored Go child only. No actual CLI, inherited private environment, .env,
// network or shell. ExtraFiles are test-only coordination pipes, never secrets.
func TestSandboxDevCLIHelper(t *testing.T) {
	index := -1
	for i, arg := range os.Args {
		if arg == "--" {
			index = i + 1
			break
		}
	}
	if index < 0 {
		return
	}
	mode, args := os.Args[index], os.Args[index+1:]
	events := os.NewFile(3, "authored-events")
	control := os.NewFile(4, "authored-control")
	emit := func(event byte) { _, _ = events.Write([]byte{event}) }
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	version := reflect.DeepEqual(args, []string{"--version"})
	if !sandboxDevHelperEnvironment(version) {
		os.Exit(31)
	}
	if version {
		emit('V')
		switch mode {
		case "versionwrong":
			_, _ = io.WriteString(os.Stdout, "stripe version 9.9.9\n")
		case "versionover":
			_, _ = io.WriteString(os.Stdout, strings.Repeat("x", sandboxDevCLILimit+100))
		case "versionerror":
			_, _ = io.WriteString(os.Stderr, "SYNTHETIC_PRIVATE_SENTINEL")
			os.Exit(32)
		case "versionwait":
			<-interrupt
			emit('S')
		default:
			_, _ = io.WriteString(os.Stdout, "stripe version 1.43.2\n")
		}
		os.Exit(0)
	}
	config := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "stripe.toml")
	if !reflect.DeepEqual(args, sandboxDevListenerArgs(config)) {
		os.Exit(33)
	}
	for _, dir := range []string{os.Getenv("HOME"), os.Getenv("XDG_CONFIG_HOME"), os.Getenv("TMPDIR")} {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
			os.Exit(34)
		}
	}
	info, err := os.Stat(config)
	body, readErr := os.ReadFile(config)
	if err != nil || readErr != nil || info.Mode().Perm() != 0600 || len(body) != 0 {
		os.Exit(35)
	}
	emit('L')
	if mode == "gated" || mode == "neverready" {
		start := make(chan struct{})
		go func() { var b [1]byte; _, _ = control.Read(b[:]); close(start) }()
		select {
		case <-start:
		case <-interrupt:
			emit('S')
			os.Exit(0)
		}
	}
	ready := sandboxDevTestReady()
	switch mode {
	case "unknown":
		ready = "SYNTHETIC_PRIVATE_SENTINEL\n"
	case "duplicate":
		ready = "Getting ready...\nGetting ready...\n" + ready
	case "prelude":
		ready = "> Getting ready...\n> " + ready
	case "wrongapi":
		ready = strings.ReplaceAll(ready, "2026-09-30.endive", "2025-01-01.wrong")
	case "oversize":
		ready = strings.Repeat("x", sandboxDevCLILimit+100) + "\n"
	case "eof":
		os.Exit(36)
	}
	_, _ = io.WriteString(os.Stderr, ready)
	emit('R')
	if mode == "noise" {
		_, _ = io.WriteString(os.Stdout, strings.Repeat(sandboxLocalTestKey, 8192))
		_, _ = io.WriteString(os.Stderr, strings.Repeat("SYNTHETIC_PRIVATE_EVENT", 8192))
	}
	commands := make(chan byte)
	go func() {
		var b [1]byte
		for {
			if _, err := control.Read(b[:]); err != nil {
				return
			}
			commands <- b[0]
		}
	}()
	for {
		select {
		case <-interrupt:
			emit('S')
			if mode == "delayedstop" {
				<-commands
			}
			emit('E')
			os.Exit(0)
		case command := <-commands:
			if command == 'E' {
				emit('E')
				os.Exit(37)
			}
			if command == 'D' {
				_ = os.Stderr.Close()
			}
		}
	}
}

func sandboxDevHelperEnvironment(version bool) bool {
	want := map[string]string{
		"PATH": "/usr/bin:/bin", "HOME": os.Getenv("HOME"), "XDG_CONFIG_HOME": os.Getenv("XDG_CONFIG_HOME"), "TMPDIR": os.Getenv("TMPDIR"),
		"LANG": "C", "LC_ALL": "C", "CI": "1", "OTEL_SDK_DISABLED": "true", "STRIPE_CLI_TELEMETRY_OPTOUT": "1", "DO_NOT_TRACK": "1",
	}
	if !version {
		want["STRIPE_API_KEY"] = sandboxLocalTestKey
	}
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if expected, ok := want[key]; !ok || expected != value {
			return false
		}
		delete(want, key)
	}
	return len(want) == 0
}

type sandboxDevHarness struct {
	dependencies sandboxDevDependencies
	events       <-chan byte
	control      *os.File
	root         atomic.Value // Private directory published independently of OS pipe events.
	output       bytes.Buffer
}

func newSandboxDevHarness(t *testing.T, mode string) *sandboxDevHarness {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal("DEV_TEST_EXECUTABLE")
	}
	eventRead, eventWrite, err := os.Pipe()
	if err != nil {
		t.Fatal("DEV_TEST_EVENT_PIPE")
	}
	controlRead, controlWrite, err := os.Pipe()
	if err != nil {
		t.Fatal("DEV_TEST_CONTROL_PIPE")
	}
	events := make(chan byte, 32)
	go func() {
		defer close(events)
		var b [1]byte
		for {
			if _, err := eventRead.Read(b[:]); err != nil {
				return
			}
			events <- b[0]
		}
	}()
	h := &sandboxDevHarness{events: events, control: controlWrite}
	h.dependencies = sandboxDevDependencies{
		volumePresent: func() bool { return false }, resolve: func() (string, error) { return "/authored/stripe", nil },
		readKey: func() (string, error) { return sandboxLocalTestKey, nil },
		command: func(path string, args ...string) *exec.Cmd {
			if path != "/authored/stripe" {
				panic("SYNTHETIC_PRIVATE_SENTINEL")
			}
			cmd := exec.Command(binary, append([]string{"-test.run=^TestSandboxDevCLIHelper$", "--", mode}, args...)...)
			cmd.ExtraFiles = []*os.File{eventWrite, controlRead}
			return cmd
		},
		app: func(ctx context.Context, key, secret string, bound func([]byte)) error {
			if key != sandboxLocalTestKey || secret != sandboxLocalTestSecret {
				return sandboxDevFailure("stripe_dev_app", "authored_private_handoff")
			}
			bound([]byte("authored-public-cert"))
			<-ctx.Done()
			return nil
		},
		probe: func(context.Context, []byte) error { return nil }, output: &h.output,
		versionWait: 5 * time.Second, readyWait: 5 * time.Second,
	}
	// Capture the private root from the closed child env before Start, protected
	// by the runtime command invocation and accessed after command completion.
	command := h.dependencies.command
	h.dependencies.command = func(path string, args ...string) *exec.Cmd {
		cmd := command(path, args...)
		if len(args) > 3 && args[0] == "--color" {
			h.root.Store(filepath.Dir(filepath.Dir(args[3])))
		}
		return cmd
	}
	t.Cleanup(func() {
		_ = eventWrite.Close()
		_ = eventRead.Close()
		_ = controlRead.Close()
		_ = controlWrite.Close()
	})
	return h
}

func (h *sandboxDevHarness) privateRoot() string {
	if root := h.root.Load(); root != nil {
		return root.(string)
	}
	return ""
}

func sandboxDevEvent(t *testing.T, h *sandboxDevHarness, want byte) {
	t.Helper()
	select {
	case got := <-h.events:
		if got != want {
			t.Fatal("DEV_TEST_HELPER_SEQUENCE")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("DEV_TEST_HELPER_TIMEOUT")
	}
}

func sandboxDevResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("DEV_TEST_SUPERVISOR_NOT_JOINED")
		return nil
	}
}

func TestSandboxDevRejectsOptionsAndVolumeBeforeInput(t *testing.T) {
	for _, args := range [][]string{{"--"}, {"--listen=127.0.0.1:9000"}, {"SYNTHETIC_PRIVATE_SENTINEL"}, nil} {
		dependencies := sandboxDevDependencies{volumePresent: func() bool { return true }}
		err := executeStripeSandboxDevWith(context.Background(), args, nil, dependencies)
		if err == nil {
			t.Fatal("DEV_TEST_OPTIONS_ACCEPTED")
		}
		assertClosedRuntime(t, err)
	}
	// Actual dispatch refuses arguments without touching inputs or resolving CLI.
	if err := executeContext(context.Background(), []string{"stripe-sandbox-dev", "--secret=SYNTHETIC_PRIVATE_SENTINEL"}); err == nil || ledger.Failure(err).Code != codeSyntheticOptions {
		t.Fatal("DEV_TEST_EXPLICIT_DISPATCH")
	}
}

func TestSandboxDevVersionBeforeInputAndNoListenerOnRefusal(t *testing.T) {
	for _, mode := range []string{"versionwrong", "versionover", "versionerror", "versionwait"} {
		t.Run(mode, func(t *testing.T) {
			h := newSandboxDevHarness(t, mode)
			h.dependencies.versionWait = 500 * time.Millisecond
			h.dependencies.readKey = func() (string, error) { t.Error("DEV_TEST_INPUT_BEFORE_VERSION"); return "", nil }
			err := executeStripeSandboxDevWith(context.Background(), nil, nil, h.dependencies)
			if err == nil || h.output.Len() != 0 {
				t.Fatal("DEV_TEST_VERSION_ACCEPTED")
			}
			assertClosedRuntime(t, err)
			sandboxDevEvent(t, h, 'V')
			if mode == "versionwait" {
				sandboxDevEvent(t, h, 'S')
			}
		})
	}
	for _, key := range []string{"sk_live_Synthetic123", "sk_test_offline_fixture_only", "SYNTHETIC_PRIVATE_SENTINEL"} {
		h := newSandboxDevHarness(t, "ready")
		h.dependencies.readKey = func() (string, error) { return key, nil }
		if err := executeStripeSandboxDevWith(context.Background(), nil, nil, h.dependencies); err == nil {
			t.Fatal("DEV_TEST_KEY_ACCEPTED")
		}
		sandboxDevEvent(t, h, 'V')
		if h.privateRoot() != "" {
			t.Fatal("DEV_TEST_LISTENER_BEFORE_VALID_INPUT")
		}
	}
}

func TestSandboxDevReadinessRefusalsNeverStartApp(t *testing.T) {
	for _, mode := range []string{"unknown", "duplicate", "wrongapi", "oversize", "eof", "neverready"} {
		t.Run(mode, func(t *testing.T) {
			h := newSandboxDevHarness(t, mode)
			h.dependencies.readyWait = 500 * time.Millisecond
			h.dependencies.app = func(context.Context, string, string, func([]byte)) error {
				t.Error("DEV_TEST_APP_BEFORE_READY")
				return nil
			}
			err := executeStripeSandboxDevWith(context.Background(), nil, nil, h.dependencies)
			if err == nil || h.output.Len() != 0 {
				t.Fatal("DEV_TEST_BAD_READINESS_ACCEPTED")
			}
			assertClosedRuntime(t, err)
			if _, err := os.Stat(h.privateRoot()); !os.IsNotExist(err) {
				t.Fatal("DEV_TEST_JOINED_CLI_FILES_RETAINED")
			}
		})
	}
}

func TestSandboxDevReadyHandoffDrainAndDiscard(t *testing.T) {
	for _, mode := range []string{"gated", "prelude", "noise"} {
		t.Run(mode, func(t *testing.T) {
			h := newSandboxDevHarness(t, mode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started, probed, draining, release := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			h.dependencies.app = func(ctx context.Context, key, secret string, bound func([]byte)) error {
				if key != sandboxLocalTestKey || secret != sandboxLocalTestSecret {
					t.Error("DEV_TEST_PRIVATE_HANDOFF")
				}
				close(started)
				bound([]byte("authored-public-cert"))
				<-ctx.Done()
				close(draining)
				<-release
				return nil
			}
			h.dependencies.probe = func(_ context.Context, der []byte) error {
				if string(der) != "authored-public-cert" {
					t.Error("DEV_TEST_PUBLIC_CERT_HANDOFF")
				}
				close(probed)
				return nil
			}
			done := make(chan error, 1)
			go func() { done <- executeStripeSandboxDevWith(ctx, nil, nil, h.dependencies) }()
			sandboxDevEvent(t, h, 'V')
			sandboxDevEvent(t, h, 'L')
			if mode == "gated" {
				select {
				case <-started:
					t.Fatal("DEV_TEST_APP_BEFORE_CLI_READY")
				default:
				}
				_, _ = h.control.Write([]byte{'R'})
			}
			sandboxDevEvent(t, h, 'R')
			sandboxAwait(t, started)
			sandboxAwait(t, probed)
			cancel()
			sandboxAwait(t, draining)
			sandboxNotDone(t, done)
			select {
			case <-h.events:
				t.Fatal("DEV_TEST_CLI_SIGNALLED_BEFORE_APP_JOIN")
			default:
			}
			if info, err := os.Stat(h.privateRoot()); err != nil || info.Mode().Perm() != 0700 {
				t.Fatal("DEV_TEST_PRIVATE_FILES_REMOVED_BEFORE_APP_JOIN")
			}
			_ = filepath.WalkDir(h.privateRoot(), func(path string, entry os.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return err
				}
				body, err := os.ReadFile(path)
				if err != nil || strings.Contains(string(body), sandboxLocalTestKey) || strings.Contains(string(body), sandboxLocalTestSecret) {
					t.Error("DEV_TEST_SECRET_FILE")
				}
				return nil
			})
			close(release)
			if err := sandboxDevResult(t, done); err != nil {
				t.Fatal("DEV_TEST_NORMAL_CANCELLATION_FAILED")
			}
			sandboxDevEvent(t, h, 'S')
			sandboxDevEvent(t, h, 'E')
			// Cancellation may race the fixed marker write after probe. No secret,
			// event or success text other than this single marker is allowed.
			if h.output.String() != "" && h.output.String() != sandboxDevMarker {
				t.Fatal("DEV_TEST_RAW_COMPANION_OUTPUT")
			}
			if _, err := os.Stat(h.privateRoot()); !os.IsNotExist(err) {
				t.Fatal("DEV_TEST_JOINED_PRIVATE_FILES_REMAIN")
			}
		})
	}
}

func TestSandboxDevCompanionExitAndAppPrimaryWin(t *testing.T) {
	for _, command := range []byte{'E', 'D'} {
		h := newSandboxDevHarness(t, "ready")
		started, draining, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		primary := sandboxLocalBindFailure()
		h.dependencies.app = func(ctx context.Context, _, _ string, bound func([]byte)) error {
			close(started)
			bound(nil)
			<-ctx.Done()
			close(draining)
			<-release
			return primary
		}
		done := make(chan error, 1)
		go func() { done <- executeStripeSandboxDevWith(context.Background(), nil, nil, h.dependencies) }()
		sandboxDevEvent(t, h, 'V')
		sandboxDevEvent(t, h, 'L')
		sandboxDevEvent(t, h, 'R')
		sandboxAwait(t, started)
		_, _ = h.control.Write([]byte{command})
		sandboxAwait(t, draining)
		sandboxNotDone(t, done)
		if _, err := os.Stat(h.privateRoot()); err != nil {
			t.Fatal("DEV_TEST_COMPANION_EARLY_CLEANUP")
		}
		close(release)
		if sandboxDevResult(t, done) != primary {
			t.Fatal("DEV_TEST_APP_PRIMARY_LOST")
		}
	}
}

func TestSandboxDevJoinsChildBeforeRemovingPrivateFiles(t *testing.T) {
	h := newSandboxDevHarness(t, "delayedstop")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probed := make(chan struct{})
	h.dependencies.probe = func(context.Context, []byte) error { close(probed); return nil }
	done := make(chan error, 1)
	go func() { done <- executeStripeSandboxDevWith(ctx, nil, nil, h.dependencies) }()
	sandboxDevEvent(t, h, 'V')
	sandboxDevEvent(t, h, 'L')
	sandboxDevEvent(t, h, 'R')
	sandboxAwait(t, probed)
	cancel()
	sandboxDevEvent(t, h, 'S')
	sandboxNotDone(t, done)
	if _, err := os.Stat(h.privateRoot()); err != nil {
		t.Fatal("DEV_TEST_FILES_REMOVED_BEFORE_CHILD_WAIT")
	}
	_, _ = h.control.Write([]byte{'J'})
	if sandboxDevResult(t, done) != nil {
		t.Fatal("DEV_TEST_CHILD_JOIN")
	}
}

type sandboxDevBadOutput struct{ panicWrite bool }

func (w sandboxDevBadOutput) Write([]byte) (int, error) {
	if w.panicWrite {
		panic("SYNTHETIC_PRIVATE_SENTINEL")
	}
	return 0, errors.New("SYNTHETIC_PRIVATE_SENTINEL")
}

func TestSandboxDevProbeOutputAndPanicFailuresJoin(t *testing.T) {
	for _, mode := range []string{"probe", "probe_panic", "output", "output_panic", "app_panic", "app_error"} {
		t.Run(mode, func(t *testing.T) {
			h := newSandboxDevHarness(t, "ready")
			var joined atomic.Bool
			app := h.dependencies.app
			h.dependencies.app = func(ctx context.Context, key, secret string, bound func([]byte)) error {
				defer joined.Store(true)
				if mode == "app_panic" {
					panic("SYNTHETIC_PRIVATE_SENTINEL")
				}
				if mode == "app_error" {
					return errors.New("SYNTHETIC_PRIVATE_SENTINEL")
				}
				return app(ctx, key, secret, bound)
			}
			if mode == "probe" {
				h.dependencies.probe = func(context.Context, []byte) error {
					return sandboxDevFailure("stripe_dev_probe", "authored_probe_refusal")
				}
			}
			if mode == "probe_panic" {
				h.dependencies.probe = func(context.Context, []byte) error { panic("SYNTHETIC_PRIVATE_SENTINEL") }
			}
			if strings.HasPrefix(mode, "output") {
				h.dependencies.output = sandboxDevBadOutput{mode == "output_panic"}
			}
			err := executeStripeSandboxDevWith(context.Background(), nil, nil, h.dependencies)
			if err == nil || !joined.Load() || h.output.Len() != 0 {
				t.Fatal("DEV_TEST_FAILURE_NOT_JOINED_OR_CLASSIFIED")
			}
			assertClosedRuntime(t, err)
		})
	}
}

func TestSandboxDevCancellationBeforeReadiness(t *testing.T) {
	h := newSandboxDevHarness(t, "neverready")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.dependencies.app = func(context.Context, string, string, func([]byte)) error {
		t.Error("DEV_TEST_CANCEL_STARTED_APP")
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- executeStripeSandboxDevWith(ctx, nil, nil, h.dependencies) }()
	sandboxDevEvent(t, h, 'V')
	sandboxDevEvent(t, h, 'L')
	cancel()
	if sandboxDevResult(t, done) != nil || h.output.Len() != 0 {
		t.Fatal("DEV_TEST_CANCEL_BECAME_FAILURE")
	}
	sandboxDevEvent(t, h, 'S')
}

// io.Writer is serialized here only to let a test await the one marker without
// reading a bytes.Buffer concurrently with the supervisor.
type sandboxDevMarkerOutput struct {
	mu    sync.Mutex
	bytes []byte
	wrote chan struct{}
}

func (w *sandboxDevMarkerOutput) Write(body []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.bytes = append(w.bytes, body...)
	close(w.wrote)
	return len(body), nil
}

func TestSandboxDevEmitsOnlyOneMarkerAfterProbe(t *testing.T) {
	h := newSandboxDevHarness(t, "ready")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := &sandboxDevMarkerOutput{wrote: make(chan struct{})}
	h.dependencies.output = output
	probeEntered, allowProbe := make(chan struct{}), make(chan struct{})
	h.dependencies.probe = func(context.Context, []byte) error { close(probeEntered); <-allowProbe; return nil }
	done := make(chan error, 1)
	go func() { done <- executeStripeSandboxDevWith(ctx, nil, nil, h.dependencies) }()
	sandboxAwait(t, probeEntered)
	select {
	case <-output.wrote:
		t.Fatal("DEV_TEST_MARKER_BEFORE_PROBE")
	default:
	}
	close(allowProbe)
	sandboxAwait(t, output.wrote)
	cancel()
	if sandboxDevResult(t, done) != nil || string(output.bytes) != sandboxDevMarker {
		t.Fatal("DEV_TEST_MARKER_NOT_SINGLE_FIXED_VALUE")
	}
}

func TestSandboxDevFixedListenerArgumentsAndEnvironment(t *testing.T) {
	private := sandboxDevPrivate{root: "/authored", home: "/authored/home", configHome: "/authored/config", tmp: "/authored/tmp", config: "/authored/config/stripe.toml"}
	want := []string{"--color", "off", "--config", private.config, "--device-name", "possums-local-sandbox", "listen", "--skip-update", "--latest", "--events", "checkout.session.completed,checkout.session.async_payment_succeeded", "--forward-to", "http://127.0.0.1:8844/payments/stripe/webhook"}
	if !reflect.DeepEqual(sandboxDevListenerArgs(private.config), want) {
		t.Fatal("DEV_TEST_FIXED_CLI_ARGUMENTS")
	}
	wantEnv := []string{"PATH=/usr/bin:/bin", "HOME=/authored/home", "XDG_CONFIG_HOME=/authored/config", "TMPDIR=/authored/tmp", "LANG=C", "LC_ALL=C", "CI=1", "OTEL_SDK_DISABLED=true", "STRIPE_CLI_TELEMETRY_OPTOUT=1", "DO_NOT_TRACK=1"}
	if !reflect.DeepEqual(private.environment(""), wantEnv) || !reflect.DeepEqual(private.environment(sandboxLocalTestKey), append(wantEnv, "STRIPE_API_KEY="+sandboxLocalTestKey)) {
		t.Fatal("DEV_TEST_CLOSED_CLI_ENVIRONMENT")
	}
}

func TestSandboxDevVersionCancellationJoinsWithoutReadingInput(t *testing.T) {
	h := newSandboxDevHarness(t, "versionwait")
	h.dependencies.readKey = func() (string, error) { t.Error("DEV_TEST_CANCEL_READ_INPUT"); return "", nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- executeStripeSandboxDevWith(ctx, nil, nil, h.dependencies) }()
	sandboxDevEvent(t, h, 'V')
	cancel()
	if sandboxDevResult(t, done) != nil {
		t.Fatal("DEV_TEST_VERSION_CANCEL_FAILED")
	}
	sandboxDevEvent(t, h, 'S')
}

func TestSandboxDevEarlyHostFailuresAreClosed(t *testing.T) {
	for _, stage := range []string{"resolve", "version_spawn", "listener_spawn", "read", "read_panic"} {
		t.Run(stage, func(t *testing.T) {
			h := newSandboxDevHarness(t, "ready")
			switch stage {
			case "resolve":
				h.dependencies.resolve = func() (string, error) { return "", errors.New("SYNTHETIC_PRIVATE_SENTINEL") }
			case "version_spawn", "listener_spawn":
				command := h.dependencies.command
				h.dependencies.command = func(path string, args ...string) *exec.Cmd {
					if stage == "version_spawn" || args[0] != "--version" {
						return exec.Command("/authored/nonexistent-cli")
					}
					return command(path, args...)
				}
			case "read":
				h.dependencies.readKey = func() (string, error) { return "", errors.New("SYNTHETIC_PRIVATE_SENTINEL") }
			case "read_panic":
				h.dependencies.readKey = func() (string, error) { panic("SYNTHETIC_PRIVATE_SENTINEL") }
			}
			err := executeStripeSandboxDevWith(context.Background(), nil, nil, h.dependencies)
			if err == nil || h.output.Len() != 0 {
				t.Fatal("DEV_TEST_HOST_FAILURE_NOT_CLASSIFIED")
			}
			assertClosedRuntime(t, err)
		})
	}
}
