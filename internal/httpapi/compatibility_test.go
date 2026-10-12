package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Exercise the actual HTTP boundary with additive fields, not just the binder.
// Extensions never become accounting inputs, persisted state or response fields.
func TestAdditiveAccountingCompatibilityOnActualHTTP(t *testing.T) {
	service := newTestLedger(t)
	server := httptest.NewServer(newTestHandler(t, service, nil))
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	call := func(path, body, idempotency string, variant int) map[string]any {
		t.Helper()
		extra := `,"future":{"nested":[true,null,{"amount":999999999999999999999999}],"ignored":"SYNTHETIC_EXTENSION_SENTINEL"},"Account_ID":"not-the-account"`
		if variant == 1 {
			extra = `,"another_future":null,"future":"\udfff"`
		}
		body = strings.TrimSuffix(body, "}") + extra + "}"
		req, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal("synthetic request unavailable")
		}
		req.Header.Set("Authorization", "Bearer "+testKey)
		req.Header.Set("Content-Type", "APPLICATION/JSON; charset=UTF-8")
		if idempotency != "" {
			req.Header.Set("Idempotency-Key", idempotency)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal("synthetic HTTP response unavailable")
		}
		raw, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusOK {
			t.Fatal("additive request did not succeed")
		}
		if strings.Contains(string(raw), "SYNTHETIC_EXTENSION_SENTINEL") || strings.Contains(string(raw), "another_future") || strings.Contains(string(raw), testKey) {
			t.Fatal("ignored extension or credential reached response")
		}
		var result map[string]any
		if json.Unmarshal(raw, &result) != nil {
			t.Fatal("synthetic response shape unavailable")
		}
		return result
	}
	account := `{"account_id":"opaque\u0000account\n"}`
	call("/accounts/provision", account, "", 0)
	creditBody := `{"account_id":"opaque\u0000account\n","amount":"100"}`
	creditKey := key(101)
	firstCredit := call("/credits", creditBody, creditKey, 0)
	if !reflect.DeepEqual(firstCredit, call("/credits", creditBody, creditKey, 1)) {
		t.Fatal("ignored extensions changed credit idempotency")
	}
	reserveBody := `{"account_id":"opaque\u0000account\n","maximum":"20","timeout_seconds":600}`
	reserveKey := key(102)
	reservation := call("/reservations", reserveBody, reserveKey, 0)
	if !reflect.DeepEqual(reservation, call("/reservations", reserveBody, reserveKey, 1)) {
		t.Fatal("ignored extensions changed reserve idempotency")
	}
	row := reservation["reservation"].(map[string]any)
	id, _ := json.Marshal(row["reservation_id"])
	identity := `{"reservation_id":` + string(id) + `}`
	call("/reservations/lookup", identity, "", 0)
	call("/reservations/settle", strings.TrimSuffix(identity, "}")+`,"charged_amount":"7"}`, "", 0)
	refunded := call("/reservations/refund", identity, "", 1)
	if refunded["reservation"].(map[string]any)["state"] != "settled" {
		t.Fatal("additive refund changed first terminal outcome")
	}
	balance := call("/balance", account, "", 1)
	if balance["available"] != "93" || len(balance["pending_reservations"].([]any)) != 0 {
		t.Fatal("ignored fields affected money or holds")
	}
	accountRow := balance["account_balance"].([]any)[0].(map[string]any)
	if accountRow["account_id"] != "opaque\x00account\n" {
		t.Fatal("gateway-compatible identity changed")
	}
}
