package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/failuretype"
)

type lifecycleObserver struct {
	kinds   []failuretype.Type
	invalid int
}

func (o *lifecycleObserver) ObserveFailure(kind failuretype.Type) { o.kinds = append(o.kinds, kind) }
func (o *lifecycleObserver) InvalidateFailures()                  { o.invalid++ }
func (o *lifecycleObserver) want(t *testing.T, want ...failuretype.Type) {
	t.Helper()
	if o.invalid != 0 || len(o.kinds) != len(want) {
		t.Fatal("lifecycle observation population differs")
	}
	for i, kind := range want {
		if o.kinds[i] != kind {
			t.Fatal("lifecycle category differs")
		}
	}
}

func TestLifecycleActualInitOpenAndCloseOwners(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	o := &lifecycleObserver{}
	if err := InitObserved(background, dir, o); err != nil {
		t.Fatal("actual initialization unavailable")
	}
	o.want(t)
	if err := InitObserved(background, dir, o); err == nil {
		t.Fatal("nonempty initialization allowed")
	}
	o.want(t, failuretype.InitializationUnknown)
	o = &lifecycleObserver{}
	s, err := OpenObserved(background, dir, o)
	if err != nil {
		t.Fatal("actual open unavailable")
	}
	o.want(t)
	// Two actual supported lifecycle hooks observe independent typed failures.
	// The outer hook preserves the already observed close result explicitly.
	inner := s.app.OnBootstrapClear().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		return context.DeadlineExceeded
	})
	outer := s.app.OnTerminate().BindFunc(func(e *core.TerminateEvent) error {
		return errors.Join(e.Next(), context.Canceled)
	})
	defer s.app.OnBootstrapClear().Unbind(inner)
	defer s.app.OnTerminate().Unbind(outer)
	err = s.CloseObserved(o)
	if err == nil || Failure(err).Code != CodeClose || Failure(err).Kind() != failuretype.DeadlineExceeded || s.Ready() {
		t.Fatal("typed close primary or readiness lost")
	}
	o.want(t, failuretype.DeadlineExceeded, failuretype.Cancelled)
	body, marshalErr := json.Marshal(Failure(err))
	if marshalErr != nil || strings.Contains(string(body), "context") {
		t.Fatal("closure leaked cause text")
	}
}

func TestLifecycleActualPreflightFailureAndAbsentStateOwners(t *testing.T) {
	dir := t.TempDir()
	o := &lifecycleObserver{}
	if s, err := OpenObserved(background, dir, o); s != nil || err == nil {
		t.Fatal("absent state exposed service")
	}
	o.want(t, failuretype.StorageOpenUnknown)
	for _, name := range []string{"data.db", "auxiliary.db"} {
		if os.WriteFile(filepath.Join(dir, name), []byte("SYNTHETIC_PRIVATE_SENTINEL"), 0600) != nil {
			t.Fatal("storage fixture unavailable")
		}
	}
	o = &lifecycleObserver{}
	s, err := OpenObserved(background, dir, o)
	if s != nil || err == nil || Failure(err).Outcome != "unavailable" || Failure(err).Stage != "state_validation" {
		t.Fatal("failed validation fabricated evidence")
	}
	o.want(t, failuretype.SQLiteStorageFailure)
}

func TestLifecycleActualCancelledInitIsOwnedOnceWithoutObserver(t *testing.T) {
	for _, observed := range []bool{false, true} {
		ctx, cancel := context.WithCancel(background)
		cancel()
		o := &lifecycleObserver{}
		dir := filepath.Join(t.TempDir(), "state")
		var err error
		if observed {
			err = InitObserved(ctx, dir, o)
		} else {
			err = Init(ctx, dir)
		}
		if err == nil || !Failure(err).Kind().Valid() || Failure(err).Kind() != failuretype.Cancelled {
			t.Fatal("disabled classification lost")
		}
		if observed {
			o.want(t, failuretype.Cancelled)
		}
	}
}

func TestLifecycleSyntheticClosureJoinPanicAndIntegritySeams(t *testing.T) {
	for _, fixture := range []struct {
		clear   func() error
		want    []failuretype.Type
		invalid bool
	}{
		{func() error { return errors.New("SYNTHETIC_PRIVATE_SENTINEL") }, []failuretype.Type{failuretype.StorageCloseUnknown}, false},
		{func() error { panic("SYNTHETIC_PRIVATE_SENTINEL") }, []failuretype.Type{failuretype.StorageCloseUnknown}, false},
		{func() error { return errors.Join(context.Canceled, context.Canceled) }, []failuretype.Type{failuretype.Cancelled, failuretype.Cancelled}, false},
		{func() error {
			return errors.Join(context.Canceled, context.Canceled, context.Canceled, context.Canceled, context.Canceled)
		}, nil, true},
	} {
		o := &lifecycleObserver{}
		err := clearStorage(fixture.clear, o)
		if err == nil || Failure(err).Code != CodeClose || !Failure(err).Kind().Valid() {
			t.Fatal("closure classification unavailable")
		}
		if fixture.invalid {
			if o.invalid != 1 || len(o.kinds) != 0 {
				t.Fatal("unsupported close join emitted partial cells")
			}
		} else {
			o.want(t, fixture.want...)
		}
	}
}

func TestLifecycleRunReportPrimaryAndPostRunDecisionSyntheticSeam(t *testing.T) {
	for _, primary := range []bool{false, true} {
		_, report := WithFailureReport(background)
		report.used.Store(true)
		b := transactionFailures{primary: primary}
		b.add(failuretype.PolicyRefused)
		report.publish(b)
		o := &lifecycleObserver{}
		ObserveRunFailures(o, report, diagnostic(CodeExpired), failuretype.MaintenanceUnknown)
		if primary {
			o.want(t, failuretype.PolicyRefused)
		} else {
			o.want(t, failuretype.PolicyRefused, failuretype.PolicyRefused)
		}
		ObserveRunFailures(o, report, diagnostic(CodeExpired), failuretype.MaintenanceUnknown)
		if o.invalid != 1 {
			t.Fatal("report reuse was not unavailable")
		}
	}
}
