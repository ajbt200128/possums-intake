package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"possums-intake/internal/testfixture"
	"strings"
	"testing"
	"time"

	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
	"possums-intake/internal/telemetry"
)

type panicMetricsShutdown struct{ joined bool }

func (s *panicMetricsShutdown) Shutdown() {
	s.joined = true
	panic("SYNTHETIC_PRIVATE_SENTINEL")
}

func TestMetricsIndependentSetupShutdownOwners(t *testing.T) {
	for _, state := range []telemetry.State{telemetry.InvalidConfig, telemetry.SDKUnavailable} {
		o := &runtimeObserver{}
		observeMetricsSetup(state, o)
		s := &panicMetricsShutdown{}
		observeMetricsShutdown(s, o)
		o.want(t, failuretype.RuntimeSetupUnknown, failuretype.RuntimeShutdownUnknown)
		if !s.joined {
			t.Fatal("shutdown was not called")
		}
		// SDKUnavailable is a state seam: the public factory has no injection.
		observeMetricsSetup(state, nil)
		observeMetricsShutdown(s, nil)
	}
	o := &runtimeObserver{}
	observeMetricsSetup(telemetry.Disabled, o)
	observeMetricsSetup(telemetry.Enabled, o)
	observeMetricsShutdown(nil, o)
	o.want(t)
	for _, supplied := range []*telemetry.Runtime{nil, {}} {
		if independentMetricsObserver(supplied) != nil {
			t.Fatal("a supplied runtime became its own telemetry fault sink")
		}
		observeMetricsSetup(telemetry.InvalidConfig, supplied)
		observeMetricsShutdown(&panicMetricsShutdown{}, supplied)
	}
	err := shutdownMetrics(&panicMetricsShutdown{})
	if err == nil || ledger.Failure(err).Kind() != failuretype.RuntimeShutdownUnknown {
		t.Fatal("nil collector lost mandatory classification")
	}
	assertClosedRuntime(t, err)
}

func TestMetricsActualInvalidConfigExecuteKeepsAccountingAvailable(t *testing.T) {
	t.Setenv("LEDGER_ADMIN_KEY", "SYNTHETIC_ADMIN_KEY_AT_LEAST_32_BYTES")
	t.Setenv("OTEL_SDK_DISABLED", "false")
	t.Setenv("INTAKE_METRICS_ENDPOINT", "SYNTHETIC_PRIVATE_SENTINEL")
	t.Setenv("INTAKE_METRICS_HONEYCOMB_API_KEY", "SYNTHETIC_PRIVATE_FAKE_KEY")
	// InvalidConfig exits New before creating an SDK/exporter or outgoing work.
	runtime := telemetry.New(context.Background())
	if runtime.State() != telemetry.InvalidConfig || shutdownMetrics(runtime) != nil {
		t.Fatal("actual invalid configuration path unavailable")
	}
	dir := filepath.Join(t.TempDir(), "state")
	if ledger.Init(context.Background(), dir) != nil || initializeWebsiteStateObserved(context.Background(), dir, nil) != nil {
		t.Fatal("state fixture unavailable")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("listener fixture unavailable")
	}
	defer listener.Close()
	args := []string{"serve", "--state", dir, "--listen", listener.Addr().String()}
	o := &runtimeObserver{}
	err = executeObserved(context.Background(), args, o)
	if err == nil || ledger.Failure(err).Code != codeBind {
		t.Fatal("invalid telemetry replaced the actual listener result")
	}
	o.want(t, failuretype.RuntimeSetupUnknown, failuretype.RuntimeServeUnknown)
	assertClosedRuntime(t, err)
	// No independent observer legitimately means unavailable telemetry delivery.
	if err = executeObserved(context.Background(), args, nil); err == nil || ledger.Failure(err).Code != codeBind {
		t.Fatal("nil observer changed startup policy")
	}
	s, err := ledger.Open(context.Background(), dir)
	if err != nil || !s.Ready() {
		t.Fatal("telemetry failure changed accounting readiness")
	}
	if s.Close() != nil {
		t.Fatal("actual close unavailable")
	}
}

func assertClosedRuntime(t *testing.T, err error) {
	t.Helper()
	if drain, ok := err.(*unconfirmedHTTPDrain); ok {
		err = drain.Diagnostic
	}
	body, marshalErr := json.Marshal(ledger.Failure(err))
	if marshalErr != nil || strings.Contains(string(body), "SYNTHETIC_PRIVATE") || strings.Contains(string(body), "context canceled") {
		t.Fatal("runtime classification exposed hostile cause")
	}
}

type coordinatedHTTP struct {
	serveEntered, serveRelease, serveJoined chan struct{}
	drainEntered, drainRelease, drainJoined chan struct{}
	serveErr, drainErr                      error
	panicDrain                              bool
}

func (s *coordinatedHTTP) Serve(listener net.Listener) error {
	defer close(s.serveJoined)
	defer listener.Close()
	close(s.serveEntered)
	<-s.serveRelease
	return s.serveErr
}
func (s *coordinatedHTTP) Shutdown(context.Context) error {
	defer close(s.drainJoined)
	close(s.drainEntered)
	<-s.drainRelease
	if s.panicDrain {
		panic("SYNTHETIC_PRIVATE_SENTINEL")
	}
	return s.drainErr
}

type coordinatedCleanup struct {
	entered, release, joined chan struct{}
	err                      error
}

func (s coordinatedCleanup) Ready() bool { return false }
func (s coordinatedCleanup) Cleanup(ctx context.Context, _ bool) (ledger.CleanupResult, error) {
	defer close(s.joined)
	close(s.entered)
	<-s.release
	return ledger.CleanupResult{}, s.err
}

func TestCoordinatorCombinedPrimarySecondaryFaultsJoinAllWorkers(t *testing.T) {
	for _, route := range []string{"serve", "maintenance", "cancel"} {
		for _, same := range []bool{false, true} {
			for _, panicDrain := range []bool{false, true} {
				if same && panicDrain {
					continue
				}
				t.Run(route+"/"+map[bool]string{false: "distinct", true: "same"}[same]+"/"+map[bool]string{false: "error", true: "panic"}[panicDrain], func(t *testing.T) {
					listener, err := net.Listen("tcp", "127.0.0.1:0")
					if err != nil {
						t.Fatal("listener fixture unavailable")
					}
					ctx, stop := context.WithCancel(context.Background())
					defer stop()
					work, cancel := context.WithCancel(ctx)
					defer cancel()
					cause := error(errors.New("SYNTHETIC_PRIVATE_SENTINEL"))
					if same {
						cause = context.Canceled
					}
					h := &coordinatedHTTP{make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), cause, cause, panicDrain}
					m := coordinatedCleanup{make(chan struct{}), make(chan struct{}), make(chan struct{}), cause}
					ticks := make(chan time.Time, 1)
					ticks <- time.Now()
					o := &runtimeObserver{}
					done := make(chan error, 1)
					go func() { done <- coordinateServeObserved(ctx, work, cancel, m, h, listener, ticks, time.Now(), o) }()
					<-h.serveEntered
					<-m.entered
					wantRoot := failuretype.RuntimeShutdownUnknown
					switch route {
					case "serve":
						close(h.serveRelease)
						wantRoot = failuretype.RuntimeServeUnknown
					case "maintenance":
						close(m.release)
						wantRoot = failuretype.MaintenanceUnknown
					case "cancel":
						stop()
					}
					if same {
						wantRoot = failuretype.Cancelled
					}
					<-h.drainEntered
					if work.Err() != context.Canceled {
						t.Fatal("drain began before owned work cancellation")
					}
					select {
					case <-done:
						t.Fatal("coordinator abandoned drain")
					default:
					}
					close(h.drainRelease)
					<-h.drainJoined
					if route != "serve" {
						select {
						case <-done:
							t.Fatal("coordinator abandoned serve")
						default:
						}
						close(h.serveRelease)
					}
					<-h.serveJoined
					if route != "maintenance" {
						select {
						case <-done:
							t.Fatal("coordinator abandoned maintenance")
						default:
						}
						close(m.release)
					}
					err = <-done
					<-m.joined
					if ledger.FailureKind(err, failuretype.RuntimeServeUnknown) != wantRoot || !httpDrainUnconfirmed(err) {
						t.Fatal("primary or scratch-retention drain evidence lost")
					}
					assertClosedRuntime(t, err)
					o.mu.Lock()
					counts := map[failuretype.Type]int{}
					for _, kind := range o.kinds {
						counts[kind]++
					}
					valid := o.invalid == 0 && len(o.kinds) == 3
					o.mu.Unlock()
					if !valid {
						t.Fatal("combined fault population differs")
					}
					if same {
						if counts[failuretype.Cancelled] != 3 {
							t.Fatal("independent same-category faults were deduplicated")
						}
					} else if counts[failuretype.RuntimeServeUnknown] != 1 || counts[failuretype.RuntimeShutdownUnknown] != 1 || counts[failuretype.MaintenanceUnknown] != 1 {
						t.Fatal("combined closed categories differ")
					}
				})
			}
		}
	}
}

// An active caller's cancellation/deadline errors remain operational failures.
func TestMaintenanceOperationalCancellationAndDeadlineStillCount(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		ctx, cancel := context.WithCancel(context.Background())
		f := nonfatalCleanup{true, cause, make(chan struct{}, 2)}
		ticks := make(chan time.Time)
		done := make(chan error, 1)
		o := &runtimeObserver{}
		go func() { done <- maintainObserved(ctx, f, ticks, time.Now(), o) }()
		for range 2 {
			ticks <- time.Now()
			<-f.runs
		}
		cancel()
		if <-done != nil {
			t.Fatal("nonfatal cancellation policy changed")
		}
		kind := ledger.FailureKind(cause, failuretype.MaintenanceUnknown)
		o.want(t, kind, kind)
	}
}

type cancelledRealCleanup struct {
	*ledger.Service
	entered, release, joined chan struct{}
}

func (s cancelledRealCleanup) Cleanup(ctx context.Context, prune bool) (ledger.CleanupResult, error) {
	defer close(s.joined)
	close(s.entered)
	<-ctx.Done()
	<-s.release
	return s.Service.Cleanup(ctx, prune)
}

func TestCoordinatorNormalCancellationRealCleanupExcludedAndJoined(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if ledger.Init(context.Background(), dir) != nil {
		t.Fatal("state fixture unavailable")
	}
	s, err := ledger.Open(context.Background(), dir)
	if err != nil {
		t.Fatal("service fixture unavailable")
	}
	defer s.Close()
	if _, err := testfixture.Create(context.Background(), s, "synthetic000000"); err != nil {
		t.Fatal("account fixture unavailable")
	}
	before, err := s.Balance(context.Background(), "synthetic000000")
	if err != nil {
		t.Fatal("balance fixture unavailable")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("listener fixture unavailable")
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	m := cancelledRealCleanup{s, make(chan struct{}), make(chan struct{}), make(chan struct{})}
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()
	o := &runtimeObserver{}
	done := make(chan error, 1)
	go func() {
		done <- coordinateServeObserved(ctx, work, cancel, m, localServer(work, http.NotFoundHandler()), listener, ticks, time.Now(), o)
	}()
	<-m.entered
	stop()
	<-work.Done()
	select {
	case <-done:
		t.Fatal("normal shutdown abandoned real Cleanup")
	default:
	}
	close(m.release)
	if <-done != nil {
		t.Fatal("normal shutdown changed return policy")
	}
	<-m.joined
	o.want(t)
	after, err := s.Balance(context.Background(), "synthetic000000")
	if err != nil || before.AccountBalance != after.AccountBalance || before.Available != after.Available || !s.Ready() {
		t.Fatal("normal cancellation changed money/readiness")
	}
	// The caller closes storage only after confirmed HTTP and worker joins.
	if s.Close() != nil {
		t.Fatal("joined service close unavailable")
	}
}

var _ failureowner.Observer = (*runtimeObserver)(nil)
var _ httpLifecycle = (*http.Server)(nil)
