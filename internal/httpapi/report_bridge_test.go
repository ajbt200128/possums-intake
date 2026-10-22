package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"possums-intake/internal/testfixture"
	"strings"
	"testing"
	"time"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
	"possums-intake/internal/telemetry"
)

func TestHTTPRealServiceReportBridgeAndPreRunRefusals(t *testing.T) {
	s := newTestLedger(t)
	createAccountFixture(t, newTestHandler(t, s, nil), "aaaaaaaaaaaaaaa")
	for _, fixture := range []struct {
		path, body, idempotency string
		status                  int
		kind                    failuretype.Type
	}{
		// Both before-run refusals legally leave the report unused.
		{"/balance", `{"account_id":""}`, "", 400, failuretype.RequestInvalid},
		{"/credits", `{"account_id":"aaaaaaaaaaaaaaa","amount":"0"}`, key(1), 400, failuretype.RequestInvalid},
		{"/credits", `{"account_id":"aaaaaaaaaaaaaaa","amount":"1"}`, "bad-key", 400, failuretype.IdempotencyRefused},
		// Original in-run refusal is counted from the report, not again from
		// the returned primary. Every request owns a fresh one-run context.
		{"/balance", `{"account_id":"missing00000000"}`, "", 404, failuretype.AccountMissing},
		{"/reservations", `{"account_id":"aaaaaaaaaaaaaaa","maximum":"1","timeout_seconds":600}`, key(2), 409, failuretype.AdmissionRefused},
		{"/reservations/lookup", `{"reservation_id":"` + strings.Repeat("a", 32) + `"}`, "", 410, failuretype.EvidenceUnavailable},
	} {
		spy := &failureSpy{}
		h := observedHandler(t, s, spy)
		headers := authHeaders("application/json")
		if fixture.idempotency != "" {
			headers["Idempotency-Key"] = fixture.idempotency
		}
		out := request(t, h, "POST", fixture.path, headers, fixture.body)
		requireStatus(t, out, fixture.status)
		expectFailures(t, spy, fixture.kind)
	}
	spy := &failureSpy{}
	h := observedHandler(t, s, spy)
	postJSON(t, h, "/balance", `{"account_id":"aaaaaaaaaaaaaaa"}`)
	credit(t, h, "aaaaaaaaaaaaaaa", "10", 3)
	postJSON(t, h, "/balance", `{"account_id":"aaaaaaaaaaaaaaa"}`)
	expectFailures(t, spy) // valid report-zero is actual success, not an error
	if len(spy.classes) != 3 {
		t.Fatal("success terminal population changed")
	}
}

func TestHTTPRealSQLiteConstraintIsReportedOnceWithoutRawText(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if ledger.Init(testContext, dir) != nil {
		t.Fatal("fixture init unavailable")
	}
	s, err := ledger.Open(testContext, dir)
	if err != nil {
		t.Fatal("fixture service unavailable")
	}
	t.Cleanup(func() { _ = s.Close() })
	createAccountFixture(t, newTestHandler(t, s, nil), "aaaaaaaaaaaaaaa")
	// A real pinned SQLite driver error, not a forged diagnostic. The fixture
	// trigger is synthetic-only and exists outside the HTTP implementation.
	db, err := sql.Open("sqlite", filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatal("fixture SQL connection unavailable")
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TRIGGER synthetic_refuse_credit BEFORE UPDATE OF balance ON accounts BEGIN SELECT RAISE(ABORT,'SYNTHETIC_PRIVATE_SENTINEL'); END`)
	if err != nil {
		t.Fatal("fixture trigger unavailable")
	}
	spy := &failureSpy{}
	h := observedHandler(t, s, spy)
	headers := authHeaders("application/json")
	headers["Idempotency-Key"] = key(1)
	out := request(t, h, "POST", "/credits", headers, `{"account_id":"aaaaaaaaaaaaaaa","amount":"1"}`)
	requireStatus(t, out, 503)
	body := closedDiagnostic(t, out)
	if body["code"] != string(ledger.CodeStorage) || body["outcome"] != "no_effect" {
		t.Fatal("known rollback evidence lost")
	}
	expectFailures(t, spy, failuretype.SQLiteConstraint)
	var count int
	if db.QueryRow(`SELECT count(*) FROM ledger_credits`).Scan(&count) != nil || count != 0 {
		t.Fatal("diagnostics retried or mutated accounting")
	}
}

type wrappedExpiry struct{ ledger.Accounting }

func (s wrappedExpiry) Settle(ctx context.Context, in ledger.SettleInput) (ledger.Reservation, error) {
	row, err := s.Accounting.Settle(ctx, in)
	if err != nil {
		err = fmt.Errorf("SYNTHETIC_PRIVATE_SENTINEL: %w", err)
	}
	return row, err
}

func TestHTTPExpiredPolicyAfterSuccessfulRunAndWrappedRow(t *testing.T) {
	s := newTestLedger(t)
	h := newTestHandler(t, s, nil)
	createAccountFixture(t, h, "aaaaaaaaaaaaaaa")
	credit(t, h, "aaaaaaaaaaaaaaa", "10", 1)
	headers := authHeaders("application/json")
	headers["Idempotency-Key"] = key(2)
	out := request(t, h, "POST", "/reservations", headers, `{"account_id":"aaaaaaaaaaaaaaa","maximum":"5","timeout_seconds":1}`)
	requireStatus(t, out, 200)
	id := decodeMap(t, out)["reservation"].(map[string]any)["reservation_id"].(string)
	time.Sleep(1100 * time.Millisecond)
	spy := &failureSpy{}
	out = postJSON(t, observedHandler(t, wrappedExpiry{s}, spy), "/reservations/settle", fmt.Sprintf(`{"reservation_id":%q,"charged_amount":"1"}`, id))
	requireStatus(t, out, 409)
	body := decodeMap(t, out)
	row := body["reservation"].(map[string]any)
	if body["outcome"] != "expired" || row["state"] != "expired" || row["reservation_id"] != id || row["charged_amount"] != "0" || strings.Contains(out.Body.String(), "SYNTHETIC_PRIVATE_SENTINEL") {
		t.Fatal("wrapped expiry did not preserve actual receipt")
	}
	expectFailures(t, spy, failuretype.PolicyRefused)
	// A purported expiry without an authoritative row cannot fabricate one.
	spy = &failureSpy{}
	fake := stubLedger{err: fmt.Errorf("SYNTHETIC_PRIVATE_SENTINEL: %w", &ledger.Diagnostic{Code: ledger.CodeExpired, Stage: testKey, Outcome: testKey, NextStep: testKey})}
	out = postJSON(t, observedHandler(t, fake, spy), "/reservations/settle", fmt.Sprintf(`{"reservation_id":%q,"charged_amount":"1"}`, id))
	body = closedDiagnostic(t, out)
	if body["outcome"] != "expired" {
		t.Fatal("canonical expired outcome lost")
	}
	expectFailures(t, spy, failuretype.PolicyRefused)
}

type misuseReportAccounting struct {
	ledger.Accounting
	reuse      bool
	panicAfter bool
	calls      int
}

func (s *misuseReportAccounting) Balance(ctx context.Context, id string) (ledger.BalanceSnapshot, error) {
	s.calls++
	row, err := s.Accounting.Balance(ctx, id)
	if s.reuse {
		_, _ = s.Accounting.Balance(ctx, id)
	} // intentional invalid test seam, never production retry
	if s.panicAfter {
		panic("SYNTHETIC_PRIVATE_SENTINEL after run")
	}
	return row, err
}

func TestHTTPReportInvalidationAndOuterPanicOwnership(t *testing.T) {
	s := newTestLedger(t)
	createAccountFixture(t, newTestHandler(t, s, nil), "aaaaaaaaaaaaaaa")
	for _, fixture := range []struct {
		reuse, panicAfter bool
		id                string
		kinds             []failuretype.Type
	}{
		{true, false, "aaaaaaaaaaaaaaa", nil},
		{false, true, "missing00000000", []failuretype.Type{failuretype.AccountMissing, failuretype.RequestDispatchUnknown}},
		{false, true, "aaaaaaaaaaaaaaa", []failuretype.Type{failuretype.RequestDispatchUnknown}},
	} {
		spy := &failureSpy{}
		wrapped := &misuseReportAccounting{Accounting: s, reuse: fixture.reuse, panicAfter: fixture.panicAfter}
		h := observedHandler(t, wrapped, spy)
		out := postJSON(t, h, "/balance", fmt.Sprintf(`{"account_id":%q}`, fixture.id))
		if wrapped.calls != 1 {
			t.Fatal("HTTP owner replayed Accounting")
		}
		if fixture.reuse {
			requireStatus(t, out, 200)
			if len(spy.kinds) != 0 || spy.invalidFailures != 1 || spy.invalidRequests != 0 || spy.classes[0] != telemetry.Status2xx {
				t.Fatal("invalid report contaminated terminal family or healthy zeros")
			}
		} else {
			requireStatus(t, out, 503)
			closedDiagnostic(t, out)
			requireFailureCounts(t, spy, fixture.kinds...)
			if spy.classes[0] != telemetry.Unknown {
				t.Fatal("panic inferred successful terminal delivery")
			}
		}
	}
	// A missing response AFTER an actual success is still a dispatch fault.
	spy := &failureSpy{}
	h := observedHandler(t, s, spy)
	h.routes["/balance"] = func(w http.ResponseWriter, r *http.Request) {
		accountingInvoked(w)
		_, err := s.Balance(r.Context(), "aaaaaaaaaaaaaaa")
		accountingReturned(w, r, err)
	}
	postJSON(t, h, "/balance", `{"account_id":"aaaaaaaaaaaaaaa"}`)
	expectFailures(t, spy, failuretype.RequestDispatchUnknown)
	if spy.invalidRequests != 1 {
		t.Fatal("lost terminal classification was not independently invalidated")
	}
}

func TestHTTPReportTakeOnceAndZeroSuccessLaterDispatchSourceSeam(t *testing.T) {
	s := newTestLedger(t)
	if _, err := testfixture.Create(testContext, s, "aaaaaaaaaaaaaaa"); err != nil {
		t.Fatal("fixture account unavailable")
	}
	ctx, report := ledger.WithFailureReport(testContext)
	if _, err := s.Balance(ctx, "aaaaaaaaaaaaaaa"); err != nil {
		t.Fatal("fixture accounting unavailable")
	}
	b := &responseBuffer{header: http.Header{}, accountingInvoked: true, accountingReturned: true}
	b.collectAccounting(report)
	if b.failures.invalid || b.failures.count != 0 {
		t.Fatal("valid zero report was unavailable")
	}
	b.collectAccounting(report)
	if !b.failures.invalid {
		t.Fatal("duplicate consumption became healthy zero")
	}
	spy := &failureSpy{}
	h := observedHandler(t, s, spy)
	h.routes["/balance"] = func(w http.ResponseWriter, r *http.Request) {
		accountingInvoked(w)
		row, err := s.Balance(r.Context(), "aaaaaaaaaaaaaaa")
		accountingReturned(w, r, err)
		writeJSON(w, 200, balanceResponse{Available: string(row.Available), AccountBalance: []accountJSON{toAccount(row.AccountBalance)}, PendingReservations: []reservationJSON{}})
		panic("SYNTHETIC_PRIVATE_SENTINEL after encoded result")
	}
	out := postJSON(t, h, "/balance", `{"account_id":"aaaaaaaaaaaaaaa"}`)
	requireStatus(t, out, 200)
	if decodeMap(t, out)["available"] != "0" {
		t.Fatal("dispatch panic erased known result")
	}
	expectFailures(t, spy, failuretype.RequestDispatchUnknown)
	if spy.classes[0] != telemetry.Unknown {
		t.Fatal("later panic inferred terminal success")
	}
	// No report on fake successful accounting is also valid; report absence
	// never forces legacy/fake Accounting implementations to implement run.
	spy = &failureSpy{}
	postJSON(t, observedHandler(t, stubLedger{}, spy), "/balance", `{"account_id":"aaaaaaaaaaaaaaa"}`)
	expectFailures(t, spy)
}

func TestHTTPReportOriginalAndDistinctDeliveryWithoutRecount(t *testing.T) {
	s := newTestLedger(t)
	spy := &failureSpy{}
	h := observedHandler(t, s, spy)
	w := &failedWriter{header: http.Header{}, mode: "short"}
	h.ServeHTTP(w, failureRequest("/balance", `{"account_id":"missing00000000"}`))
	expectFailures(t, spy, failuretype.AccountMissing, failuretype.ResponseDeliveryUnknown)
	// An unknown short write is independent of observed run cancellation.
	spy = &failureSpy{}
	ctx, cancel := context.WithCancel(testContext)
	cancel()
	observedHandler(t, s, spy).ServeHTTP(w, failureRequest("/balance", `{"account_id":"aaaaaaaaaaaaaaa"}`).WithContext(ctx))
	expectFailures(t, spy, failuretype.Cancelled, failuretype.ResponseDeliveryUnknown)
}

func TestHTTPContextRaceFinalDeliveryOwnership(t *testing.T) {
	for range 30 {
		spy := &failureSpy{}
		ctx, cancel := context.WithCancel(testContext)
		w := &deliveryFaultWriter{failedWriter: failedWriter{header: http.Header{}, mode: "short"}, cancel: cancel}
		observedHandler(t, stubLedger{}, spy).ServeHTTP(w, failureRequest("/balance", `{"account_id":"aaaaaaaaaaaaaaa"}`).WithContext(ctx))
		expectFailures(t, spy, failuretype.ResponseDeliveryUnknown)
		if spy.classes[0] != telemetry.Unknown {
			t.Fatal("raced cancellation became successful delivery")
		}
	}
}

type racingFlushWriter struct {
	noFlushWriter
	flushing chan struct{}
	resume   chan struct{}
}

func (w *racingFlushWriter) FlushError() error {
	close(w.flushing)
	<-w.resume
	return context.Canceled
}

func TestHTTPCancellationDuringSupportedFlushPreservesCommittedCredit(t *testing.T) {
	s := newTestLedger(t)
	createAccountFixture(t, newTestHandler(t, s, nil), "aaaaaaaaaaaaaaa")
	spy := &failureSpy{}
	h := observedHandler(t, s, spy)
	ctx, cancel := context.WithCancel(testContext)
	defer cancel()
	w := &racingFlushWriter{noFlushWriter: noFlushWriter{header: http.Header{}}, flushing: make(chan struct{}), resume: make(chan struct{})}
	r := failureRequest("/credits", `{"account_id":"aaaaaaaaaaaaaaa","amount":"10"}`).WithContext(ctx)
	r.Header.Set("Idempotency-Key", key(4))
	cancelled := make(chan struct{})
	go func() {
		<-w.flushing
		cancel()
		close(w.resume)
		close(cancelled)
	}()
	h.ServeHTTP(w, r)
	<-cancelled
	expectFailures(t, spy, failuretype.Cancelled)
	if spy.classes[0] != telemetry.Unknown {
		t.Fatal("flush cancellation established peer receipt")
	}
	row, err := s.Balance(testContext, "aaaaaaaaaaaaaaa")
	if err != nil || row.Available != "10" {
		t.Fatal("flush cancellation erased actual committed credit")
	}
}

func TestHTTPExpectedControlFlowPanicNotNewFailure(t *testing.T) {
	for _, err := range []error{http.ErrAbortHandler, http.ErrServerClosed, http.ErrNotSupported} {
		spy := &failureSpy{}
		h := observedHandler(t, stubLedger{}, spy)
		h.routes["/balance"] = func(http.ResponseWriter, *http.Request) { panic(err) }
		out := postJSON(t, h, "/balance", `{}`)
		requireStatus(t, out, 503)
		closedDiagnostic(t, out)
		expectFailures(t, spy)
	}
	// Classification helpers also tolerate hostile error inspection methods.
	if safeErrorIs(hostileUnwrap{}, errors.New("target")) || expectedHTTPError(hostileUnwrap{}) {
		t.Fatal("hostile unwrap manufactured a control-flow category")
	}
}
