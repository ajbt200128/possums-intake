package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"possums-intake/internal/ledger"
)

// testKey is a raw 32-byte runtime admin key. It must never appear in a response.
const testKey = "0123456789abcdef0123456789abcdef"

var testContext = context.Background()

func newTestLedger(t *testing.T) *ledger.Service {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	if err := ledger.Init(testContext, dir); err != nil {
		t.Fatalf("init: %s", ledger.Failure(err).Code)
	}
	service, err := ledger.Open(testContext, dir)
	if err != nil {
		t.Fatalf("open: %s", ledger.Failure(err).Code)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service
}

func newTestHandler(t *testing.T, accounting ledger.Accounting, ready func() bool) http.Handler {
	t.Helper()
	handler, err := NewHandler(accounting, ready, testKey, nil)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	return handler
}

func request(t *testing.T, handler http.Handler, method, path string, headers map[string]string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func authHeaders(contentType string) map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + testKey,
		"Content-Type":  contentType,
	}
}

func postJSON(t *testing.T, handler http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return request(t, handler, http.MethodPost, path, authHeaders("application/json"), body)
}

func key(nonce int) string {
	return fmt.Sprintf("v1.%013d.%032x", time.Now().UnixMilli(), nonce)
}

func decodeMap(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
		t.Fatalf("response was not one JSON object: %v", err)
	}
	return value
}

func requireStatus(t *testing.T, recorder *httptest.ResponseRecorder, status int) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, status, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q", got)
	}
}

func exactKeys(t *testing.T, value map[string]any, want ...string) {
	t.Helper()
	if len(value) != len(want) {
		t.Fatalf("field set = %v, want exactly %v", mapKeys(value), want)
	}
	for _, name := range want {
		if _, ok := value[name]; !ok {
			t.Fatalf("missing field %q in %v", name, mapKeys(value))
		}
	}
}

func mapKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	return keys
}

func provision(t *testing.T, handler http.Handler, accountID string) map[string]any {
	t.Helper()
	recorder := postJSON(t, handler, "/accounts/provision", fmt.Sprintf(`{"account_id":%q}`, accountID))
	requireStatus(t, recorder, http.StatusOK)
	return decodeMap(t, recorder)
}

func credit(t *testing.T, handler http.Handler, accountID, amount string, nonce int) map[string]any {
	t.Helper()
	headers := authHeaders("application/json")
	headers["Idempotency-Key"] = key(nonce)
	recorder := request(t, handler, http.MethodPost, "/credits", headers, fmt.Sprintf(`{"account_id":%q,"amount":%q}`, accountID, amount))
	requireStatus(t, recorder, http.StatusOK)
	return decodeMap(t, recorder)
}

func TestProvisionCreditBalanceAndAccountScoping(t *testing.T) {
	handler := newTestHandler(t, newTestLedger(t), func() bool { return true })

	account := provision(t, handler, "account-a")
	accounts := account["account_balance"].([]any)
	if len(accounts) != 1 {
		t.Fatalf("account_balance must be a list of exactly one account, got %v", account["account_balance"])
	}
	exactKeys(t, accounts[0].(map[string]any), "account_id", "balance", "enabled")
	if accounts[0].(map[string]any)["balance"] != "0" || accounts[0].(map[string]any)["enabled"] != true {
		t.Fatalf("provisioned account = %v", accounts[0])
	}

	credited := credit(t, handler, "account-a", "500", 1)
	creditRow := credited["credit"].(map[string]any)
	exactKeys(t, creditRow, "idempotency_key", "account_id", "amount", "balance_after", "committed_at", "retain_until")
	if creditRow["amount"] != "500" || creditRow["balance_after"] != "500" {
		t.Fatalf("credit row = %v", creditRow)
	}
	if _, ok := creditRow["committed_at"].(string); !ok {
		t.Fatalf("committed_at must be a fixed UTC string: %v", creditRow["committed_at"])
	}

	balance := postJSON(t, handler, "/balance", `{"account_id":"account-a"}`)
	requireStatus(t, balance, http.StatusOK)
	snapshot := decodeMap(t, balance)
	exactKeys(t, snapshot, "account_balance", "pending_reservations", "available")
	balanceAccounts := snapshot["account_balance"].([]any)
	if len(balanceAccounts) != 1 || balanceAccounts[0].(map[string]any)["balance"] != "500" {
		t.Fatalf("balance account = %v", snapshot["account_balance"])
	}
	if snapshot["available"] != "500" {
		t.Fatalf("available = %v", snapshot["available"])
	}
	if pending, ok := snapshot["pending_reservations"].([]any); !ok || len(pending) != 0 {
		t.Fatalf("pending_reservations must be an empty list, not null: %v", snapshot["pending_reservations"])
	}

	// Unknown account is scoped, never a global list or another account's data.
	unknown := postJSON(t, handler, "/balance", `{"account_id":"account-b"}`)
	requireStatus(t, unknown, http.StatusNotFound)
	if code := decodeMap(t, unknown)["code"]; code != string(ledger.CodeAccount) {
		t.Fatalf("unknown account code = %v", code)
	}
}

func TestReserveLookupSettleRefundAndPendingRows(t *testing.T) {
	handler := newTestHandler(t, newTestLedger(t), func() bool { return true })
	provision(t, handler, "account-a")
	credit(t, handler, "account-a", "1000", 1)

	headers := authHeaders("application/json")
	headers["Idempotency-Key"] = key(2)
	reserved := request(t, handler, http.MethodPost, "/reservations", headers, `{"account_id":"account-a","maximum":"400","timeout_seconds":600}`)
	requireStatus(t, reserved, http.StatusOK)
	reservation := decodeMap(t, reserved)["reservation"].(map[string]any)
	exactKeys(t, reservation, "reservation_id", "idempotency_key", "account_id", "maximum", "timeout_seconds", "expires_at", "state", "charged_amount", "terminal_at", "retain_until")
	if reservation["state"] != "active" || reservation["maximum"] != "400" || reservation["timeout_seconds"] != float64(600) {
		t.Fatalf("reservation row = %v", reservation)
	}
	if reservation["charged_amount"] != nil || reservation["terminal_at"] != nil || reservation["retain_until"] != nil {
		t.Fatalf("active terminal fields must be null: %v", reservation)
	}
	reservationID := reservation["reservation_id"].(string)

	// Balance lists the complete active row while available excludes the hold.
	balance := decodeMap(t, postJSON(t, handler, "/balance", `{"account_id":"account-a"}`))
	pending := balance["pending_reservations"].([]any)
	if len(pending) != 1 || pending[0].(map[string]any)["reservation_id"] != reservationID {
		t.Fatalf("pending rows = %v", pending)
	}
	if balance["available"] != "600" {
		t.Fatalf("available = %v", balance["available"])
	}

	lookup := decodeMap(t, postJSON(t, handler, "/reservations/lookup", fmt.Sprintf(`{"reservation_id":%q}`, reservationID)))
	if lookup["reservation"].(map[string]any)["state"] != "active" {
		t.Fatalf("lookup = %v", lookup)
	}

	settled := decodeMap(t, postJSON(t, handler, "/reservations/settle", fmt.Sprintf(`{"reservation_id":%q,"charged_amount":"250"}`, reservationID)))
	settledRow := settled["reservation"].(map[string]any)
	if settledRow["state"] != "settled" || settledRow["charged_amount"] != "250" {
		t.Fatalf("settled row = %v", settledRow)
	}
	if settledRow["terminal_at"] == nil || settledRow["retain_until"] == nil {
		t.Fatalf("terminal fields must be present: %v", settledRow)
	}

	// A conflicting refund returns the first committed outcome unchanged.
	refunded := decodeMap(t, postJSON(t, handler, "/reservations/refund", fmt.Sprintf(`{"reservation_id":%q}`, reservationID)))
	if refunded["reservation"].(map[string]any)["state"] != "settled" {
		t.Fatalf("conflicting refund changed outcome: %v", refunded)
	}
	balance = decodeMap(t, postJSON(t, handler, "/balance", `{"account_id":"account-a"}`))
	if balance["available"] != "750" {
		t.Fatalf("available after settle = %v", balance["available"])
	}
}

func TestExpiredSettlementPreservesAuthoritativeRow(t *testing.T) {
	handler := newTestHandler(t, newTestLedger(t), func() bool { return true })
	provision(t, handler, "account-a")
	credit(t, handler, "account-a", "1000", 1)

	headers := authHeaders("application/json")
	headers["Idempotency-Key"] = key(2)
	reserved := request(t, handler, http.MethodPost, "/reservations", headers, `{"account_id":"account-a","maximum":"400","timeout_seconds":1}`)
	requireStatus(t, reserved, http.StatusOK)
	reservationID := decodeMap(t, reserved)["reservation"].(map[string]any)["reservation_id"].(string)

	time.Sleep(1200 * time.Millisecond)

	late := postJSON(t, handler, "/reservations/settle", fmt.Sprintf(`{"reservation_id":%q,"charged_amount":"100"}`, reservationID))
	requireStatus(t, late, http.StatusConflict)
	body := decodeMap(t, late)
	if body["code"] != string(ledger.CodeExpired) || body["outcome"] != "expired" {
		t.Fatalf("late settlement diagnostic = %v", body)
	}
	row, ok := body["reservation"].(map[string]any)
	if !ok {
		t.Fatalf("expired settlement must preserve the authoritative reservation: %v", body)
	}
	if row["state"] != "expired" || row["charged_amount"] != "0" || row["terminal_at"] != row["expires_at"] {
		t.Fatalf("expired row = %v", row)
	}
	// The hold was released, not charged.
	balance := decodeMap(t, postJSON(t, handler, "/balance", `{"account_id":"account-a"}`))
	if balance["available"] != "1000" {
		t.Fatalf("available after expiry = %v", balance["available"])
	}
}

func TestResponsesNeverEchoTheRuntimeKey(t *testing.T) {
	handler := newTestHandler(t, newTestLedger(t), func() bool { return true })
	provision(t, handler, "account-a")
	credit(t, handler, "account-a", "10", 1)
	successes := []*httptest.ResponseRecorder{
		postJSON(t, handler, "/balance", `{"account_id":"account-a"}`),
		postJSON(t, handler, "/accounts/provision", `{"account_id":"account-a"}`),
		postJSON(t, handler, "/reservations/lookup", `{"reservation_id":"`+strings.Repeat("a", 32)+`"}`),
		request(t, handler, http.MethodGet, "/healthz", nil, ""),
	}
	for _, recorder := range successes {
		if strings.Contains(recorder.Body.String(), testKey) || strings.Contains(recorder.Header().Get("Authorization"), testKey) {
			t.Fatalf("response echoed the runtime key: %s", recorder.Body.String())
		}
	}
}

func TestHealthIsFixedAndDoesNoAccounting(t *testing.T) {
	ready := true
	handler := newTestHandler(t, panicLedger{}, func() bool { return ready })
	recorder := request(t, handler, http.MethodGet, "/healthz", nil, "")
	requireStatus(t, recorder, http.StatusOK)
	if body := decodeMap(t, recorder); body["status"] != "ok" || len(body) != 1 {
		t.Fatalf("health body = %v", body)
	}
	ready = false
	recorder = request(t, handler, http.MethodGet, "/healthz", nil, "")
	requireStatus(t, recorder, http.StatusServiceUnavailable)
	if body := decodeMap(t, recorder); body["status"] != "not_ready" || len(body) != 1 {
		t.Fatalf("not-ready body = %v", body)
	}
}
