package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"possums-intake/internal/failuretype"
)

// Order is not part of the aggregate contract; distinct occurrences are.
func requireFailureCounts(t *testing.T, spy *failureSpy, want ...failuretype.Type) {
	t.Helper()
	spy.mu.Lock()
	defer spy.mu.Unlock()
	counts := map[failuretype.Type]int{}
	for _, kind := range want {
		counts[kind]++
	}
	if spy.invalidFailures != 0 || len(spy.kinds) != len(want) {
		t.Fatal("distinct observed failure was lost or evidence unexpectedly invalidated")
	}
	for _, kind := range spy.kinds {
		counts[kind]--
	}
	for _, count := range counts {
		if count != 0 {
			t.Fatal("failure categories do not match observed independent causes")
		}
	}
}

func TestHTTPExpiryDecisionAndCompletionFaultBothCount(t *testing.T) {
	s := newTestLedger(t)
	h := newTestHandler(t, s, nil)
	provision(t, h, "a")
	credit(t, h, "a", "10", 1)
	headers := authHeaders("application/json")
	headers["Idempotency-Key"] = key(2)
	out := request(t, h, "POST", "/reservations", headers, `{"account_id":"a","maximum":"5","timeout_seconds":1}`)
	requireStatus(t, out, 200)
	id := decodeMap(t, out)["reservation"].(map[string]any)["reservation_id"].(string)
	time.Sleep(1100 * time.Millisecond)
	// Test-only app seam, retaining actual Service/PocketBase completion.
	injectHTTPCompletionApp(t, s, 1, false)
	spy := &failureSpy{}
	out = postJSON(t, observedHandler(t, s, spy), "/reservations/settle", `{"reservation_id":"`+id+`","charged_amount":"1"}`)
	requireStatus(t, out, 409)
	body := decodeMap(t, out)
	if body["outcome"] != "expired" || body["reservation"].(map[string]any)["state"] != "expired" {
		t.Fatal("authoritative expired result was erased")
	}
	requireFailureCounts(t, spy, failuretype.TransactionAfterCompletionUnknown, failuretype.PolicyRefused)
}

func TestHTTPOuterPanicIsDistinctFromObservedAccountingRefusal(t *testing.T) {
	s := newTestLedger(t)
	spy := &failureSpy{}
	wrapped := &misuseReportAccounting{Accounting: s, panicAfter: true}
	out := postJSON(t, observedHandler(t, wrapped, spy), "/balance", `{"account_id":"missing"}`)
	requireStatus(t, out, 503)
	closedDiagnostic(t, out)
	requireFailureCounts(t, spy, failuretype.AccountMissing, failuretype.RequestDispatchUnknown)
}

func TestHTTPAmbientCancellationDoesNotExplainIndependentDeliveryFault(t *testing.T) {
	for _, panicWriter := range []bool{false, true} {
		spy := &failureSpy{}
		ctx, cancel := context.WithCancel(testContext)
		cancel()
		w := &deliveryFaultWriter{failedWriter: failedWriter{header: http.Header{}}}
		if panicWriter {
			w.panicValue = "SYNTHETIC_PRIVATE_INDEPENDENT_WRITER_PANIC"
		} else {
			w.err = noFormatError{}
		}
		observedHandler(t, stubLedger{err: context.Canceled}, spy).ServeHTTP(w, failureRequest("/balance", `{"account_id":"a"}`).WithContext(ctx))
		requireFailureCounts(t, spy, failuretype.Cancelled, failuretype.ResponseDeliveryUnknown)
	}
}

func TestHTTPAmbientCancellationDoesNotExplainUnknownReadFault(t *testing.T) {
	spy := &failureSpy{}
	ctx, cancel := context.WithCancel(testContext)
	cancel()
	r := failureRequest("/balance", "").WithContext(ctx)
	r.Body = readFault{err: noFormatError{}}
	out := httptest.NewRecorder()
	observedHandler(t, panicLedger{}, spy).ServeHTTP(out, r)
	requireStatus(t, out, 503)
	requireFailureCounts(t, spy, failuretype.RequestReadUnknown)
}
