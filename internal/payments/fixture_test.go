package payments

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"possums-intake/internal/testfixture"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v87/webhook"
	"possums-intake/internal/ledger"
	"possums-intake/internal/stripepay"
)

const (
	testAccount     = "paymentaccount0"
	testSession     = "cs_test_integration"
	testIntent      = "pi_integration"
	testCharge      = "ch_integration"
	privateSentinel = "PRIVATE_SYNTHETIC_EMAIL_CARD_KEY_EXCEPTION"
)

type object = map[string]any

type fixture struct {
	core                    *ledger.Service
	dir                     string
	service                 *Service
	client                  *stripepay.Client
	provider                *httptest.Server
	webhook                 *httptest.Server
	form                    ledger.PaymentForm
	mu                      sync.Mutex
	posts, gets             int
	session, intent, charge object
	createFail              bool
	readFail                bool
	onCreate                func()
	onCharge                func()
}

func newFixture(t *testing.T, initialize bool) *fixture {
	t.Helper()
	f := &fixture{dir: filepath.Join(t.TempDir(), "state")}
	ctx := context.Background()
	if err := ledger.Init(ctx, f.dir); err != nil {
		t.Fatal(err)
	}
	var err error
	f.core, err = ledger.Open(ctx, f.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.core.Close() })
	if _, err = testfixture.Create(ctx, f.core, testAccount); err != nil {
		t.Fatal(err)
	}
	if initialize {
		if err = f.core.InitializeSyntheticPayments(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// Future monotonic clock fixture forces fractional issuance without sleeping
	// or changing production clock APIs. It remains within provider skew bounds.
	f.exec(t, "UPDATE ledger_meta SET clock_high_water_ms=?", (time.Now().UnixMilli()+10000)/1000*1000+789)
	now := time.Now().UnixMilli()
	f.form = ledger.PaymentForm{Attempt: strings.Repeat("d", 64), IssuedAt: ledger.Millis(now), ExpiresAt: ledger.Millis(now + 600000)}
	f.provider = httptest.NewServer(http.HandlerFunc(f.providerHTTP))
	t.Cleanup(f.provider.Close)
	f.client, err = stripepay.NewOffline(stripepay.OfflineConfig{Endpoint: f.provider.URL, ReturnOrigin: f.provider.URL, APIKey: stripepay.FixtureAPIKey, WebhookSecret: stripepay.FixtureWebhookSecret})
	if err != nil {
		t.Fatal(err)
	}
	if initialize {
		f.service, err = NewOffline(ctx, f.core, f.client)
		if err != nil {
			t.Fatal(err)
		}
		f.mount(t)
	}
	return f
}
func (f *fixture) mount(t *testing.T) {
	f.webhook = httptest.NewServer(f.service.WebhookHandler())
	t.Cleanup(f.webhook.Close)
}
func (f *fixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "data.db"))
	if err != nil {
		t.Fatal("fixture db")
	}
	defer db.Close()
	if _, err = db.Exec(q, args...); err != nil {
		t.Fatal("fixture sql")
	}
}
func (f *fixture) count(t *testing.T, table string) int {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "data.db"))
	if err != nil {
		t.Fatal("fixture db")
	}
	defer db.Close()
	var n int
	if err = db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal("fixture count")
	}
	return n
}
func (f *fixture) assertBalance(t *testing.T, want ledger.Money) {
	t.Helper()
	got, err := f.core.Balance(context.Background(), testAccount)
	if err != nil || got.AccountBalance.Balance != want {
		t.Fatalf("balance mismatch (expected %s)", want)
	}
}
func (f *fixture) start(t *testing.T) string {
	t.Helper()
	got, err := f.service.StartTopup(context.Background(), testAccount, f.form, "usd5")
	if err != nil {
		t.Fatal(err)
	}
	if got.Reference == "" || got.URL != f.provider.URL+"/checkout/synthetic" || got.Status != "awaiting_payment" {
		t.Fatal("start result")
	}
	return got.Reference
}
func (f *fixture) requests() (int, int)     { f.mu.Lock(); defer f.mu.Unlock(); return f.posts, f.gets }
func (f *fixture) change(fn func(*fixture)) { f.mu.Lock(); defer f.mu.Unlock(); fn(f) }

// Every canonical object is generated from the actual SDK create request. No
// production fixture or provider record is copied into this fake server.
func (f *fixture) providerHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Stripe-Version", stripepay.APIVersion)
	w.Header().Set("Stripe-Notice", privateSentinel)
	if r.Method == http.MethodPost {
		f.posts++
		if r.URL.Path != "/v1/checkout/sessions" || r.ParseForm() != nil {
			w.WriteHeader(400)
			return
		}
		amount, err := strconv.ParseInt(r.Form.Get("line_items[0][price_data][unit_amount]"), 10, 64)
		if err != nil {
			w.WriteHeader(400)
			return
		}
		expires, err := strconv.ParseInt(r.Form.Get("expires_at"), 10, 64)
		if err != nil {
			w.WriteHeader(400)
			return
		}
		created := time.Now().Unix()
		f.session = object{"object": "checkout.session", "id": testSession, "livemode": false, "mode": "payment", "ui_mode": "hosted_page", "status": "open", "payment_status": "unpaid", "client_reference_id": r.Form.Get("client_reference_id"), "created": created, "expires_at": expires, "currency": "usd", "amount_total": amount, "amount_subtotal": amount, "customer_creation": "if_required", "payment_method_types": []string{"card"}, "allowed_payment_method_types": []string{"card"}, "url": f.provider.URL + "/checkout/synthetic",
			"customer_details": object{"email": privateSentinel},
			"line_items":       object{"object": "list", "has_more": false, "data": []any{object{"object": "item", "quantity": 1, "currency": "usd", "amount_total": amount, "amount_subtotal": amount, "price": object{"object": "price", "type": "one_time", "billing_scheme": "per_unit", "currency": "usd", "unit_amount": amount}}}},
		}
		f.intent = object{"object": "payment_intent", "id": testIntent, "livemode": false, "status": "succeeded", "currency": "usd", "amount": amount, "amount_received": amount, "capture_method": "automatic", "latest_charge": testCharge, "payment_method_types": []string{"card"}}
		f.charge = object{"object": "charge", "id": testCharge, "livemode": false, "status": "succeeded", "currency": "usd", "amount": amount, "amount_captured": amount, "paid": true, "captured": true, "amount_refunded": 0, "payment_intent": testIntent, "created": created, "refunds": object{"object": "list", "has_more": false, "data": []any{}}, "payment_method_details": object{"type": "card", "card": object{"fingerprint": privateSentinel}}, "billing_details": object{"email": privateSentinel}}
		if f.onCreate != nil {
			f.onCreate()
		}
		if f.createFail {
			w.WriteHeader(500)
			_, _ = io.WriteString(w, privateSentinel)
			return
		}
		_ = json.NewEncoder(w).Encode(f.session)
		return
	}
	f.gets++
	if r.Method != http.MethodGet || f.readFail {
		w.WriteHeader(503)
		_, _ = io.WriteString(w, privateSentinel)
		return
	}
	var result object
	switch r.URL.Path {
	case "/v1/checkout/sessions/" + testSession:
		result = f.session
	case "/v1/payment_intents/" + testIntent:
		result = f.intent
	case "/v1/charges/" + testCharge:
		if f.onCharge != nil {
			f.onCharge()
		}
		result = f.charge
	default:
		w.WriteHeader(404)
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}
func (f *fixture) paid() {
	f.change(func(f *fixture) {
		f.session["status"] = "complete"
		f.session["payment_status"] = "paid"
		f.session["payment_intent"] = testIntent
	})
}
func event(ref string) object {
	return object{"object": "event", "id": "evt_synthetic", "api_version": stripepay.APIVersion, "livemode": false, "type": "checkout.session.completed", "data": object{"object": object{"object": "checkout.session", "id": testSession, "client_reference_id": ref, "livemode": false, "amount_total": 1, "currency": "eur", "payment_status": "unpaid", "customer_email": privateSentinel}}}
}
func signedRequest(t *testing.T, e object) *http.Request {
	t.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal("event fixture")
	}
	req := httptest.NewRequest(http.MethodPost, WebhookPath, bytes.NewReader(raw))
	req.Header.Set("Stripe-Signature", webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: raw, Secret: stripepay.FixtureWebhookSecret, Timestamp: time.Now()}).Header)
	return req
}
func (f *fixture) deliver(t *testing.T, e object) (int, webhookReply) {
	t.Helper()
	req := signedRequest(t, e)
	req.RequestURI = ""
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(f.webhook.URL, "http://")
	response, err := f.webhook.Client().Do(req)
	if err != nil {
		t.Fatal("webhook delivery")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal("webhook response")
	}
	for _, private := range []string{privateSentinel, testSession, testIntent, testCharge, stripepay.FixtureAPIKey, stripepay.FixtureWebhookSecret, "http://", "https://", strings.Repeat("a", 64)} {
		if bytes.Contains(body, []byte(private)) {
			t.Fatal("private webhook response")
		}
	}
	ref := e["data"]
	if data, ok := ref.(object); ok {
		if snapshot, ok := data["object"].(object); ok {
			if value, ok := snapshot["client_reference_id"].(string); ok && value != "" && bytes.Contains(body, []byte(value)) {
				t.Fatal("reference leaked")
			}
		}
	}
	var reply webhookReply
	if json.Unmarshal(body, &reply) != nil {
		t.Fatal("closed response required")
	}
	return response.StatusCode, reply
}
func assertStatus(t *testing.T, core *Service, ref, want string) {
	t.Helper()
	got, err := core.TopupStatus(context.Background(), testAccount, ref)
	if err != nil || got.Status != want || got.Reference != "" || got.URL != "" {
		t.Fatal("local status mismatch")
	}
}
func (f *fixture) restart(t *testing.T) {
	t.Helper()
	if err := f.core.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.core, err = ledger.Open(context.Background(), f.dir)
	if err != nil {
		t.Fatal(err)
	}
	f.service, err = NewOffline(context.Background(), f.core, f.client)
	if err != nil {
		t.Fatal(err)
	}
	f.webhook.Close()
	f.mount(t)
}
func requireErrorType(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected typed error")
	}
	switch err.(type) {
	case *Error, *stripepay.Error, *ledger.Diagnostic:
	default:
		t.Fatalf("untyped boundary: %T", err)
	}
	if strings.Contains(fmt.Sprint(err), privateSentinel) {
		t.Fatal("raw failure escaped")
	}
}
