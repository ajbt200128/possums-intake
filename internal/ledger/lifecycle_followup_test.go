package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
)

func assertClosedLifecycle(t *testing.T, err error) {
	t.Helper()
	body, marshalErr := json.Marshal(Failure(err))
	if marshalErr != nil || strings.Contains(string(body), "SYNTHETIC_PRIVATE") || strings.Contains(string(body), "context") {
		t.Fatal("lifecycle owner exposed private cause")
	}
}

func TestBootstrapDefaultOwnerRealHooksPrimaryAndTeardown(t *testing.T) {
	for _, observed := range []bool{false, true} {
		for _, same := range []bool{false, true} {
			for _, panicPrimary := range []bool{false, true} {
				dir := filepath.Join(t.TempDir(), "state")
				o := &lifecycleObserver{}
				var observer failureowner.Observer
				if observed {
					observer = o
				}
				var retained *core.BaseApp
				cleared := false
				primary, secondary := error(context.Canceled), error(context.DeadlineExceeded)
				if same {
					secondary = context.Canceled
				}
				wantPrimary := failuretype.Cancelled
				if panicPrimary {
					wantPrimary = failuretype.InitializationUnknown
				}
				app, err := bootstrapUsingObserved(true, observer, func() *core.BaseApp {
					retained = storageApp(dir, true)
					retained.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
						if err := e.Next(); err != nil {
							return err
						}
						if panicPrimary {
							panic("SYNTHETIC_PRIVATE_SENTINEL")
						}
						return primary
					})
					retained.OnBootstrapClear().BindFunc(func(e *core.BootstrapEvent) error {
						if err := e.Next(); err != nil {
							return err
						}
						cleared = true
						return secondary
					})
					return retained
				})
				if app != nil || err == nil || Failure(err).Kind() != wantPrimary || !cleared || retained.IsBootstrapped() {
					t.Fatal("bootstrap primary/teardown owner lost evidence")
				}
				assertClosedLifecycle(t, err)
				if observed {
					o.want(t, wantPrimary, FailureKind(secondary, failuretype.StorageCloseUnknown))
				}
				// Actual default app/hooks, not a real driver failed-close fixture.
				// Successful framework bootstrap created partial files: preserve them.
				if reopened, openErr := bootstrap(dir, false); openErr != nil {
					t.Fatal("bootstrap failure lost original partial files")
				} else if reopened.ClearBootstrap() != nil {
					t.Fatal("partial fixture close unavailable")
				}
			}
		}
	}
}

func TestOpenDefaultOwnerValidationAndRealTeardownPreserveMoney(t *testing.T) {
	for _, observed := range []bool{false, true} {
		for _, same := range []bool{false, true} {
			s, dir, now := testService(t)
			fund(t, s, "synthetic", "20", now)
			reserve(t, s, ReserveInput{"synthetic", "7", 1800, key(now, 2)})
			before := balance(t, s, "synthetic")
			if s.Close() != nil {
				t.Fatal("fixture close unavailable")
			}
			ctx, cancel := context.WithCancel(background)
			defer cancel()
			o := &lifecycleObserver{}
			var observer failureowner.Observer
			if observed {
				observer = o
			}
			secondary := error(context.DeadlineExceeded)
			if same {
				secondary = context.Canceled
			}
			var retained *core.BaseApp
			cleared := false
			opened, err := openObserved(ctx, dir, observer, func(dir string, create bool, o failureowner.Observer) (*core.BaseApp, error) {
				return bootstrapUsingObserved(create, o, func() *core.BaseApp {
					retained = storageApp(dir, create)
					retained.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
						if err := e.Next(); err != nil {
							return err
						}
						cancel() // after real preflight/bootstrap, before real validation
						return nil
					})
					retained.OnBootstrapClear().BindFunc(func(e *core.BootstrapEvent) error {
						if err := e.Next(); err != nil {
							return err
						}
						cleared = true
						return secondary
					})
					return retained
				})
			})
			if opened != nil || err == nil || Failure(err).Kind() != failuretype.Cancelled || !cleared || retained.IsBootstrapped() {
				t.Fatal("Open validation primary or secondary teardown lost")
			}
			if observed {
				o.want(t, failuretype.Cancelled, FailureKind(secondary, failuretype.StorageCloseUnknown))
			}
			assertClosedLifecycle(t, err)
			reopened, err := Open(background, dir)
			if err != nil {
				t.Fatal("preserved state reopen unavailable")
			}
			after := balance(t, reopened, "synthetic")
			if !reflect.DeepEqual(before, after) {
				t.Fatal("validation/teardown faults changed accounting")
			}
			if reopened.Close() != nil {
				t.Fatal("reopened close unavailable")
			}
		}
	}
}

func TestMaintenanceRealCleanupCallerAdmissionCancellationExcluded(t *testing.T) {
	s, _, _ := testService(t)
	ctx, cancel := context.WithCancel(background)
	s.gate <- struct{}{} // qualify real Cleanup while the writer is owned
	runCtx, report := WithFailureReport(ctx)
	done := make(chan error, 1)
	go func() { _, err := s.Cleanup(runCtx, false); done <- err }()
	cancel()
	err := <-done
	<-s.gate
	if err == nil || Failure(err).Code != CodeWriter || Failure(err).Kind() != failuretype.Cancelled || !s.Ready() {
		t.Fatal("real normal-cancellation evidence changed")
	}
	o := &lifecycleObserver{}
	if !ObserveMaintenanceFailures(ctx, o, report, err) {
		t.Fatal("run primary evidence absent")
	}
	o.want(t)
}

func TestMaintenanceCancellationExclusionUsesRolesNotCategories(t *testing.T) {
	ctx, cancel := context.WithCancel(background)
	cancel()
	for _, fixture := range []struct {
		code    Code
		primary bool
		want    []failuretype.Type
	}{
		{CodeWriter, true, []failuretype.Type{failuretype.Cancelled, failuretype.DeadlineExceeded}},
		{CodeWork, true, []failuretype.Type{failuretype.Cancelled, failuretype.Cancelled, failuretype.DeadlineExceeded}},
		{CodeWriter, false, []failuretype.Type{failuretype.Cancelled, failuretype.Cancelled, failuretype.DeadlineExceeded, failuretype.Cancelled}},
	} {
		_, report := WithFailureReport(ctx)
		report.used.Store(true)
		batch := transactionFailures{primary: fixture.primary}
		batch.add(failuretype.Cancelled)
		batch.add(failuretype.Cancelled)
		batch.add(failuretype.DeadlineExceeded)
		report.publish(batch)
		d := diagnostic(fixture.code)
		d.kind = failuretype.Cancelled
		o := &lifecycleObserver{}
		ObserveMaintenanceFailures(ctx, o, report, d)
		o.want(t, fixture.want...)
	}
	// Raw cancellation under ambient cancellation is ambiguous: do not guess
	// that an independent hook came from our caller. No report confirms origin.
	o := &lifecycleObserver{}
	ObserveMaintenanceFailures(ctx, o, nil, errors.Join(context.Canceled))
	o.want(t, failuretype.Cancelled)
}
