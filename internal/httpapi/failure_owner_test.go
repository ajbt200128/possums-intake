package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
	"possums-intake/internal/telemetry"
)

// This observer can retain only numeric categories/classes, not diagnostics or
// request fields. afterDelivery qualifies ordering without production knobs.
type failureSpy struct {
	mu                               sync.Mutex
	kinds                            []failuretype.Type
	classes                          []telemetry.StatusClass
	invalidFailures, invalidRequests int
	panicObserve, panicInvalidate    bool
	afterDelivery                    func()
}

func (s *failureSpy) ObserveFailure(kind failuretype.Type) {
	if s.afterDelivery != nil {
		s.afterDelivery()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kinds = append(s.kinds, kind)
	if s.panicObserve {
		panic("SYNTHETIC_PRIVATE_SENTINEL")
	}
}
func (s *failureSpy) InvalidateFailures() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidFailures++
	if s.panicInvalidate {
		panic("SYNTHETIC_PRIVATE_SENTINEL")
	}
}
func (s *failureSpy) ObserveTerminal(class telemetry.StatusClass, duration time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if duration < 0 {
		panic("invalid duration")
	}
	s.classes = append(s.classes, class)
}
func (s *failureSpy) InvalidateRequests() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidRequests++
}
func expectFailures(t *testing.T, s *failureSpy, want ...failuretype.Type) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.invalidFailures != 0 || len(s.kinds) != len(want) {
		t.Fatal("unexpected numeric failure count or integrity loss")
	}
	for i, kind := range want {
		if s.kinds[i] != kind {
			t.Fatalf("numeric failure slot %d: got %d, want %d", i, s.kinds[i], kind)
		}
	}
}

func TestHTTPFailureOwnerAuthParserAndPopulation(t *testing.T) {
	for _, route := range routesAndBodies {
		for _, fixture := range []struct {
			auth, body bool
			want       failuretype.Type
		}{
			{false, true, failuretype.AuthenticationRefused},
			{true, false, failuretype.RequestInvalid},
		} {
			spy := &failureSpy{}
			h := observedHandler(t, panicLedger{}, spy)
			headers := authHeaders("application/json")
			headers["Idempotency-Key"] = key(1)
			if !fixture.auth {
				delete(headers, "Authorization")
			}
			body := route.body
			if !fixture.body {
				body = `{}`
			}
			out := request(t, h, "POST", route.path, headers, body)
			closedDiagnostic(t, out)
			if route.path == "/reservations/renew" {
				expectFailures(t, spy)
				if len(spy.classes) != 0 {
					t.Fatal("renewal expanded telemetry population")
				}
				continue
			}
			expectFailures(t, spy, fixture.want)
			if len(spy.classes) != 1 || spy.classes[0] != telemetry.Status4xx {
				t.Fatal("terminal population changed")
			}
		}
	}
	for _, path := range []string{"/credits", "/reservations"} {
		spy := &failureSpy{}
		h := observedHandler(t, panicLedger{}, spy)
		postJSON(t, h, path, `{}`)
		expectFailures(t, spy, failuretype.IdempotencyRefused)
	}
	for _, header := range []string{"Authorization", "Content-Type", "Content-Encoding", "Idempotency-Key"} {
		spy := &failureSpy{}
		h := observedHandler(t, panicLedger{}, spy)
		r := failureRequest("/balance", `{"account_id":"a"}`)
		r.Header.Set("Content-Encoding", "identity")
		r.Header.Set("Idempotency-Key", key(1))
		r.Header.Add(header, r.Header.Get(header))
		h.ServeHTTP(httptest.NewRecorder(), r)
		expectFailures(t, spy, failuretype.RequestInvalid)
	}
	spy := &failureSpy{}
	h := observedHandler(t, panicLedger{}, spy)
	for _, path := range []string{"/healthz", "/balance", "/api/collections", "/unknown"} {
		request(t, h, "GET", path, nil, "")
	}
	request(t, h, "POST", "/api/collections", nil, "")
	if len(spy.kinds) != 0 || len(spy.classes) != 0 {
		t.Fatal("non-custom population expanded")
	}
}

func failureRequest(path, body string) *http.Request {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+testKey)
	r.Header.Set("Content-Type", "application/json")
	return r
}

// Error() must never be consulted, even when a reader/writer returns this.
type noFormatError struct{}

func (noFormatError) Error() string { panic("SYNTHETIC_PRIVATE_SENTINEL formatted") }

type hostileUnwrap struct{}

func (hostileUnwrap) Error() string { panic("SYNTHETIC_PRIVATE_SENTINEL formatted") }
func (hostileUnwrap) Unwrap() error { panic("SYNTHETIC_PRIVATE_SENTINEL unwrapped") }

func TestHTTPFailureOwnerCanonicalHostileFieldsAndFakeAccounting(t *testing.T) {
	for _, fixture := range []struct {
		err    error
		kind   failuretype.Type
		code   ledger.Code
		status int
	}{
		{fmt.Errorf("SYNTHETIC_PRIVATE_SENTINEL: %w", &ledger.Diagnostic{Code: ledger.CodeAccount, Stage: testKey, Constraint: testKey, Outcome: testKey, NextStep: testKey}), failuretype.AccountMissing, ledger.CodeAccount, 404},
		{&ledger.Diagnostic{Code: ledger.CodeClosed, Stage: testKey}, failuretype.NotReady, ledger.CodeClosed, 503},
		{&ledger.Diagnostic{Code: ledger.CodeConflict, Stage: testKey}, failuretype.IdempotencyRefused, ledger.CodeConflict, 409},
		{&ledger.Diagnostic{Code: ledger.CodeOverflow, Stage: testKey}, failuretype.AmountRefused, ledger.CodeOverflow, 409},
		{&ledger.Diagnostic{Code: ledger.CodeInsufficient, Stage: testKey}, failuretype.AdmissionRefused, ledger.CodeInsufficient, 409},
		{noFormatError{}, failuretype.RequestDispatchUnknown, "LEDGER_HTTP_OUTCOME_UNKNOWN", 503},
		{hostileUnwrap{}, failuretype.RequestDispatchUnknown, "LEDGER_HTTP_OUTCOME_UNKNOWN", 503},
		{&ledger.Diagnostic{Code: ledger.Code(testKey), Stage: testKey}, failuretype.RequestDispatchUnknown, "LEDGER_HTTP_OUTCOME_UNKNOWN", 503},
		{context.Canceled, failuretype.Cancelled, "LEDGER_HTTP_OUTCOME_UNKNOWN", 503},
	} {
		spy := &failureSpy{}
		h := observedHandler(t, stubLedger{err: fixture.err}, spy)
		out := postJSON(t, h, "/balance", `{"account_id":"a"}`)
		requireStatus(t, out, fixture.status)
		body := closedDiagnostic(t, out)
		if body["code"] != string(fixture.code) {
			t.Fatal("canonical code lost")
		}
		if fixture.kind == failuretype.RequestDispatchUnknown && (!strings.Contains(body["next_step"].(string), "cause unknown") || body["outcome"] != "unknown") {
			t.Fatal("fallback invented an outcome or omitted guidance")
		}
		expectFailures(t, spy, fixture.kind)
		// Classification and canonicalization must also work without a collector.
		without := postJSON(t, newTestHandler(t, stubLedger{err: fixture.err}, nil), "/balance", `{"account_id":"a"}`)
		if without.Body.String() != out.Body.String() {
			t.Fatal("disabled observer changed diagnostics")
		}
	}
	for _, route := range panicRoutes {
		spy := &failureSpy{}
		h := observedHandler(t, stubLedger{panicOn: route.method}, spy)
		headers := authHeaders("application/json")
		headers["Idempotency-Key"] = key(1)
		out := request(t, h, "POST", route.path, headers, route.body)
		requireStatus(t, out, 503)
		closedDiagnostic(t, out)
		expectFailures(t, spy, failuretype.RequestDispatchUnknown)
	}
}

type readFault struct {
	err       error
	panicRead bool
}

func (r readFault) Read([]byte) (int, error) {
	if r.panicRead {
		if r.err != nil {
			panic(r.err)
		}
		panic("SYNTHETIC_PRIVATE_SENTINEL reader")
	}
	return 0, r.err
}
func (readFault) Close() error { return nil }

func TestHTTPFailureOwnerReadEvidenceBeforeDiscard(t *testing.T) {
	for _, fixture := range []struct {
		reader readFault
		kind   failuretype.Type
		status int
	}{
		{readFault{err: io.ErrUnexpectedEOF}, failuretype.RequestInvalid, 400},
		{readFault{err: &http.MaxBytesError{Limit: MaxBodyBytes}}, failuretype.RequestInvalid, 400},
		{readFault{err: noFormatError{}}, failuretype.RequestReadUnknown, 503},
		{readFault{err: hostileUnwrap{}}, failuretype.RequestReadUnknown, 503},
		{readFault{panicRead: true}, failuretype.RequestReadUnknown, 503},
		{readFault{err: fmt.Errorf("SYNTHETIC_PRIVATE_SENTINEL: %w", context.Canceled)}, failuretype.Cancelled, 503},
		{readFault{err: context.DeadlineExceeded}, failuretype.DeadlineExceeded, 503},
		{readFault{err: context.Canceled, panicRead: true}, failuretype.Cancelled, 503},
		{readFault{err: noFormatError{}, panicRead: true}, failuretype.RequestReadUnknown, 503},
	} {
		spy := &failureSpy{}
		h := observedHandler(t, panicLedger{}, spy)
		r := failureRequest("/balance", "")
		r.Body = fixture.reader
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		requireStatus(t, out, fixture.status)
		body := closedDiagnostic(t, out)
		if fixture.status == 503 && (body["stage"] != "request_read" || body["outcome"] != "no_effect") {
			t.Fatal("read failure guessed accounting or syntax")
		}
		expectFailures(t, spy, fixture.kind)
	}
}

type deliveryFaultWriter struct {
	failedWriter
	err        error
	panicValue any
	cancel     func()
}

func (w *deliveryFaultWriter) Write(p []byte) (int, error) {
	w.wrote = true
	if w.cancel != nil {
		w.cancel()
	}
	if w.panicValue != nil {
		panic(w.panicValue)
	}
	if w.err != nil {
		return 0, w.err
	}
	return w.failedWriter.Write(p)
}
func (w *deliveryFaultWriter) FlushError() error {
	if w.cancel != nil {
		w.cancel()
	}
	return w.failedWriter.FlushError()
}

func TestHTTPFailureOwnerDeliveryAndCancellationDedup(t *testing.T) {
	for _, mode := range []string{"header_panic", "write_panic", "flush_panic", "flush_error", "error", "short"} {
		spy := &failureSpy{}
		w := &failedWriter{header: http.Header{}, mode: mode}
		spy.afterDelivery = func() {
			if mode != "header_panic" && !w.wrote {
				t.Fatal("observed before final response attempt")
			}
		}
		h := observedHandler(t, stubLedger{err: &ledger.Diagnostic{Code: ledger.CodeAccount}}, spy)
		h.ServeHTTP(w, failureRequest("/balance", `{"account_id":"a"}`))
		expectFailures(t, spy, failuretype.AccountMissing, failuretype.ResponseDeliveryUnknown)
		if len(spy.classes) != 1 || spy.classes[0] != telemetry.Unknown {
			t.Fatal("failed delivery became a receipt")
		}
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, mode := range []string{"typed_error", "wrapped_typed_error", "consequent_short", "consequent_panic"} {
			spy := &failureSpy{}
			ctx := context.Background()
			cancel := func() {}
			if cause == context.Canceled {
				ctx, cancel = context.WithCancel(ctx)
			} else {
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
			}
			cancel()
			w := &deliveryFaultWriter{failedWriter: failedWriter{header: http.Header{}}}
			switch mode {
			case "typed_error":
				w.err = cause
			case "wrapped_typed_error":
				w.err = fmt.Errorf("SYNTHETIC_PRIVATE_SENTINEL: %w", cause)
			case "consequent_short":
				w.mode = "short"
			case "consequent_panic":
				w.panicValue = "SYNTHETIC_PRIVATE_SENTINEL"
			}
			h := observedHandler(t, stubLedger{err: cause}, spy)
			h.ServeHTTP(w, failureRequest("/balance", `{"account_id":"a"}`).WithContext(ctx))
			want := []failuretype.Type{ledger.FailureKind(cause, 0)}
			if mode == "consequent_short" || mode == "consequent_panic" {
				want = append(want, failuretype.ResponseDeliveryUnknown)
			}
			expectFailures(t, spy, want...)
			if spy.classes[0] != telemetry.Unknown {
				t.Fatal("interrupted delivery inferred terminal success")
			}
		}
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, noFormatError{}, hostileUnwrap{}} {
		spy := &failureSpy{}
		w := &deliveryFaultWriter{failedWriter: failedWriter{header: http.Header{}}, panicValue: cause}
		service := stubLedger{}
		want := ioFailureKind(cause, failuretype.ResponseDeliveryUnknown)
		kinds := []failuretype.Type{want}
		if want == failuretype.Cancelled || want == failuretype.DeadlineExceeded {
			service.err = cause // caller remains active; equal categories do not prove shared cause
			kinds = append(kinds, want)
		}
		observedHandler(t, service, spy).ServeHTTP(w, failureRequest("/balance", `{"account_id":"a"}`))
		expectFailures(t, spy, kinds...)
	}
	for _, err := range []error{http.ErrAbortHandler, http.ErrServerClosed, http.ErrNotSupported} {
		for _, panicValue := range []bool{false, true} {
			spy := &failureSpy{}
			w := &deliveryFaultWriter{failedWriter: failedWriter{header: http.Header{}}}
			if panicValue {
				w.panicValue = err
			} else {
				w.err = err
			}
			observedHandler(t, stubLedger{}, spy).ServeHTTP(w, failureRequest("/balance", `{"account_id":"a"}`))
			expectFailures(t, spy)
			if spy.classes[0] != telemetry.Unknown {
				t.Fatal("expected interruption inferred delivery")
			}
		}
	}
	// Unsupported Flush is a successful local write, not a new exception.
	spy := &failureSpy{}
	w := &noFlushWriter{header: http.Header{}}
	observedHandler(t, stubLedger{}, spy).ServeHTTP(w, failureRequest("/balance", `{"account_id":"a"}`))
	expectFailures(t, spy)
	if spy.classes[0] != telemetry.Status2xx {
		t.Fatal("unsupported optional flush changed terminal evidence")
	}
}

type noFlushWriter struct{ header http.Header }

func (w *noFlushWriter) Header() http.Header       { return w.header }
func (*noFlushWriter) WriteHeader(int)             {}
func (*noFlushWriter) Write(p []byte) (int, error) { return len(p), nil }

// Source-seam tests only: scalar production DTOs do not have failing marshalers.
type syntheticMarshaler struct{ panicMarshal bool }

func (m syntheticMarshaler) MarshalJSON() ([]byte, error) {
	if m.panicMarshal {
		panic("SYNTHETIC_PRIVATE_SENTINEL encoder")
	}
	return nil, noFormatError{}
}

func TestHTTPFailureOwnerEncodingSourceSeamPreservesPrimaryAndProof(t *testing.T) {
	for _, panicMarshal := range []bool{false, true} {
		spy := &failureSpy{}
		h := observedHandler(t, stubLedger{}, spy)
		h.routes["/balance"] = func(w http.ResponseWriter, r *http.Request) {
			writeDiagnostic(w, 400, diagInput)
			writeJSON(w, 200, syntheticMarshaler{panicMarshal: panicMarshal})
		}
		out := postJSON(t, h, "/balance", `{"account_id":"a"}`)
		requireStatus(t, out, 503)
		body := closedDiagnostic(t, out)
		if body["stage"] != "response_encoding" || body["outcome"] != "unknown" || strings.Contains(body["next_step"].(string), "rolled back") {
			t.Fatal("encoding fault invented rollback")
		}
		expectFailures(t, spy, failuretype.RequestInvalid, failuretype.ResponseEncodingUnknown)
		spy = &failureSpy{}
		h = observedHandler(t, stubLedger{}, spy)
		h.routes["/balance"] = func(w http.ResponseWriter, r *http.Request) {
			accountingInvoked(w)
			accountingReturned(w, r, nil)
			writeJSON(w, 200, accountResponse{AccountBalance: []accountJSON{{AccountID: "a", Balance: "10", Enabled: true}}})
			writeJSON(w, 200, syntheticMarshaler{panicMarshal: panicMarshal})
		}
		out = postJSON(t, h, "/balance", `{"account_id":"a"}`)
		requireStatus(t, out, 200)
		if decodeMap(t, out)["account_balance"].([]any)[0].(map[string]any)["balance"] != "10" {
			t.Fatal("known encoded proof erased")
		}
		expectFailures(t, spy, failuretype.ResponseEncodingUnknown)
	}
}

func TestHTTPFailureBatchFixedSlotsAndIndependentIntegrity(t *testing.T) {
	b := &responseBuffer{header: http.Header{}}
	for range 3 {
		b.failures.add(failuretype.TransactionAfterCompletionUnknown)
	}
	writeJSON(b, 200, syntheticMarshaler{})
	b.deliveryFailure(failuretype.ResponseDeliveryUnknown)
	spy := &failureSpy{}
	observeFailures(spy, b.failures)
	expectFailures(t, spy, failuretype.TransactionAfterCompletionUnknown, failuretype.TransactionAfterCompletionUnknown, failuretype.TransactionAfterCompletionUnknown, failuretype.ResponseEncodingUnknown, failuretype.ResponseDeliveryUnknown)
	for _, kind := range []failuretype.Type{0, 40, 255, failuretype.Cancelled} {
		batch := b.failures
		batch.add(kind)
		spy := &failureSpy{}
		observeFailures(spy, batch)
		if len(spy.kinds) != 0 || spy.invalidFailures != 1 || spy.invalidRequests != 0 {
			t.Fatal("invalid or overflowed failures became partial/healthy evidence")
		}
	}
	// Data-only batch metadata contains no string, pointer, request or registry.
	typ := reflect.TypeOf(failureBatch{})
	for i := range typ.NumField() {
		kind := typ.Field(i).Type.Kind()
		if kind != reflect.Array && kind != reflect.Int && kind != reflect.Bool {
			t.Fatal("batch acquired private data storage")
		}
	}
}

func TestHTTPFailureObserverPanicCannotAffectAccounting(t *testing.T) {
	service := newTestLedger(t)
	spy := &failureSpy{panicObserve: true, panicInvalidate: true}
	h := observedHandler(t, service, spy)
	provision(t, h, "a")
	credit(t, h, "a", "10", 1)
	out := postJSON(t, h, "/balance", `{"account_id":"missing"}`)
	requireStatus(t, out, 404)
	if spy.invalidFailures != 1 || len(spy.kinds) != 1 || spy.invalidRequests != 0 {
		t.Fatal("failure observer panic recursed or invalidated terminal family")
	}
	out = postJSON(t, h, "/balance", `{"account_id":"a"}`)
	if decodeMap(t, out)["available"] != "10" {
		t.Fatal("observer panic changed accounting")
	}
}
