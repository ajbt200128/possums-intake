package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

type runtimeObserver struct {
	mu      sync.Mutex
	kinds   []failuretype.Type
	invalid int
}

func (o *runtimeObserver) ObserveFailure(kind failuretype.Type) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.kinds = append(o.kinds, kind)
}
func (o *runtimeObserver) InvalidateFailures() { o.mu.Lock(); defer o.mu.Unlock(); o.invalid++ }
func (o *runtimeObserver) want(t *testing.T, want ...failuretype.Type) {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.invalid != 0 || len(o.kinds) != len(want) {
		t.Fatal("runtime failure population differs")
	}
	for i, kind := range want {
		if o.kinds[i] != kind {
			t.Fatal("runtime failure category differs")
		}
	}
}

type nonfatalCleanup struct {
	ready bool
	err   error
	runs  chan struct{}
}

func (f nonfatalCleanup) Ready() bool { return f.ready }
func (f nonfatalCleanup) Cleanup(context.Context, bool) (ledger.CleanupResult, error) {
	f.runs <- struct{}{}
	return ledger.CleanupResult{}, f.err
}
func TestMaintenanceEveryNonfatalInvocationClassifiesOffAndObserved(t *testing.T) {
	for _, observed := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		f := nonfatalCleanup{true, errors.New("SYNTHETIC_PRIVATE_SENTINEL"), make(chan struct{}, 2)}
		ticks := make(chan time.Time)
		done := make(chan error, 1)
		o := &runtimeObserver{}
		go func() {
			if observed {
				done <- maintainObserved(ctx, f, ticks, time.Now(), o)
			} else {
				done <- maintain(ctx, f, ticks, time.Now())
			}
		}()
		for range 2 {
			ticks <- time.Now()
			<-f.runs
		}
		cancel()
		if <-done != nil {
			t.Fatal("nonfatal classification changed cleanup policy")
		}
		if observed {
			o.want(t, failuretype.MaintenanceUnknown, failuretype.MaintenanceUnknown)
		}
	}
}
func TestMaintenanceActualCleanupReportFatalPropagationOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if ledger.Init(context.Background(), dir) != nil {
		t.Fatal("state fixture unavailable")
	}
	s, err := ledger.Open(context.Background(), dir)
	if err != nil {
		t.Fatal("service fixture unavailable")
	}
	if s.Close() != nil {
		t.Fatal("fixture close unavailable")
	}
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()
	o := &runtimeObserver{}
	err = maintainObserved(context.Background(), s, ticks, time.Now(), o)
	if err == nil || ledger.Failure(err).Kind() != failuretype.NotReady {
		t.Fatal("actual cleanup failure lost")
	}
	o.want(t, failuretype.NotReady)
}
func TestRuntimeKindsAreMandatoryAndServeBindOwnsOnce(t *testing.T) {
	for _, fixture := range []struct {
		code  ledger.Code
		stage string
		kind  failuretype.Type
	}{
		{codeAdminKey, "startup", failuretype.RequestInvalid},
		{codeScratch, "scratch_create", failuretype.RuntimeSetupUnknown},
		{codeScratchCleanup, "scratch_cleanup", failuretype.RuntimeShutdownUnknown},
		{codeBind, "listener_start", failuretype.RuntimeServeUnknown},
		{codeShutdown, "shutdown", failuretype.RuntimeShutdownUnknown},
	} {
		if d := runtimeFailure(fixture.code, fixture.stage, "closed", "unknown", "Inspect privately."); d.Kind() != fixture.kind {
			t.Fatal("runtime diagnostic lacks mandatory numeric classification")
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("bind fixture unavailable")
	}
	defer listener.Close()
	o := &runtimeObserver{}
	err = serveObserved(context.Background(), nonfatalCleanup{ready: true}, http.NotFoundHandler(), listener.Addr().String(), o)
	if err == nil || ledger.Failure(err).Code != codeBind {
		t.Fatal("actual bind failure lost")
	}
	o.want(t, failuretype.RuntimeServeUnknown)
}
func TestExecuteSetupAndLedgerPropagationDoNotRecount(t *testing.T) {
	o := &runtimeObserver{}
	if err := executeObserved(context.Background(), []string{"serve", "--state", "relative"}, o); err == nil || !ledger.Failure(err).Kind().Valid() {
		t.Fatal("setup classification absent")
	}
	o.want(t, failuretype.InitializationUnknown)
	o = &runtimeObserver{}
	t.Setenv("LEDGER_ADMIN_KEY", "SYNTHETIC_ADMIN_KEY_AT_LEAST_32_BYTES")
	if err := executeObserved(context.Background(), []string{"serve", "--state", filepath.Join(t.TempDir(), "absent")}, o); err == nil || !ledger.Failure(err).Kind().Valid() {
		t.Fatal("open classification absent")
	}
	o.want(t, failuretype.StorageOpenUnknown)
}
func TestMaintenanceSyntheticUnexpectedPanicIsClosed(t *testing.T) {
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()
	o := &runtimeObserver{}
	err := maintainObserved(context.Background(), panickingMaintenance{}, ticks, time.Now(), o)
	if err == nil || ledger.Failure(err).Kind() != failuretype.MaintenanceUnknown {
		t.Fatal("maintenance panic classification absent")
	}
	o.want(t, failuretype.MaintenanceUnknown)
}

type panickingMaintenance struct{}

func (panickingMaintenance) Ready() bool { return true }
func (panickingMaintenance) Cleanup(context.Context, bool) (ledger.CleanupResult, error) {
	panic("SYNTHETIC_PRIVATE_SENTINEL")
}
