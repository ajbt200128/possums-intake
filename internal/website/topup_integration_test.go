package website

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v87/webhook"
	"possums-intake/internal/payments"
	"possums-intake/internal/stripepay"
)

// Joins the actual PocketBase auth, HTTPS/CSRF page, SDK transport, signed
// webhook and accounting transaction. The only processor is an in-process fake.
func TestTopupHTTPSCanonicalWebhookFundsAuthenticatedAccount(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	if f.service.InitializeSyntheticPayments(ctx) != nil {
		t.Fatal("TOPUP_INTEGRATION_SCHEMA")
	}
	type object = map[string]any
	var mu sync.Mutex
	var session, intent, charge object
	var posts, gets int
	var ref string
	var paid bool
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Stripe-Version", stripepay.APIVersion)
		if r.Method == "POST" && r.URL.Path == "/v1/checkout/sessions" {
			posts++
			if r.ParseForm() != nil {
				w.WriteHeader(400)
				return
			}
			amount, e1 := strconv.ParseInt(r.Form.Get("line_items[0][price_data][unit_amount]"), 10, 64)
			expires, e2 := strconv.ParseInt(r.Form.Get("expires_at"), 10, 64)
			if e1 != nil || e2 != nil {
				w.WriteHeader(400)
				return
			}
			ref = r.Form.Get("client_reference_id")
			created := time.Now().Unix()
			session = object{"object": "checkout.session", "id": "cs_test_http_join", "livemode": false, "mode": "payment", "ui_mode": "hosted_page", "status": "open", "payment_status": "unpaid", "client_reference_id": ref, "created": created, "expires_at": expires, "currency": "usd", "amount_total": amount, "amount_subtotal": amount, "customer_creation": "if_required", "payment_method_types": []string{"card"}, "allowed_payment_method_types": []string{"card"}, "url": provider.URL + "/checkout/synthetic", "line_items": object{"object": "list", "has_more": false, "data": []any{object{"object": "item", "quantity": 1, "currency": "usd", "amount_total": amount, "amount_subtotal": amount, "price": object{"object": "price", "type": "one_time", "billing_scheme": "per_unit", "currency": "usd", "unit_amount": amount}}}}}
			intent = object{"object": "payment_intent", "id": "pi_http_join", "livemode": false, "status": "succeeded", "currency": "usd", "amount": amount, "amount_received": amount, "capture_method": "automatic", "latest_charge": "ch_http_join", "payment_method_types": []string{"card"}}
			charge = object{"object": "charge", "id": "ch_http_join", "livemode": false, "status": "succeeded", "currency": "usd", "amount": amount, "amount_captured": amount, "paid": true, "captured": true, "amount_refunded": 0, "payment_intent": "pi_http_join", "created": created, "refunds": object{"object": "list", "has_more": false, "data": []any{}}, "payment_method_details": object{"type": "card"}}
			_ = json.NewEncoder(w).Encode(session)
			return
		}
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		gets++
		var result object
		switch r.URL.Path {
		case "/v1/checkout/sessions/cs_test_http_join":
			if paid {
				session["status"] = "complete"
				session["payment_status"] = "paid"
				session["payment_intent"] = "pi_http_join"
			}
			result = session
		case "/v1/payment_intents/pi_http_join":
			result = intent
		case "/v1/charges/ch_http_join":
			result = charge
		default:
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	t.Cleanup(provider.Close)
	client, err := stripepay.NewOffline(stripepay.OfflineConfig{Endpoint: provider.URL, ReturnOrigin: f.server.URL, APIKey: stripepay.FixtureAPIKey, WebhookSecret: stripepay.FixtureWebhookSecret})
	if err != nil {
		t.Fatal("TOPUP_INTEGRATION_CLIENT")
	}
	service, err := payments.NewOffline(ctx, f.service, client)
	if err != nil {
		t.Fatal("TOPUP_INTEGRATION_ADMISSION")
	}
	f.host.payments = service
	f.host.fallback = service.WebhookHandler()
	if accountLogin(t, f.browser, keyHTTPA) != 303 {
		t.Fatal("TOPUP_INTEGRATION_AUTH")
	}
	form := paymentForm(t, f.browser)
	response, _ := f.request(t, "POST", "/topup/start", form, f.server.URL)
	if response.StatusCode != 303 || response.Header.Get("Location") != provider.URL+"/checkout/synthetic" {
		t.Fatal("TOPUP_INTEGRATION_REDIRECT")
	}
	response, _ = f.request(t, "POST", "/topup/start", form, f.server.URL)
	if response.StatusCode != 200 {
		t.Fatal("TOPUP_INTEGRATION_DUPLICATE_START")
	}
	mu.Lock()
	paid = true
	mu.Unlock()
	for _, path := range []string{"/topup/return", "/topup/status"} {
		response, body := f.request(t, "GET", path, nil, "")
		if response.StatusCode != 200 || !strings.Contains(body, "funding are not confirmed") {
			t.Fatal("TOPUP_INTEGRATION_PASSIVE_STATUS")
		}
	}
	mu.Lock()
	p, g, reference := posts, gets, ref
	mu.Unlock()
	if p != 1 || g != 0 || len(reference) != 64 {
		t.Fatal("TOPUP_INTEGRATION_PASSIVE_EFFECTS")
	}
	balance, err := f.service.Balance(ctx, accountHTTPA)
	if err != nil || balance.AccountBalance.Balance != "0" {
		t.Fatal("TOPUP_INTEGRATION_PREMATURE_FUNDING")
	}
	event := object{"object": "event", "id": "evt_http_join", "api_version": stripepay.APIVersion, "livemode": false, "type": "checkout.session.completed", "data": object{"object": object{"object": "checkout.session", "id": "cs_test_http_join", "client_reference_id": reference, "livemode": false, "amount_total": 1, "currency": "eur"}}}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal("TOPUP_INTEGRATION_FIXTURE")
	}
	// Separate client: no website session, CSRF, or shared accounting admin key.
	for _, want := range []string{"funded", "already_funded"} {
		req, err := http.NewRequest("POST", f.server.URL+payments.WebhookPath, bytes.NewReader(raw))
		if err != nil {
			t.Fatal("TOPUP_INTEGRATION_REQUEST")
		}
		req.Header.Set("Stripe-Signature", webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: raw, Secret: stripepay.FixtureWebhookSecret, Timestamp: time.Now()}).Header)
		reply, err := f.server.Client().Do(req)
		if err != nil {
			t.Fatal("TOPUP_INTEGRATION_WEBHOOK")
		}
		body, e := io.ReadAll(reply.Body)
		_ = reply.Body.Close()
		if e != nil || reply.StatusCode != 200 || strings.TrimSpace(string(body)) != `{"status":"`+want+`"}` {
			t.Fatal("TOPUP_INTEGRATION_CANONICAL_FUNDING")
		}
	}
	balance, err = f.service.Balance(ctx, accountHTTPA)
	if err != nil || balance.AccountBalance.Balance != "10000000" {
		t.Fatal("TOPUP_INTEGRATION_EXACT_FUNDING")
	}
	other, err := f.service.Balance(ctx, accountHTTPB)
	if err != nil || other.AccountBalance.Balance != "0" {
		t.Fatal("TOPUP_INTEGRATION_ACCOUNT_ISOLATION")
	}
	for _, path := range []string{"/topup/return", "/topup/status"} {
		response, body := f.request(t, "GET", path, nil, "")
		if response.StatusCode != 200 || !strings.Contains(body, "Credit was added once") || strings.Contains(body, reference) || strings.Contains(body, "cs_test_http_join") {
			t.Fatal("TOPUP_INTEGRATION_RECEIPT_PRIVACY")
		}
	}
	mu.Lock()
	p, g = posts, gets
	mu.Unlock()
	if p != 1 || g != 6 {
		t.Fatal("TOPUP_INTEGRATION_NO_EXTRA_PROVIDER_WORK")
	}
}
