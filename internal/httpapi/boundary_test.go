package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"possums-intake/internal/ledger"
)

// stubLedger returns canned values or one closed diagnostic. It never touches
// storage, so error/privacy boundaries are exercised without real state.
type stubLedger struct {
	err         error
	panicOn     string
	account     ledger.Account
	credit      ledger.Credit
	reservation ledger.Reservation
	snapshot    ledger.BalanceSnapshot
}

func (s stubLedger) maybePanic(name string) {
	if s.panicOn == name {
		panic("SYNTHETIC_PRIVATE_SENTINEL panic payload")
	}
}

func (s stubLedger) result(err error) error {
	if s.err != nil {
		return s.err
	}
	return err
}

func (s stubLedger) Provision(context.Context, string) (ledger.Account, error) {
	s.maybePanic("provision")
	return s.account, s.result(nil)
}
func (s stubLedger) Credit(context.Context, ledger.CreditInput) (ledger.Credit, error) {
	s.maybePanic("credit")
	return s.credit, s.result(nil)
}
func (s stubLedger) Reserve(context.Context, ledger.ReserveInput) (ledger.Reservation, error) {
	s.maybePanic("reserve")
	return s.reservation, s.result(nil)
}
func (s stubLedger) Renew(context.Context, ledger.RenewInput) (ledger.Renewal, error) {
	s.maybePanic("renew")
	return ledger.Renewal{Reservation: s.reservation}, s.result(nil)
}
func (s stubLedger) Settle(context.Context, ledger.SettleInput) (ledger.Reservation, error) {
	s.maybePanic("settle")
	return s.reservation, s.result(nil)
}
func (s stubLedger) Refund(context.Context, string) (ledger.Reservation, error) {
	s.maybePanic("refund")
	return s.reservation, s.result(nil)
}
func (s stubLedger) Balance(context.Context, string) (ledger.BalanceSnapshot, error) {
	s.maybePanic("balance")
	return s.snapshot, s.result(nil)
}
func (s stubLedger) Lookup(context.Context, string) (ledger.Reservation, error) {
	s.maybePanic("lookup")
	return s.reservation, s.result(nil)
}
func (s stubLedger) Cleanup(context.Context, bool) (ledger.CleanupResult, error) {
	return ledger.CleanupResult{}, nil
}

type panicLedger struct{}

func (panicLedger) Provision(context.Context, string) (ledger.Account, error) {
	panic("SYNTHETIC_PRIVATE_SENTINEL")
}
func (panicLedger) Credit(context.Context, ledger.CreditInput) (ledger.Credit, error) {
	panic("SYNTHETIC_PRIVATE_SENTINEL")
}
func (panicLedger) Reserve(context.Context, ledger.ReserveInput) (ledger.Reservation, error) {
	panic("SYNTHETIC_PRIVATE_SENTINEL")
}
func (panicLedger) Renew(context.Context, ledger.RenewInput) (ledger.Renewal, error) {
	panic("SYNTHETIC_PRIVATE_SENTINEL")
}
func (panicLedger) Settle(context.Context, ledger.SettleInput) (ledger.Reservation, error) {
	panic("SYNTHETIC_PRIVATE_SENTINEL")
}
func (panicLedger) Refund(context.Context, string) (ledger.Reservation, error) {
	panic("SYNTHETIC_PRIVATE_SENTINEL")
}
func (panicLedger) Balance(context.Context, string) (ledger.BalanceSnapshot, error) {
	panic("SYNTHETIC_PRIVATE_SENTINEL")
}
func (panicLedger) Lookup(context.Context, string) (ledger.Reservation, error) {
	panic("SYNTHETIC_PRIVATE_SENTINEL")
}
func (panicLedger) Cleanup(context.Context, bool) (ledger.CleanupResult, error) {
	panic("SYNTHETIC_PRIVATE_SENTINEL")
}

// routesAndBodies pairs every accounting route with a minimally valid body so
// authentication is the only failing precondition in the unauthorized tests.
var routesAndBodies = []struct {
	path string
	body string
}{
	{"/accounts/provision", `{"account_id":"a"}`},
	{"/credits", `{"account_id":"a","amount":"1"}`},
	{"/reservations", `{"account_id":"a","maximum":"1","timeout_seconds":600}`},
	{"/reservations/renew", `{"reservation_id":"` + strings.Repeat("a", 32) + `","expected_expires_at":"2030-01-01T00:00:00.000Z"}`},
	{"/reservations/settle", `{"reservation_id":"` + strings.Repeat("a", 32) + `","charged_amount":"0"}`},
	{"/reservations/refund", `{"reservation_id":"` + strings.Repeat("a", 32) + `"}`},
	{"/reservations/lookup", `{"reservation_id":"` + strings.Repeat("a", 32) + `"}`},
	{"/balance", `{"account_id":"a"}`},
}

func closedDiagnostic(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if strings.Contains(recorder.Body.String(), testKey) || strings.Contains(recorder.Body.String(), "SYNTHETIC_PRIVATE_SENTINEL") || strings.Contains(recorder.Body.String(), "panic:") {
		t.Fatalf("response leaked a credential, sentinel or panic text: %s", recorder.Body.String())
	}
	body := decodeMap(t, recorder)
	exactKeys(t, body, "code", "stage", "constraint", "outcome", "next_step")
	for name, value := range body {
		if text, ok := value.(string); !ok || text == "" {
			t.Fatalf("diagnostic field %q = %v", name, value)
		}
	}
	return body
}

func TestNewHandlerRejectsInvalidRuntimeKey(t *testing.T) {
	for _, key := range []string{"", "short", strings.Repeat("x", MinAdminKeyBytes-1)} {
		_, err := NewHandler(stubLedger{}, func() bool { return true }, key, nil)
		if err == nil {
			t.Fatalf("key of %d bytes accepted", len(key))
		}
		if strings.Contains(err.Error(), key) && key != "" {
			t.Fatalf("constructor error echoed the key: %v", err)
		}
	}
	if _, err := NewHandler(nil, func() bool { return true }, testKey, nil); err == nil {
		t.Fatal("nil accounting accepted")
	}
	if _, err := NewHandler(stubLedger{}, func() bool { return true }, strings.Repeat("x", MinAdminKeyBytes), nil); err != nil {
		t.Fatalf("minimum key rejected: %v", err)
	}
}

func TestEveryAccountingRouteRequiresTheSharedKey(t *testing.T) {
	handler := newTestHandler(t, stubLedger{}, func() bool { return true })
	attempts := []map[string]string{
		{"Content-Type": "application/json"},
		{"Authorization": "Bearer wrong-key-wrong-key-wrong-key-wrong", "Content-Type": "application/json"},
		{"Authorization": "Basic " + testKey, "Content-Type": "application/json"},
		{"Authorization": testKey, "Content-Type": "application/json"},
	}
	for _, route := range routesAndBodies {
		for _, headers := range attempts {
			recorder := request(t, handler, http.MethodPost, route.path, headers, route.body)
			requireStatus(t, recorder, http.StatusUnauthorized)
			body := closedDiagnostic(t, recorder)
			if body["code"] != "LEDGER_HTTP_UNAUTHORIZED" {
				t.Fatalf("%s unauthorized code = %v", route.path, body["code"])
			}
		}
		// A duplicated Authorization header is malformed, not a valid key.
		req := httptest.NewRequest(http.MethodPost, route.path, strings.NewReader(route.body))
		req.Header.Add("Authorization", "Bearer "+testKey)
		req.Header.Add("Authorization", "Bearer "+testKey)
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		requireStatus(t, recorder, http.StatusBadRequest)
		closedDiagnostic(t, recorder)
	}
}

func TestUnknownAndNativeRoutesAreClosed(t *testing.T) {
	handler := newTestHandler(t, panicLedger{}, func() bool { return true })
	for _, path := range []string{
		"/", "/api/collections", "/api/collections/ledger_accounts/records",
		"/api/backups", "/api/settings", "/_/", "/api/admins", "/index.html",
		"/healthz/extra", "/accounts", "/credits/extra",
	} {
		recorder := request(t, handler, http.MethodGet, path, nil, "")
		requireStatus(t, recorder, http.StatusNotFound)
		if body := closedDiagnostic(t, recorder); body["code"] != "LEDGER_HTTP_NOT_FOUND" {
			t.Fatalf("%s code = %v", path, body["code"])
		}
	}
	// Wrong method on a real path is also a closed 404, never a framework error.
	recorder := request(t, handler, http.MethodGet, "/balance", nil, "")
	requireStatus(t, recorder, http.StatusNotFound)
	recorder = request(t, handler, http.MethodPut, "/accounts/provision", authHeaders("application/json"), `{"account_id":"a"}`)
	requireStatus(t, recorder, http.StatusNotFound)
	// healthz never requires the key and never runs accounting.
	recorder = request(t, handler, http.MethodGet, "/healthz", nil, "")
	requireStatus(t, recorder, http.StatusOK)
}

func TestRequiredBodyContract(t *testing.T) {
	handler := newTestHandler(t, stubLedger{}, func() bool { return true })
	valid := authHeaders("application/json")

	cases := []struct {
		name    string
		headers map[string]string
		body    string
	}{
		{"empty", valid, ""},
		{"array", valid, `[]`},
		{"scalar", valid, `"a"`},
		{"trailing", valid, `{"account_id":"a"} {}`},
		{"trailing token", valid, `{"account_id":"a"} trailing`},
		{"duplicate key", valid, `{"account_id":"a","account_id":"b"}`},
		{"null required", valid, `{"account_id":null}`},
		{"missing required", valid, `{}`},
		{"malformed utf8", valid, "{\"account_id\":\"a\xffb\"}"},
		{"invalid escape", valid, `{"account_id":"a\q"}`},
		{"number account", valid, `{"account_id":123}`},
	}
	for _, test := range cases {
		recorder := request(t, handler, http.MethodPost, "/accounts/provision", test.headers, test.body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d body = %s", test.name, recorder.Code, recorder.Body.String())
		}
		if body := closedDiagnostic(t, recorder); body["code"] != "LEDGER_HTTP_INPUT_REJECTED" {
			t.Fatalf("%s code = %v", test.name, body["code"])
		}
	}

	// Oversized bodies are rejected, not truncated.
	big := `{"account_id":"` + strings.Repeat("a", MaxBodyBytes) + `"}`
	recorder := request(t, handler, http.MethodPost, "/accounts/provision", valid, big)
	requireStatus(t, recorder, http.StatusBadRequest)

	// Compression is never accepted.
	compressed := authHeaders("application/json")
	compressed["Content-Encoding"] = "gzip"
	recorder = request(t, handler, http.MethodPost, "/accounts/provision", compressed, `{"account_id":"a"}`)
	requireStatus(t, recorder, http.StatusBadRequest)

	// Query parameters are rejected on every route.
	recorder = request(t, handler, http.MethodPost, "/balance?account_id=a", valid, `{"account_id":"a"}`)
	requireStatus(t, recorder, http.StatusBadRequest)

	// Content type must identify JSON; it need not use one textual spelling.
	for _, contentType := range []string{"", "text/plain", "application/json; broken"} {
		headers := authHeaders(contentType)
		if contentType == "" {
			delete(headers, "Content-Type")
		}
		recorder = request(t, handler, http.MethodPost, "/accounts/provision", headers, `{"account_id":"a"}`)
		requireStatus(t, recorder, http.StatusBadRequest)
	}
}

func TestMoneyAndTimeoutWireValidation(t *testing.T) {
	handler := newTestHandler(t, newTestLedger(t), func() bool { return true })
	valid := authHeaders("application/json")
	valid["Idempotency-Key"] = key(9)

	badCredits := []string{
		`{"account_id":"a","amount":"0"}`,
		`{"account_id":"a","amount":"00"}`,
		`{"account_id":"a","amount":"1.0"}`,
		`{"account_id":"a","amount":"1e2"}`,
		`{"account_id":"a","amount":"18446744073709551616"}`,
		`{"account_id":"a","amount":"-1"}`,
		`{"account_id":"a","amount":5}`,
	}
	for _, body := range badCredits {
		recorder := request(t, handler, http.MethodPost, "/credits", valid, body)
		requireStatus(t, recorder, http.StatusBadRequest)
	}

	badReserves := []struct {
		body   string
		reason string
	}{
		{`{"account_id":"a","maximum":"0","timeout_seconds":600}`, "zero maximum"},
		{`{"account_id":"a","maximum":"1","timeout_seconds":0}`, "timeout zero"},
		{`{"account_id":"a","maximum":"1","timeout_seconds":1801}`, "timeout too large"},
		{`{"account_id":"a","maximum":"1","timeout_seconds":600.5}`, "fractional timeout"},
		{`{"account_id":"a","maximum":"1","timeout_seconds":"600"}`, "string timeout"},
		{`{"account_id":"a","maximum":"1","timeout_seconds":6e2}`, "exponent timeout"},
		{`{"account_id":"a","maximum":"1","timeout_seconds":null}`, "null timeout"},
		{`{"account_id":"a","maximum":"1"}`, "missing timeout"},
	}
	for _, test := range badReserves {
		recorder := request(t, handler, http.MethodPost, "/reservations", valid, test.body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d body = %s", test.reason, recorder.Code, recorder.Body.String())
		}
	}

	// Idempotency-Key is required and singleton on credits and reservations.
	noKey := authHeaders("application/json")
	recorder := request(t, handler, http.MethodPost, "/credits", noKey, `{"account_id":"a","amount":"1"}`)
	requireStatus(t, recorder, http.StatusBadRequest)
	recorder = request(t, handler, http.MethodPost, "/reservations", noKey, `{"account_id":"a","maximum":"1","timeout_seconds":600}`)
	requireStatus(t, recorder, http.StatusBadRequest)
	// Settle/refund/lookup do not require the header. Missing evidence is a
	// ledger decision, not a missing-header rejection.
	recorder = request(t, handler, http.MethodPost, "/reservations/lookup", noKey, `{"reservation_id":"`+strings.Repeat("a", 32)+`"}`)
	requireStatus(t, recorder, http.StatusGone)
}

func TestLedgerDiagnosticsMapToPinnedStatuses(t *testing.T) {
	cases := []struct {
		code   ledger.Code
		status int
	}{
		{ledger.CodeInput, http.StatusBadRequest},
		{ledger.CodeKey, http.StatusBadRequest},
		{ledger.CodeAccount, http.StatusNotFound},
		{ledger.CodeInsufficient, http.StatusConflict},
		{ledger.CodeDisabled, http.StatusConflict},
		{ledger.CodeConflict, http.StatusConflict},
		{ledger.CodeCharge, http.StatusConflict},
		{ledger.CodeExpired, http.StatusConflict},
		{ledger.CodeOverflow, http.StatusConflict},
		{ledger.CodeKeyWindow, http.StatusConflict},
		{ledger.CodeEvidence, http.StatusGone},
		{ledger.CodeWriter, http.StatusServiceUnavailable},
		{ledger.CodeWork, http.StatusServiceUnavailable},
		{ledger.CodeBegin, http.StatusServiceUnavailable},
		{ledger.CodeStorage, http.StatusServiceUnavailable},
		{ledger.CodeCommit, http.StatusServiceUnavailable},
		{ledger.CodeCorrupt, http.StatusServiceUnavailable},
		{ledger.CodeClosed, http.StatusServiceUnavailable},
	}
	for _, test := range cases {
		diagnostic := &ledger.Diagnostic{Code: test.code, Stage: "s", Constraint: "c", Outcome: "o", NextStep: "n"}
		handler := newTestHandler(t, stubLedger{err: diagnostic}, func() bool { return true })
		recorder := postJSON(t, handler, "/balance", `{"account_id":"a"}`)
		if recorder.Code != test.status {
			t.Fatalf("%s: status = %d, want %d", test.code, recorder.Code, test.status)
		}
		body := closedDiagnostic(t, recorder)
		canonical := ledger.CanonicalFailure(diagnostic, 0)
		if body["code"] != string(test.code) || body["stage"] != canonical.Stage || body["constraint"] != canonical.Constraint || body["outcome"] != canonical.Outcome || body["next_step"] != canonical.NextStep {
			t.Fatalf("%s body = %v", test.code, body)
		}
	}
	// Unknown/unexpected errors and panics are a safe unknown-outcome 503.
	handler := newTestHandler(t, stubLedger{err: fmt.Errorf("SYNTHETIC_PRIVATE_SENTINEL raw driver failure")}, func() bool { return true })
	recorder := postJSON(t, handler, "/balance", `{"account_id":"a"}`)
	requireStatus(t, recorder, http.StatusServiceUnavailable)
	if body := closedDiagnostic(t, recorder); body["code"] != "LEDGER_HTTP_OUTCOME_UNKNOWN" || body["outcome"] != "unknown" {
		t.Fatalf("unexpected error body = %v", body)
	}
	for _, route := range panicRoutes {
		handler := newTestHandler(t, stubLedger{panicOn: route.method}, func() bool { return true })
		headers := authHeaders("application/json")
		if route.method == "credit" || route.method == "reserve" {
			headers["Idempotency-Key"] = key(11)
		}
		recorder := request(t, handler, http.MethodPost, route.path, headers, route.body)
		requireStatus(t, recorder, http.StatusServiceUnavailable)
		if body := closedDiagnostic(t, recorder); body["code"] != "LEDGER_HTTP_OUTCOME_UNKNOWN" {
			t.Fatalf("%s panic body = %v", route.method, body)
		}
	}
}

var panicRoutes = []struct {
	method string
	path   string
	body   string
}{
	{"provision", "/accounts/provision", `{"account_id":"a"}`},
	{"credit", "/credits", `{"account_id":"a","amount":"1"}`},
	{"reserve", "/reservations", `{"account_id":"a","maximum":"1","timeout_seconds":600}`},
	{"settle", "/reservations/settle", `{"reservation_id":"` + strings.Repeat("a", 32) + `","charged_amount":"0"}`},
	{"refund", "/reservations/refund", `{"reservation_id":"` + strings.Repeat("a", 32) + `"}`},
	{"balance", "/balance", `{"account_id":"a"}`},
	{"lookup", "/reservations/lookup", `{"reservation_id":"` + strings.Repeat("a", 32) + `"}`},
}
