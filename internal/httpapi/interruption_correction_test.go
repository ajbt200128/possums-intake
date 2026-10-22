package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
	"possums-intake/internal/telemetry"
)

// Preserve the actual Service.run/report while moving only its caller to a
// cancelled subcontext. The HTTP request context is intentionally still active.
type subcontextCancellationAccounting struct{ ledger.Accounting }

func (s subcontextCancellationAccounting) Balance(ctx context.Context, id string) (ledger.BalanceSnapshot, error) {
	child, cancel := context.WithCancel(ctx)
	cancel()
	return s.Accounting.Balance(child, id)
}

func TestHTTPPrimarySubcontextInterruptionAndDeliveryAreDistinct(t *testing.T) {
	for _, budgetDeadline := range []bool{false, true} {
		t.Run(fmt.Sprintf("work_budget_deadline_%t", budgetDeadline), func(t *testing.T) {
			s := newTestLedger(t)
			createAccountFixture(t, newTestHandler(t, s, nil), "aaaaaaaaaaaaaaa")
			var accounting ledger.Accounting = subcontextCancellationAccounting{s}
			cause := context.Canceled
			if budgetDeadline {
				injectHTTPCompletionFaultApp(t, s, httpCompletionApp{delayWork: true})
				accounting = s
				cause = context.DeadlineExceeded
			}
			r := failureRequest("/balance", `{"account_id":"aaaaaaaaaaaaaaa"}`)
			spy := &failureSpy{}
			w := &deliveryFaultWriter{failedWriter: failedWriter{header: http.Header{}}, err: cause}
			observedHandler(t, accounting, spy).ServeHTTP(w, r)
			kind := ledger.FailureKind(cause, 0)
			requireFailureCounts(t, spy, kind, kind)
			if r.Context().Err() != nil || spy.classes[0] != telemetry.Unknown || !s.Ready() {
				t.Fatal("internal interruption changed caller or inferred receipt")
			}
		})
	}
}

func TestHTTPFakePrimaryInterruptionWithoutCallerProofCountsDistinctDelivery(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, cancelDuringWrite := range []bool{false, true} {
			ctx, cancel := context.WithCancel(testContext)
			spy := &failureSpy{}
			w := &deliveryFaultWriter{failedWriter: failedWriter{header: http.Header{}}, err: cause}
			if cancelDuringWrite {
				w.cancel = cancel
			}
			observedHandler(t, stubLedger{err: cause}, spy).ServeHTTP(w, failureRequest("/balance", `{"account_id":"aaaaaaaaaaaaaaa"}`).WithContext(ctx))
			cancel()
			kind := ledger.FailureKind(cause, 0)
			requireFailureCounts(t, spy, kind, kind)
		}
	}
}

func TestHTTPActualCallerInterruptionAndConsequentTypedDeliveryCountOnce(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		s := newTestLedger(t)
		ctx, cancel := context.WithCancel(testContext)
		if cause == context.DeadlineExceeded {
			cancel()
			ctx, cancel = context.WithDeadline(testContext, time.Now().Add(-time.Second))
		}
		cancel()
		spy := &failureSpy{}
		w := &deliveryFaultWriter{failedWriter: failedWriter{header: http.Header{}}, err: fmt.Errorf("SYNTHETIC_PRIVATE_SENTINEL: %w", cause)}
		observedHandler(t, s, spy).ServeHTTP(w, failureRequest("/balance", `{"account_id":"aaaaaaaaaaaaaaa"}`).WithContext(ctx))
		requireFailureCounts(t, spy, ledger.FailureKind(cause, 0))
		if spy.classes[0] != telemetry.Unknown || !s.Ready() {
			t.Fatal("caller interruption inferred receipt or changed readiness")
		}
	}
}

func TestHTTPReadInterruptionProofIsCapturedBeforeDelivery(t *testing.T) {
	for _, fixture := range []struct {
		name              string
		cause             error
		cancelBeforeRead  bool
		cancelDuringWrite bool
		requestDeadline   bool
		panicRead         bool
		shared            bool
	}{
		{"caller_cancel", context.Canceled, true, false, false, false, true},
		{"caller_cancel_panic", context.Canceled, true, false, false, true, true},
		{"caller_deadline", context.DeadlineExceeded, false, false, true, false, true},
		{"caller_deadline_panic", context.DeadlineExceeded, false, false, true, true, true},
		{"reader_cancel_caller_active", context.Canceled, false, false, false, false, false},
		{"reader_deadline_caller_active", context.DeadlineExceeded, false, false, false, false, false},
		{"reader_cancel_writer_later_cancels", context.Canceled, false, true, false, false, false},
		{"reader_deadline_caller_cancelled", context.DeadlineExceeded, true, false, false, false, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(testContext)
			if fixture.requestDeadline {
				cancel()
				ctx, cancel = context.WithDeadline(testContext, time.Now().Add(-time.Second))
			}
			defer cancel()
			if fixture.cancelBeforeRead {
				cancel()
			}
			r := failureRequest("/balance", "").WithContext(ctx)
			r.Body = readFault{err: fmt.Errorf("SYNTHETIC_PRIVATE_SENTINEL: %w", fixture.cause), panicRead: fixture.panicRead}
			w := &deliveryFaultWriter{failedWriter: failedWriter{header: http.Header{}}, err: fixture.cause}
			if fixture.cancelDuringWrite {
				w.cancel = cancel
			}
			spy := &failureSpy{}
			observedHandler(t, panicLedger{}, spy).ServeHTTP(w, r)
			kind := ledger.FailureKind(fixture.cause, 0)
			want := []failuretype.Type{kind}
			if !fixture.shared {
				want = append(want, kind)
			}
			requireFailureCounts(t, spy, want...)
			if spy.classes[0] != telemetry.Unknown {
				t.Fatal("reader/writer interruption inferred receipt")
			}
		})
	}
}

// A later wrapper decision is not a run primary, even if its category and
// caller context match. TakeWithPrimary must clear the tentative return marker.
type postRunCancellationAccounting struct {
	ledger.Accounting
	cancel context.CancelFunc
}

func (s postRunCancellationAccounting) Balance(ctx context.Context, id string) (ledger.BalanceSnapshot, error) {
	row, err := s.Accounting.Balance(ctx, id)
	if err != nil {
		return row, err
	}
	s.cancel()
	return row, context.Canceled
}

func TestHTTPPostRunInterruptionCannotSupplyPrimaryRequestProof(t *testing.T) {
	s := newTestLedger(t)
	createAccountFixture(t, newTestHandler(t, s, nil), "aaaaaaaaaaaaaaa")
	injectHTTPCompletionFaultApp(t, s, httpCompletionApp{count: 1, cause: context.Canceled})
	ctx, cancel := context.WithCancel(testContext)
	defer cancel()
	spy := &failureSpy{}
	w := &deliveryFaultWriter{failedWriter: failedWriter{header: http.Header{}}, err: context.Canceled}
	accounting := postRunCancellationAccounting{Accounting: s, cancel: cancel}
	observedHandler(t, accounting, spy).ServeHTTP(w, failureRequest("/balance", `{"account_id":"aaaaaaaaaaaaaaa"}`).WithContext(ctx))
	requireFailureCounts(t, spy, failuretype.Cancelled, failuretype.Cancelled, failuretype.Cancelled)
}

func TestHTTPUnprovenEqualInterruptionCategoriesRetainFiveSlotBound(t *testing.T) {
	b := &responseBuffer{}
	for range 5 {
		b.deliveryFailure(failuretype.Cancelled)
	}
	spy := &failureSpy{}
	observeFailures(spy, b.failures)
	requireFailureCounts(t, spy, failuretype.Cancelled, failuretype.Cancelled, failuretype.Cancelled, failuretype.Cancelled, failuretype.Cancelled)
	b.deliveryFailure(failuretype.Cancelled)
	spy = &failureSpy{}
	observeFailures(spy, b.failures)
	if len(spy.kinds) != 0 || spy.invalidFailures != 1 {
		t.Fatal("equal-category overflow was silently deduplicated or truncated")
	}
}

func TestHTTPSecondaryCompletionInterruptionAndDeliveryAreDistinct(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, cancelDuringWrite := range []bool{false, true} {
			t.Run(fmt.Sprintf("category_%d_cancel_during_write_%t", ledger.FailureKind(cause, 0), cancelDuringWrite), func(t *testing.T) {
				s := newTestLedger(t)
				createAccountFixture(t, newTestHandler(t, s, nil), "aaaaaaaaaaaaaaa")
				injectHTTPCompletionFaultApp(t, s, httpCompletionApp{count: 1, cause: cause})
				ctx, cancel := context.WithCancel(testContext)
				defer cancel()
				w := &deliveryFaultWriter{failedWriter: failedWriter{header: http.Header{}}, err: fmt.Errorf("SYNTHETIC_PRIVATE_SENTINEL: %w", cause)}
				if cancelDuringWrite {
					w.cancel = cancel
				}
				spy := &failureSpy{}
				identity := key(1)
				r := failureRequest("/credits", `{"account_id":"aaaaaaaaaaaaaaa","amount":"10"}`).WithContext(ctx)
				r.Header.Set("Idempotency-Key", identity)
				observedHandler(t, s, spy).ServeHTTP(w, r)
				kind := ledger.FailureKind(cause, 0)
				requireFailureCounts(t, spy, kind, kind)
				if (!cancelDuringWrite && ctx.Err() != nil) || spy.classes[0] != telemetry.Unknown {
					t.Fatal("independent writer failure changed caller state or inferred receipt")
				}
				row, err := s.Credit(testContext, ledger.CreditInput{AccountID: "aaaaaaaaaaaaaaa", Amount: "10", IdempotencyKey: identity})
				balance, balanceErr := s.Balance(testContext, "aaaaaaaaaaaaaaa")
				if err != nil || balanceErr != nil || row.IdempotencyKey != identity || row.BalanceAfter != "10" || balance.Available != "10" || !s.Ready() {
					t.Fatal("hook and writer faults erased or duplicated committed credit")
				}
			})
		}
	}
}
