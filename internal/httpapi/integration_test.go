package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"possums-intake/internal/ledger"
	"possums-intake/internal/telemetry"
)

type terminalSpy struct {
	classes                       []telemetry.StatusClass
	invalid                       int
	panicObserve, panicInvalidate bool
	afterWrite                    func()
}

func (s *terminalSpy) ObserveTerminal(class telemetry.StatusClass, duration time.Duration) {
	if s.afterWrite != nil {
		s.afterWrite()
	}
	if duration < 0 {
		panic("invalid synthetic duration")
	}
	s.classes = append(s.classes, class)
	if s.panicObserve {
		panic("SYNTHETIC_PRIVATE_SENTINEL")
	}
}
func (s *terminalSpy) InvalidateRequests() {
	s.invalid++
	if s.panicInvalidate {
		panic("SYNTHETIC_PRIVATE_SENTINEL")
	}
}

func observedHandler(t *testing.T, service ledger.Accounting, spy TerminalObserver) *handler {
	t.Helper()
	h, err := NewHandler(service, func() bool { return true }, testKey, spy)
	if err != nil {
		t.Fatal("handler unavailable")
	}
	return h.(*handler)
}

func TestInvalidUnicodeAndMissingFieldsNeverDispatch(t *testing.T) {
	h := newTestHandler(t, panicLedger{}, nil)
	invalid := []string{
		`{"account_id":"\ud800"}`, `{"account_id":"\udfff"}`,
		`{"account_id":"\ud800x"}`, `{"account_id":"\ud800\u0061"}`,
		`{"account_id":"\ud800\ud800"}`, `{"account_id":"\udc00\ud800"}`,
		`{"account_id":"\ud800\\udc00"}`, `{"account_id":"\ud800\udc00\udc00"}`,
		`{"account_\ud800id":"aaaaaaaaaaaaaaa"}`,
	}
	for _, body := range invalid {
		r := postJSON(t, h, "/balance", body)
		requireStatus(t, r, 400)
		closedDiagnostic(t, r)
	}
	for _, route := range routesAndBodies {
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(route.body), &fields) != nil {
			t.Fatal("fixture invalid")
		}
		for name := range fields {
			var reduced map[string]json.RawMessage
			_ = json.Unmarshal([]byte(route.body), &reduced)
			delete(reduced, name)
			body, _ := json.Marshal(reduced)
			headers := authHeaders("application/json")
			headers["Idempotency-Key"] = key(1)
			r := request(t, h, http.MethodPost, route.path, headers, string(body))
			requireStatus(t, r, 400) // panicLedger would produce 503 if invoked
			closedDiagnostic(t, r)
		}
	}
}

func TestPairedUnicodeCannotCreateOrSelectAnSDKAccount(t *testing.T) {
	h := newTestHandler(t, newTestLedger(t), nil)
	for _, fixture := range []struct{ body, id string }{
		{`{"account_id":"\ud83d\udc3e"}`, "🐾"},
		{`{"account_id":"\uDBFF\uDFFF"}`, "\U0010ffff"},
		{`{"account_id":"\\ud800"}`, `\ud800`},
		{`{"account_id":"\ufffd"}`, "�"},
		{`{"account_id":"e\u0301"}`, "e\u0301"},
		{`{"account_id":"é"}`, "é"},
		{`{"account_id":"a\u0000b"}`, "a\x00b"},
		{`{"account_id":"control\nidentity"}`, "control\nidentity"},
		{`{"account_id":"\\t"}`, `\t`},
	} {
		r := postJSON(t, h, "/balance", fixture.body)
		requireStatus(t, r, 400)
		if closedDiagnostic(t, r)["code"] != string(ledger.CodeInput) {
			t.Fatal("non-SDK identity was normalized or admitted")
		}
	}
}

func TestSingletonHeadersOnActualHTTP(t *testing.T) {
	server := httptest.NewServer(newTestHandler(t, panicLedger{}, nil))
	defer server.Close()
	for _, route := range routesAndBodies {
		for _, name := range []string{"Authorization", "Content-Type", "Content-Encoding", "Idempotency-Key"} {
			r, _ := http.NewRequest(http.MethodPost, server.URL+route.path, strings.NewReader(route.body))
			r.Header.Set("Authorization", "Bearer "+testKey)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Content-Encoding", "identity")
			r.Header.Set("Idempotency-Key", key(1))
			r.Header.Add(name, r.Header.Get(name))
			response, err := server.Client().Do(r)
			if err != nil {
				t.Fatal("synthetic delivery failed")
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != 400 || strings.Contains(string(body), testKey) {
				t.Fatal("duplicate header boundary drifted")
			}
		}
	}
	// A comma-joined credential is one wrong credential, not two valid headers.
	r := postJSON(t, newTestHandler(t, panicLedger{}, nil), "/balance", `{}`)
	requireStatus(t, r, 400)
	headers := authHeaders("application/json")
	headers["Authorization"] += ", Bearer " + testKey
	r = request(t, newTestHandler(t, panicLedger{}, nil), "POST", "/balance", headers, `{}`)
	requireStatus(t, r, 401)
}

func TestFinalClassesAndRouteExclusions(t *testing.T) {
	for _, fixture := range []struct {
		status     int
		service    stubLedger
		body       string
		authorized bool
	}{
		{200, stubLedger{}, `{"account_id":"aaaaaaaaaaaaaaa"}`, true},
		{400, stubLedger{}, `{}`, true},
		{401, stubLedger{}, `{"account_id":"aaaaaaaaaaaaaaa"}`, false},
		{409, stubLedger{err: &ledger.Diagnostic{Code: ledger.CodeInsufficient}}, `{"account_id":"aaaaaaaaaaaaaaa"}`, true},
		{410, stubLedger{err: &ledger.Diagnostic{Code: ledger.CodeEvidence}}, `{"account_id":"aaaaaaaaaaaaaaa"}`, true},
		{503, stubLedger{err: &ledger.Diagnostic{Code: ledger.CodeCommit, Outcome: "unknown"}}, `{"account_id":"aaaaaaaaaaaaaaa"}`, true},
	} {
		spy := &terminalSpy{}
		h := observedHandler(t, fixture.service, spy)
		recorder := httptest.NewRecorder()
		spy.afterWrite = func() {
			if recorder.Body.Len() == 0 || recorder.Code != fixture.status || !recorder.Flushed {
				t.Fatal("observed before final flush")
			}
		}
		headers := authHeaders("application/json")
		if !fixture.authorized {
			delete(headers, "Authorization")
		}
		req := httptest.NewRequest("POST", "/balance", strings.NewReader(fixture.body))
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		h.ServeHTTP(recorder, req)
		if recorder.Code != fixture.status || len(spy.classes) != 1 || spy.classes[0] != statusClass(fixture.status) {
			t.Fatal("final classified observation missing")
		}
	}
	spy := &terminalSpy{}
	h := observedHandler(t, stubLedger{}, spy)
	expected := 0
	for _, route := range routesAndBodies {
		request(t, h, "POST", route.path, nil, route.body)
		// Renewal remains outside the approved telemetry population, including
		// its authentication failures; other custom routes count once.
		if route.path != "/reservations/renew" {
			expected++
		}
		if len(spy.classes) != expected {
			t.Fatal("custom route observation differs from its approved population")
		}
	}
	for _, path := range []string{"/healthz", "/balance", "/api/collections", "/_", "/unknown"} {
		request(t, h, "GET", path, nil, "")
	}
	request(t, h, "POST", "/api/collections", nil, "")
	if len(spy.classes) != expected {
		t.Fatal("non-accounting route observed")
	}
}

type failedWriter struct {
	header http.Header
	mode   string
	wrote  bool
}

func (w *failedWriter) Header() http.Header { return w.header }
func (w *failedWriter) WriteHeader(int) {
	if w.mode == "header_panic" {
		panic("SYNTHETIC_PRIVATE_SENTINEL")
	}
}
func (w *failedWriter) Write(p []byte) (int, error) {
	w.wrote = true
	switch w.mode {
	case "write_panic":
		panic("SYNTHETIC_PRIVATE_SENTINEL")
	case "error":
		return 0, errors.New("SYNTHETIC_PRIVATE_SENTINEL")
	case "short":
		return len(p) - 1, nil
	default:
		return len(p), nil
	}
}

func (w *failedWriter) FlushError() error {
	if w.mode == "flush_panic" {
		panic("SYNTHETIC_PRIVATE_SENTINEL")
	}
	if w.mode == "flush_error" {
		return errors.New("SYNTHETIC_PRIVATE_SENTINEL")
	}
	return nil
}

func TestUnknownDeliveryAndPanicObservations(t *testing.T) {
	for _, mode := range []string{"header_panic", "write_panic", "flush_panic", "flush_error", "error", "short", "cancelled", "route_panic", "unclassified"} {
		spy := &terminalSpy{}
		service := stubLedger{}
		if mode == "route_panic" {
			service.panicOn = "balance"
		}
		h := observedHandler(t, service, spy)
		if mode == "unclassified" {
			h.routes["/balance"] = func(http.ResponseWriter, *http.Request) {}
		}
		req := httptest.NewRequest("POST", "/balance", strings.NewReader(`{"account_id":"aaaaaaaaaaaaaaa"}`))
		req.Header.Set("Authorization", "Bearer "+testKey)
		req.Header.Set("Content-Type", "application/json")
		if mode == "cancelled" {
			ctx, cancel := context.WithCancel(req.Context())
			cancel()
			req = req.WithContext(ctx)
		}
		w := &failedWriter{header: http.Header{}, mode: mode}
		h.ServeHTTP(w, req)
		if len(spy.classes) != 1 || spy.classes[0] != telemetry.Unknown {
			t.Fatalf("%s assumed terminal success", mode)
		}
		if mode == "unclassified" && spy.invalid != 1 {
			t.Fatal("lost classification not invalidated")
		}
	}
}

func TestLocalOnceGuardAndObserverFailureIsolation(t *testing.T) {
	spy := &terminalSpy{}
	complete := terminalCompletion(spy, time.Now())
	complete(telemetry.Status2xx)
	complete(telemetry.Status2xx)
	if len(spy.classes) != 1 || spy.invalid != 1 {
		t.Fatal("duplicate evidence not invalidated")
	}
	spy = &terminalSpy{}
	terminalCompletion(spy, time.Now().Add(time.Hour))(telemetry.Status2xx)
	if len(spy.classes) != 0 || spy.invalid != 1 {
		t.Fatal("fabricated unavailable latency")
	}
	service := newTestLedger(t)
	spy = &terminalSpy{panicObserve: true, panicInvalidate: true}
	h := observedHandler(t, service, spy)
	createAccountFixture(t, h, "aaaaaaaaaaaaaaa")
	credit(t, h, "aaaaaaaaaaaaaaa", "10", 1)
	balance := postJSON(t, h, "/balance", `{"account_id":"aaaaaaaaaaaaaaa"}`)
	requireStatus(t, balance, 200)
	if decodeMap(t, balance)["available"] != "10" || spy.invalid != 3 {
		t.Fatal("observer changed money or result")
	}
	// Disabled runtime and hooks after shutdown are also inert for accounting.
	t.Setenv("OTEL_SDK_DISABLED", "true")
	runtime := telemetry.New(context.Background())
	h = observedHandler(t, service, runtime)
	credit(t, h, "aaaaaaaaaaaaaaa", "5", 2)
	runtime.Shutdown()
	credit(t, h, "aaaaaaaaaaaaaaa", "5", 3)
	balance = postJSON(t, h, "/balance", `{"account_id":"aaaaaaaaaaaaaaa"}`)
	if decodeMap(t, balance)["available"] != "20" {
		t.Fatal("disabled/shutdown telemetry changed accounting")
	}
}

// Models missing completion evidence AFTER a real credit commits, not an
// invented database rollback. The production core separately tests this seam.
type unknownCredit struct{ ledger.Accounting }

func (s unknownCredit) Credit(ctx context.Context, in ledger.CreditInput) (ledger.Credit, error) {
	if _, err := s.Accounting.Credit(ctx, in); err != nil {
		return ledger.Credit{}, err
	}
	return ledger.Credit{}, &ledger.Diagnostic{Code: ledger.CodeCommit, Stage: "transaction_completion", Constraint: "authoritative_commit_evidence", Outcome: "unknown", NextStep: "Resolve using the original identity; a new identity may duplicate an effect."}
}

func TestDeliveryLossAndUnknownCommitNeverInventReceipt(t *testing.T) {
	for _, mode := range []string{"error", "short", "write_panic", "flush_panic", "flush_error", "unknown_commit"} {
		service := newTestLedger(t)
		spy := &terminalSpy{}
		h := observedHandler(t, service, spy)
		createAccountFixture(t, h, "aaaaaaaaaaaaaaa")
		identity := key(1)
		body := `{"account_id":"aaaaaaaaaaaaaaa","amount":"10"}`
		headers := authHeaders("application/json")
		headers["Idempotency-Key"] = identity
		if mode == "unknown_commit" {
			h.accounting = unknownCredit{service}
			out := request(t, h, "POST", "/credits", headers, body)
			requireStatus(t, out, 503)
			d := closedDiagnostic(t, out)
			if d["outcome"] != "unknown" {
				t.Fatal("unknown commit invented a receipt")
			}
			h.accounting = service
		} else {
			req := httptest.NewRequest("POST", "/credits", strings.NewReader(body))
			for name, value := range headers {
				req.Header.Set(name, value)
			}
			h.ServeHTTP(&failedWriter{header: http.Header{}, mode: mode}, req)
			if spy.classes[len(spy.classes)-1] != telemetry.Unknown {
				t.Fatal("delivery loss inferred success")
			}
		}
		original := request(t, h, "POST", "/credits", headers, body)
		requireStatus(t, original, 200)
		duplicate := request(t, h, "POST", "/credits", headers, body)
		if original.Body.String() != duplicate.Body.String() {
			t.Fatal("retry changed original result")
		}
		altered := request(t, h, "POST", "/credits", headers, `{"account_id":"aaaaaaaaaaaaaaa","amount":"11"}`)
		requireStatus(t, altered, 409)
		balance := postJSON(t, h, "/balance", `{"account_id":"aaaaaaaaaaaaaaa"}`)
		if decodeMap(t, balance)["available"] != "10" {
			t.Fatal("retry/conflict duplicated money")
		}
	}
}
