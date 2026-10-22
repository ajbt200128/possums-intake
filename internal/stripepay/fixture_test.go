package stripepay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	fixtureSession = "cs_test_synthetic"
	fixtureIntent  = "pi_synthetic"
	fixtureCharge  = "ch_synthetic"
	hostile        = "SYNTHETIC_PRIVATE_EMAIL_CARD_KEY_URL_EXCEPTION"
)

type object = map[string]any

type wireRequest struct {
	method string
	path   string
	form   url.Values
	header http.Header
	close  bool
	remote string
}

type fixture struct {
	purchase Purchase
	session  object
	intent   object
	charge   object
	server   *httptest.Server
	mu       sync.Mutex
	requests []wireRequest
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	issued := time.Now().UTC().Truncate(time.Second)
	f := &fixture{purchase: Purchase{Reference: strings.Repeat("a", 64), GrossCents: 500, IssuedAt: issued, ExpiresAt: issued.Add(time.Hour)}}
	f.session = object{
		"id": fixtureSession, "object": "checkout.session", "livemode": false,
		"mode": "payment", "ui_mode": "hosted_page", "payment_status": "paid", "status": "complete",
		"client_reference_id": f.purchase.Reference, "created": issued.Unix(), "expires_at": f.purchase.ExpiresAt.Unix(),
		"currency": "usd", "amount_total": 500, "amount_subtotal": 500,
		"customer_creation": "if_required", "payment_method_types": []string{"card"}, "allowed_payment_method_types": []string{"card"},
		"payment_intent":   fixtureIntent,
		"customer_details": object{"email": hostile, "name": hostile, "address": object{"line1": hostile}},
		"line_items": object{"object": "list", "has_more": false, "data": []any{object{
			"object": "item", "quantity": 1, "currency": "usd", "amount_total": 500, "amount_subtotal": 500,
			"price": object{"object": "price", "type": "one_time", "billing_scheme": "per_unit", "currency": "usd", "unit_amount": 500, "unit_amount_decimal": "500.000000000000", "livemode": false},
		}}},
	}
	f.intent = object{"id": fixtureIntent, "object": "payment_intent", "livemode": false, "status": "succeeded", "currency": "usd", "amount": 500, "amount_received": 500, "amount_capturable": 0, "capture_method": "automatic", "latest_charge": fixtureCharge, "payment_method_types": []string{"card"}}
	f.charge = object{"id": fixtureCharge, "object": "charge", "livemode": false, "status": "succeeded", "currency": "usd", "amount": 500, "amount_captured": 500, "amount_refunded": 0, "paid": true, "captured": true, "disputed": false, "refunded": false,
		"payment_intent": fixtureIntent, "created": issued.Unix(), "refunds": object{"object": "list", "has_more": false, "data": []any{}}, "payment_method_details": object{"type": "card", "card": object{"last4": "4242", "fingerprint": hostile}}, "billing_details": object{"name": hostile, "email": hostile}, "receipt_url": "https://invalid.example/" + hostile,
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.record(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Request-Id", "req_synthetic_prior_request")
		w.Header().Set("Stripe-Notice", hostile)
		w.Header().Set("Stripe-Version", APIVersion)
		var result object
		switch r.URL.Path {
		case "/v1/checkout/sessions", "/v1/checkout/sessions/" + fixtureSession:
			result = f.session
		case "/v1/payment_intents/" + fixtureIntent:
			result = f.intent
		case "/v1/charges/" + fixtureCharge:
			result = f.charge
		default:
			t.Error("unexpected fixture route")
			w.WriteHeader(404)
			return
		}
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Error("fixture encode failed")
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fixture) record(t *testing.T, r *http.Request) {
	t.Helper()
	if err := r.ParseForm(); err != nil {
		t.Error("fixture form decode failed")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, wireRequest{method: r.Method, path: r.URL.Path, form: r.Form, header: r.Header.Clone(), close: r.Close, remote: r.RemoteAddr})
}

func (f *fixture) snapshot() []wireRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]wireRequest(nil), f.requests...)
}

func offlineClient(t *testing.T, endpoint string) *Client {
	t.Helper()
	c, err := NewOffline(OfflineConfig{Endpoint: endpoint, ReturnOrigin: endpoint, APIKey: FixtureAPIKey, WebhookSecret: FixtureWebhookSecret})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func createInput(f *fixture) CreateRequest {
	return CreateRequest{Purchase: f.purchase, IdempotencyKey: strings.Repeat("b", 64)}
}

func makeCreateFixture(f *fixture) {
	f.session["status"] = "open"
	f.session["payment_status"] = "unpaid"
	f.session["payment_intent"] = nil
	f.session["url"] = f.server.URL + "/checkout/synthetic"
}

func requireFailure(t *testing.T, err error, stage Stage, constraint Constraint, outcome Outcome) *Error {
	t.Helper()
	var diagnostic *Error
	if !errors.As(err, &diagnostic) {
		t.Fatal("expected closed diagnostic")
	}
	if diagnostic.Stage != stage || diagnostic.Constraint != constraint || diagnostic.Outcome != outcome {
		t.Fatalf("unexpected closed diagnostic: %s", diagnostic)
	}
	for _, value := range []string{hostile, FixtureAPIKey, FixtureWebhookSecret, fixtureSession, fixtureIntent, fixtureCharge, "https://", "http://"} {
		if strings.Contains(err.Error(), value) || strings.Contains(fmt.Sprintf("%+v", err), value) {
			t.Fatal("private value escaped diagnostic")
		}
	}
	if errors.Unwrap(err) != nil {
		t.Fatal("raw cause retained")
	}
	return diagnostic
}

func captureOutput(t *testing.T, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal("capture setup failed")
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w
	result := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(r); result <- b }()
	defer func() {
		os.Stdout, os.Stderr = stdout, stderr
		_ = w.Close()
		output := <-result
		_ = r.Close()
		if len(output) != 0 {
			t.Error("SDK emitted output; raw bytes withheld")
		}
	}()
	fn()
}

func TestCanonicalEvidenceReadOnly(t *testing.T) {
	f := newFixture(t)
	c := offlineClient(t, f.server.URL)
	got, err := c.RetrieveEvidence(context.Background(), fixtureSession, f.purchase)
	if err != nil {
		t.Fatal(err)
	}
	if got.Session.SessionID != fixtureSession || got.Session.PaymentIntentID != fixtureIntent || got.Session.Reference != f.purchase.Reference || got.Session.Quantity != 1 || got.Session.GrossCents != 500 || got.AmountReceived != 500 || got.AmountCaptured != 500 || !got.Paid || !got.Captured || got.Refunded || got.Disputed || got.AmountRefunded != 0 || got.ChargeCreatedSeconds != f.purchase.IssuedAt.Unix() || got.ChargeDateUTC != f.purchase.IssuedAt.Format("2006-01-02") {
		t.Fatal("canonical projection mismatch")
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), hostile) || strings.Contains(string(encoded), fixtureCharge) || strings.Contains(string(encoded), "4242") {
		t.Fatal("provider details retained in evidence")
	}
	requests := f.snapshot()
	if len(requests) != 3 {
		t.Fatal("expected three bounded reads")
	}
	for _, r := range requests {
		if r.method != http.MethodGet {
			t.Fatal("evidence retrieval mutated provider")
		}
		if r.header.Get("Idempotency-Key") != "" {
			t.Fatal("read received create key")
		}
	}
	if requests[0].form.Get("expand[0]") != "line_items.data.price" || requests[2].form.Get("expand[0]") != "refunds" {
		t.Fatal("canonical expansions missing")
	}
}
