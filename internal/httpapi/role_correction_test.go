package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
	"possums-intake/internal/telemetry"
)

func TestHTTPExpiryPostRunRoleWithEqualCategoryDeliveryAndOverflow(t *testing.T) {
	for _, fixture := range []struct {
		name     string
		hooks    int
		policy   bool
		delivery bool
		encoding bool
	}{
		{"equal_policy_categories", 1, true, false, false},
		{"hook_and_expiry_and_delivery", 1, false, true, false},
		{"five_slots", 3, false, true, false},
		{"six_occurrences_invalidate", 3, false, true, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
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
			app := httpCompletionApp{count: fixture.hooks}
			hookKind := failuretype.TransactionAfterCompletionUnknown
			if fixture.policy {
				app.cause = &ledger.Diagnostic{Code: ledger.CodeExpired, Stage: testKey, Outcome: testKey, NextStep: testKey}
				hookKind = failuretype.PolicyRefused
			}
			injectHTTPCompletionFaultApp(t, s, app)
			spy := &failureSpy{}
			observed := observedHandler(t, s, spy)
			if fixture.encoding {
				observed.routes["/reservations/settle"] = func(w http.ResponseWriter, r *http.Request) {
					observed.settle(w, r)
					writeJSON(w, 200, syntheticMarshaler{})
				}
			}
			body := `{"reservation_id":"` + id + `","charged_amount":"1"}`
			if fixture.delivery {
				observed.ServeHTTP(&failedWriter{header: http.Header{}, mode: "short"}, failureRequest("/reservations/settle", body))
				if spy.classes[0] != telemetry.Unknown {
					t.Fatal("delivery loss inferred terminal success")
				}
			} else {
				out = postJSON(t, observed, "/reservations/settle", body)
				requireStatus(t, out, 409)
				wire := decodeMap(t, out)
				row := wire["reservation"].(map[string]any)
				exactKeys(t, wire, "code", "stage", "constraint", "outcome", "next_step", "reservation")
				exactKeys(t, row, "reservation_id", "idempotency_key", "account_id", "maximum", "timeout_seconds", "initial_expires_at", "expires_at", "state", "charged_amount", "terminal_at", "retain_until")
				if wire["outcome"] != "expired" || row["state"] != "expired" || row["charged_amount"] != "0" || row["terminal_at"] != row["expires_at"] {
					t.Fatal("post-run role altered canonical authoritative expiry row")
				}
			}
			if fixture.encoding {
				if len(spy.kinds) != 0 || spy.invalidFailures != 1 || spy.invalidRequests != 0 {
					t.Fatal("six occurrences produced truncated counts instead of invalidity")
				}
			} else {
				want := []failuretype.Type{failuretype.PolicyRefused}
				for range fixture.hooks {
					want = append(want, hookKind)
				}
				if fixture.delivery {
					want = append(want, failuretype.ResponseDeliveryUnknown)
				}
				requireFailureCounts(t, spy, want...)
			}
			row, err := s.Lookup(testContext, id)
			balance, balanceErr := s.Balance(testContext, "a")
			if err != nil || balanceErr != nil || row.State != ledger.Expired || row.ChargedAmount == nil || *row.ChargedAmount != "0" || balance.Available != "10" || !s.Ready() {
				t.Fatal("hook/expiry/delivery/overflow reporting altered committed accounting")
			}
		})
	}
}

type propagationAccounting struct{ ledger.Accounting }

func (s propagationAccounting) Balance(ctx context.Context, id string) (ledger.BalanceSnapshot, error) {
	row, err := s.Accounting.Balance(ctx, id)
	if err != nil {
		err = fmt.Errorf("SYNTHETIC_PRIVATE_SENTINEL: %w", err)
	}
	return row, err
}

func TestHTTPReportPropagationIsNotIndependentOuterPanic(t *testing.T) {
	s := newTestLedger(t)
	spy := &failureSpy{}
	out := postJSON(t, observedHandler(t, propagationAccounting{s}, spy), "/balance", `{"account_id":"missing"}`)
	requireStatus(t, out, 404)
	closedDiagnostic(t, out)
	requireFailureCounts(t, spy, failuretype.AccountMissing)
	for _, mode := range []string{"encoded", "encoding_before_body", "encoding_after_body"} {
		spy := &failureSpy{}
		h := observedHandler(t, s, spy)
		h.routes["/balance"] = func(w http.ResponseWriter, r *http.Request) {
			if mode == "encoding_before_body" {
				accountingInvoked(w)
				_, err := s.Balance(r.Context(), "missing")
				accountingReturned(w, r, err)
			} else {
				h.balance(w, r) // encoded authoritative refusal before later faults
			}
			if mode != "encoded" {
				writeJSON(w, 200, syntheticMarshaler{})
			}
			panic("SYNTHETIC_PRIVATE_SENTINEL independent outer panic")
		}
		out := postJSON(t, h, "/balance", `{"account_id":"missing"}`)
		d := closedDiagnostic(t, out)
		want := []failuretype.Type{failuretype.AccountMissing, failuretype.RequestDispatchUnknown}
		if mode == "encoding_before_body" {
			requireStatus(t, out, 503)
			if d["stage"] != "response_encoding" || d["outcome"] != "unknown" {
				t.Fatal("replacement fabricated accounting outcome")
			}
		} else {
			requireStatus(t, out, 404)
			if d["code"] != string(ledger.CodeAccount) || d["outcome"] != "no_effect" {
				t.Fatal("later panic erased encoded refusal")
			}
		}
		if mode != "encoded" {
			want = append(want, failuretype.ResponseEncodingUnknown)
		}
		requireFailureCounts(t, spy, want...)
		if spy.classes[0] != telemetry.Unknown {
			t.Fatal("panic inferred terminal success")
		}
	}
}

func TestHTTPAmbientCancelledReadPanicRemainsUnknownAndTypedCauseSurvives(t *testing.T) {
	for _, fixture := range []struct {
		reader readFault
		want   failuretype.Type
	}{
		{readFault{panicRead: true}, failuretype.RequestReadUnknown},
		{readFault{err: noFormatError{}, panicRead: true}, failuretype.RequestReadUnknown},
		{readFault{err: fmt.Errorf("SYNTHETIC_PRIVATE_SENTINEL: %w", context.Canceled)}, failuretype.Cancelled},
		{readFault{err: fmt.Errorf("SYNTHETIC_PRIVATE_SENTINEL: %w", context.DeadlineExceeded), panicRead: true}, failuretype.DeadlineExceeded},
	} {
		ctx, cancel := context.WithCancel(testContext)
		cancel()
		r := failureRequest("/balance", "").WithContext(ctx)
		r.Body = fixture.reader
		spy := &failureSpy{}
		out := httptest.NewRecorder()
		observedHandler(t, panicLedger{}, spy).ServeHTTP(out, r)
		requireStatus(t, out, 503)
		d := closedDiagnostic(t, out)
		if d["stage"] != "request_read" || d["outcome"] != "no_effect" {
			t.Fatal("read fault invented accounting work")
		}
		requireFailureCounts(t, spy, fixture.want)
		if spy.classes[0] != telemetry.Unknown {
			t.Fatal("ambient cancellation inferred delivered terminal")
		}
	}
}

func TestHTTPUnknownCancelledWriterFaultPreservesCommittedCredit(t *testing.T) {
	for _, panicWriter := range []bool{false, true} {
		s := newTestLedger(t)
		provision(t, newTestHandler(t, s, nil), "a")
		ctx, cancel := context.WithCancel(testContext)
		defer cancel()
		w := &deliveryFaultWriter{failedWriter: failedWriter{header: http.Header{}}, cancel: cancel}
		if panicWriter {
			w.panicValue = "SYNTHETIC_PRIVATE_SENTINEL"
		} else {
			w.err = noFormatError{}
		}
		spy := &failureSpy{}
		r := failureRequest("/credits", `{"account_id":"a","amount":"10"}`).WithContext(ctx)
		r.Header.Set("Idempotency-Key", key(1))
		observedHandler(t, s, spy).ServeHTTP(w, r)
		requireFailureCounts(t, spy, failuretype.ResponseDeliveryUnknown)
		if spy.classes[0] != telemetry.Unknown {
			t.Fatal("writer fault inferred receipt")
		}
		balance, err := s.Balance(testContext, "a")
		if err != nil || balance.Available != "10" || !s.Ready() {
			t.Fatal("unknown writer fault erased committed credit")
		}
	}
}
