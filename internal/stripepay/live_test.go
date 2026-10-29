package stripepay

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v87"
)

// Public authored shape fixtures, not credentials or data from Stripe.
const (
	syntheticLiveKey    = "sk_live_SyntheticOnlyNotACredential123456"
	syntheticLiveSecret = "whsec_SyntheticOnlyNotASigningSecret123456"
	fixtureLiveSession  = "cs_live_synthetic"
	fixtureTaxCode      = "txcd_10000000" // A shape fixture, NOT a selected classification.
)

func liveConfig() LiveConfig {
	return LiveConfig{ReturnOrigin: "https://payments.example.com", APIKey: syntheticLiveKey, WebhookSecret: syntheticLiveSecret, TaxCode: fixtureTaxCode}
}

func liveClient(t *testing.T, endpoint string) *Client {
	t.Helper()
	c, err := NewLive(liveConfig())
	if err != nil {
		t.Fatal(err)
	}
	// Replace the private SDK BEFORE any operation. Only a bounded loopback
	// authored HTTP fixture is admissible; no exported bypass/config is added.
	u, ok := loopbackOrigin(endpoint)
	if !ok || u.Scheme != "http" {
		t.Fatal("test fixture must be loopback HTTP")
	}
	c.sdk = newFixedSDK(syntheticLiveKey, endpoint)
	return c
}

func newLiveFixture(t *testing.T, principal, tax int64) *fixture {
	t.Helper()
	f := newFixture(t)
	f.purchase.GrossCents = uint64(principal)
	f.purchase.TaxCode = fixtureTaxCode
	f.session["id"] = fixtureLiveSession
	f.session["livemode"] = true
	f.session["amount_subtotal"], f.session["amount_total"] = principal, principal+tax
	f.session["billing_address_collection"] = "auto"
	f.session["automatic_tax"] = object{"enabled": true, "status": "complete", "provider": "stripe", "liability": object{"type": "self", "account": nil}}
	f.session["total_details"] = object{"amount_discount": 0, "amount_shipping": 0, "amount_tax": tax}
	lineItem(f)["amount_subtotal"], lineItem(f)["amount_tax"], lineItem(f)["amount_total"] = principal, tax, principal+tax
	price(f)["unit_amount"], price(f)["unit_amount_decimal"], price(f)["livemode"] = principal, strconv.FormatInt(principal, 10), true
	price(f)["tax_behavior"] = "exclusive"
	price(f)["product"] = object{"id": "prod_synthetic", "object": "product", "livemode": true, "tax_code": fixtureTaxCode}
	f.intent["livemode"], f.intent["amount"], f.intent["amount_received"] = true, principal+tax, principal+tax
	f.charge["livemode"], f.charge["amount"], f.charge["amount_captured"] = true, principal+tax, principal+tax
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.record(t, r)
		w.Header().Set("Stripe-Version", APIVersion)
		w.Header().Set("Stripe-Notice", hostile)
		var result object
		switch r.URL.Path {
		case "/v1/checkout/sessions", "/v1/checkout/sessions/" + fixtureLiveSession:
			result = f.session
		case "/v1/payment_intents/" + fixtureIntent:
			result = f.intent
		case "/v1/charges/" + fixtureCharge:
			result = f.charge
		default:
			t.Error("unexpected live fixture route")
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func TestLiveConstructorInertAndFourBackendPrivacy(t *testing.T) {
	for _, key := range []string{syntheticLiveKey, "rk_live_SyntheticRestricted123"} {
		cfg := liveConfig()
		cfg.APIKey = key
		c, err := NewLive(cfg)
		if err != nil || !c.IsLive() || c.IsSandbox() || c.ProductTaxCode() != fixtureTaxCode || c.CheckoutOrigin() != sandboxCheckoutOrigin || c.returnURL != cfg.ReturnOrigin+ReturnPath {
			t.Fatal("live constructor contract")
		}
		for _, raw := range sandboxBackends(c) {
			b := raw.(*stripe.BackendImplementation)
			tr := b.HTTPClient.Transport.(*offlineTransport)
			logger := b.LeveledLogger.(*stripe.LeveledLogger)
			if b.URL != "https://api.stripe.com" || b.MaxNetworkRetries != 0 || b.StripeContext != nil || logger.Level != stripe.LevelNull || reflect.ValueOf(b).Elem().FieldByName("enableTelemetry").Bool() || tr.origin != b.URL || tr.base.Proxy != nil || !tr.base.DisableKeepAlives || tr.base.ForceAttemptHTTP2 || tr.base.TLSClientConfig != nil || tr.base.DialTLSContext != nil || tr.base.ResponseHeaderTimeout != requestTimeout || tr.base.MaxResponseHeaderBytes != 32<<10 || b.HTTPClient.Timeout != requestTimeout || b.HTTPClient.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
				t.Fatal("live SDK privacy settings changed")
			}
		}
	}
	for _, change := range []func(*LiveConfig){
		func(c *LiveConfig) { c.APIKey = "" }, func(c *LiveConfig) { c.APIKey = syntheticSandboxKey }, func(c *LiveConfig) { c.APIKey = FixtureAPIKey },
		func(c *LiveConfig) { c.APIKey = "sk_live_" }, func(c *LiveConfig) { c.APIKey = "rk_live_Private\n" }, func(c *LiveConfig) { c.APIKey = "sk_live_" + strings.Repeat("A", 257) },
		func(c *LiveConfig) { c.WebhookSecret = FixtureWebhookSecret }, func(c *LiveConfig) { c.WebhookSecret = "whsec_" }, func(c *LiveConfig) { c.WebhookSecret = hostile },
		func(c *LiveConfig) { c.TaxCode = "" }, func(c *LiveConfig) { c.TaxCode = "txcd_1234567" }, func(c *LiveConfig) { c.TaxCode = "txcd_123456789" }, func(c *LiveConfig) { c.TaxCode = "txcd_1234567a" }, func(c *LiveConfig) { c.TaxCode = " txcd_12345678" },
	} {
		cfg := liveConfig()
		change(&cfg)
		c, err := NewLive(cfg)
		d := requireFailure(t, err, StageLiveConfiguration, ConstraintLive, OutcomeNotAttempted)
		if c != nil || d.Code != CodeLiveConfiguration || d.NextStep != NextFixLive {
			t.Fatal("live config refusal")
		}
	}
	for _, origin := range []string{"", "http://payments.example.com", "https://127.0.0.1", "https://[::1]", "https://localhost", "https://x.localhost", "https://x.local", "https://x.internal", "https://home.arpa", "https://x.invalid", "https://123.456", "https://user@payments.example.com", "https://payments.example.com:443", "https://payments.example.com:8443", "https://payments.example.com/", "https://payments.example.com?", "https://payments.example.com#", "https://payments.example.com?q=private", "https://payments.example.com#private", "https:opaque", "HTTPS://payments.example.com", "https://PAYMENTS.example.com", "https://payments.example.com.", "https://-bad.example.com", "https://bad_.example.com"} {
		cfg := liveConfig()
		cfg.ReturnOrigin = origin
		_, err := NewLive(cfg)
		requireFailure(t, err, StageLiveConfiguration, ConstraintLive, OutcomeNotAttempted)
	}
	t.Setenv("STRIPE_API_KEY", syntheticLiveKey)
	_, err := NewLive(LiveConfig{})
	requireFailure(t, err, StageLiveConfiguration, ConstraintLive, OutcomeNotAttempted)
}

func TestLiveClosedModesAndHostedURL(t *testing.T) {
	f := newLiveFixture(t, 500, 40)
	c := liveClient(t, f.server.URL)
	path := sandboxCheckoutOrigin + "/c/pay/" + fixtureLiveSession
	for _, u := range []string{path, path + "#Synthetic%2FFragment=123", sandboxCheckoutOrigin + "/c/pay/" + fixtureSession, path + "?", path + "#", path + "#%00", path + "#" + strings.Repeat("A", 2049), path + "/other"} {
		if !c.CheckoutURLAllowed(u) {
			t.Fatal("live URL refused")
		}
	}
	for _, u := range []string{f.server.URL + "/checkout/synthetic", strings.Replace(path, "checkout.stripe.com", "checkout.stripe.com:443", 1), strings.Replace(path, "checkout.stripe.com", "user@checkout.stripe.com", 1), strings.Replace(path, "checkout.stripe.com", "checkout.stripe.com.invalid", 1), strings.Replace(path, "https:", "http:", 1)} {
		if c.CheckoutURLAllowed(u) {
			t.Fatal("unsafe live URL allowed")
		}
	}
	if sandboxClient(t).CheckoutURLAllowed(path) || sessionPattern.MatchString(fixtureLiveSession) {
		t.Fatal("legacy test policy widened")
	}
	for _, mode := range []string{"zero", "nil", "no_flags", "live_sandbox", "live_offline", "test_offline"} {
		var bad *Client
		switch mode {
		case "nil":
		case "zero":
			bad = &Client{}
		default:
			copy := *c
			bad = &copy
			switch mode {
			case "no_flags":
				bad.live = false
			case "live_sandbox":
				bad.sandbox = true
			case "live_offline":
				bad.offline = true
			case "test_offline":
				bad.live = false
				bad.sandbox = true
				bad.offline = true
			}
		}
		if bad.IsLive() || bad.IsSandbox() || bad.ProductTaxCode() != "" || bad.CheckoutURLAllowed(path) || bad.CheckoutOrigin() != "" {
			t.Fatal("invalid mode grants authority")
		}
		_, err := bad.RetrieveEvidence(context.Background(), fixtureLiveSession, f.purchase)
		requireFailure(t, err, StageSession, ConstraintInput, OutcomeNotAttempted)
	}
	_, err := c.RetrieveEvidence(context.Background(), fixtureSession, f.purchase)
	requireFailure(t, err, StageSession, ConstraintInput, OutcomeNotAttempted)
	o := offlineClient(t, f.server.URL)
	if o.IsLive() || o.ProductTaxCode() != "" || sandboxClient(t).ProductTaxCode() != "" {
		t.Fatal("test grants live config")
	}
	_, err = o.RetrieveEvidence(context.Background(), fixtureLiveSession, f.purchase)
	requireFailure(t, err, StageSession, ConstraintInput, OutcomeNotAttempted)
	if len(f.snapshot()) != 0 {
		t.Fatal("mode refusal dispatched")
	}
}

func TestLiveCreateWireOpenUnresolvedTaxAndPrincipal(t *testing.T) {
	f := newLiveFixture(t, 500, 40)
	c := liveClient(t, f.server.URL)
	f.session["status"], f.session["payment_status"], f.session["payment_intent"] = "open", "unpaid", nil
	f.session["url"] = sandboxCheckoutOrigin + "/c/pay/" + fixtureLiveSession + "#SyntheticFragment"
	f.session["automatic_tax"].(object)["status"] = "requires_location_inputs"
	// Unfinished monetary totals are neither bound nor frozen by create.
	f.session["amount_total"], f.session["total_details"] = nil, nil
	lineItem(f)["amount_tax"], lineItem(f)["amount_total"] = nil, nil
	got, err := c.CreateCheckout(context.Background(), createInput(f))
	if err != nil || got.Session.GrossCents != 500 || !got.Session.LiveMode || got.Session.TaxCode != fixtureTaxCode {
		t.Fatal("open tax binding failed", err)
	}
	r := f.snapshot()[0]
	want := map[string]string{
		"mode": "payment", "ui_mode": "hosted_page", "customer_creation": "if_required", "client_reference_id": f.purchase.Reference,
		"success_url": liveConfig().ReturnOrigin + ReturnPath, "cancel_url": liveConfig().ReturnOrigin + ReturnPath,
		"allowed_payment_method_types[0]": "card", "wallet_options[link][display]": "never", "payment_intent_data[capture_method]": "automatic",
		"adaptive_pricing[enabled]": "false", "allow_promotion_codes": "false", "after_expiration[recovery][enabled]": "false", "invoice_creation[enabled]": "false",
		"line_items[0][quantity]": "1", "line_items[0][adjustable_quantity][enabled]": "false", "line_items[0][price_data][currency]": "usd", "line_items[0][price_data][unit_amount]": "500",
		"line_items[0][price_data][product_data][name]": "Prepaid inference credit", "line_items[0][price_data][product_data][tax_code]": fixtureTaxCode, "line_items[0][price_data][tax_behavior]": "exclusive",
		"automatic_tax[enabled]": "true", "automatic_tax[liability][type]": "self", "billing_address_collection": "auto", "expand[0]": "line_items.data.price.product",
	}
	want["expires_at"] = strconv.FormatInt(f.purchase.ExpiresAt.Unix(), 10)
	if len(r.form) != len(want) {
		t.Fatal("unexpected live create field")
	}
	for key, value := range want {
		if len(r.form[key]) != 1 || r.form.Get(key) != value {
			t.Fatalf("live field mismatch: %s", key)
		}
	}
	if r.method != "POST" || len(f.snapshot()) != 1 || r.header.Get("Authorization") != "Bearer "+syntheticLiveKey || r.header.Get("Stripe-Version") != APIVersion || !r.close || r.header.Get("X-Stripe-Client-Telemetry") != "" {
		t.Fatal("live create dispatch boundary")
	}
	p := createInput(f)
	p.Purchase.TaxCode = "txcd_20000000"
	_, err = c.CreateCheckout(context.Background(), p)
	requireFailure(t, err, StageCreate, ConstraintTax, OutcomeNotAttempted)
	if len(f.snapshot()) != 1 {
		t.Fatal("changed classification dispatched")
	}
}

func TestLiveCanonicalTaxPrincipalAndOriginalCode(t *testing.T) {
	for _, principal := range []int64{500, 1000, 2500, 5000} {
		for _, tax := range []int64{0, 43} {
			f := newLiveFixture(t, principal, tax)
			c := liveClient(t, f.server.URL)
			// Earlier retained purchase keeps its original code after reconfiguration.
			c.taxCode = "txcd_20000000"
			if tax == 0 {
				price(f)["product"].(object)["tax_code"] = object{"id": fixtureTaxCode, "object": "tax_code"}
			}
			got, err := c.RetrieveEvidence(context.Background(), fixtureLiveSession, f.purchase)
			if err != nil || got.Session.GrossCents != uint64(principal) || !got.Session.LiveMode || got.Session.TaxCode != fixtureTaxCode || got.AmountReceived != uint64(principal+tax) || got.AmountCaptured != uint64(principal+tax) || got.Tax == nil || *got.Tax != (TaxEvidence{AmountCents: uint64(tax), TotalCents: uint64(principal + tax), Complete: true, Exclusive: true, LiabilitySelf: true}) {
				t.Fatal("live final projection", err)
			}
			b, _ := json.Marshal(got)
			for _, private := range []string{hostile, "prod_synthetic", fixtureCharge, "4242"} {
				if strings.Contains(string(b), private) {
					t.Fatal("raw provider details escaped")
				}
			}
			r := f.snapshot()
			if len(r) != 3 || r[0].form.Get("expand[0]") != "line_items.data.price.product" || r[2].form.Get("expand[0]") != "refunds" {
				t.Fatal("expanded three-GET bound")
			}
			for _, req := range r {
				if req.method != "GET" || req.header.Get("Idempotency-Key") != "" {
					t.Fatal("evidence mutated provider")
				}
			}
		}
	}
}

func TestLiveCanonicalTaxMismatchesReturnZero(t *testing.T) {
	tests := []struct {
		name       string
		stage      Stage
		constraint Constraint
		change     func(*fixture)
	}{
		{"test_session", StageSession, ConstraintLiveContext, func(f *fixture) { f.session["livemode"] = false }},
		{"test_id", StageSession, ConstraintSession, func(f *fixture) { f.session["id"] = fixtureSession }},
		{"subtotal", StageSession, ConstraintSession, func(f *fixture) { f.session["amount_subtotal"] = 501 }},
		{"session_currency", StageSession, ConstraintSession, func(f *fixture) { f.session["currency"] = "eur" }},
		{"open", StageSession, ConstraintPaid, func(f *fixture) { f.session["status"] = "open" }},
		{"unpaid", StageSession, ConstraintPaid, func(f *fixture) { f.session["payment_status"] = "unpaid" }},
		{"tax_missing", StageSession, ConstraintTax, func(f *fixture) { f.session["automatic_tax"] = nil }},
		{"tax_disabled", StageSession, ConstraintTax, func(f *fixture) { f.session["automatic_tax"].(object)["enabled"] = false }},
		{"provider_unknown", StageSession, ConstraintTax, func(f *fixture) { f.session["automatic_tax"].(object)["provider"] = "future" }},
		{"provider_null", StageSession, ConstraintTaxComplete, func(f *fixture) { f.session["automatic_tax"].(object)["provider"] = nil }},
		{"provider_missing", StageSession, ConstraintTaxComplete, func(f *fixture) { delete(f.session["automatic_tax"].(object), "provider") }},
		{"liability_null", StageSession, ConstraintTaxComplete, func(f *fixture) { f.session["automatic_tax"].(object)["liability"] = nil }},
		{"liability_account", StageSession, ConstraintTax, func(f *fixture) {
			f.session["automatic_tax"].(object)["liability"] = object{"type": "account", "account": "acct_synthetic"}
		}},
		{"liability_self_account", StageSession, ConstraintTax, func(f *fixture) {
			f.session["automatic_tax"].(object)["liability"] = object{"type": "self", "account": "acct_synthetic"}
		}},
		{"liability_unknown", StageSession, ConstraintTax, func(f *fixture) { f.session["automatic_tax"].(object)["liability"] = object{"type": "future"} }},
		{"inclusive", StageSession, ConstraintTax, func(f *fixture) { price(f)["tax_behavior"] = "inclusive" }},
		{"unspecified", StageSession, ConstraintTax, func(f *fixture) { delete(price(f), "tax_behavior") }},
		{"product_unexpanded", StageSession, ConstraintTax, func(f *fixture) { price(f)["product"] = "prod_synthetic" }},
		{"product_test", StageSession, ConstraintTax, func(f *fixture) { price(f)["product"].(object)["livemode"] = false }},
		{"product_deleted", StageSession, ConstraintTax, func(f *fixture) { price(f)["product"].(object)["deleted"] = true }},
		{"code_wrong", StageSession, ConstraintTax, func(f *fixture) { price(f)["product"].(object)["tax_code"] = "txcd_20000000" }},
		{"code_null", StageSession, ConstraintTax, func(f *fixture) { price(f)["product"].(object)["tax_code"] = nil }},
		{"performance_location", StageSession, ConstraintTax, func(f *fixture) {
			price(f)["product"].(object)["tax_details"] = object{"tax_code": fixtureTaxCode, "performance_location": "taxloc_synthetic"}
		}},
		{"tax_details_mismatch", StageSession, ConstraintTax, func(f *fixture) { price(f)["product"].(object)["tax_details"] = object{"tax_code": "txcd_20000000"} }},
		{"session_total", StageSession, ConstraintTaxComplete, func(f *fixture) { f.session["amount_total"] = 541 }},
		{"negative_total", StageSession, ConstraintTaxComplete, func(f *fixture) { f.session["amount_total"] = -1 }},
		{"total_details_missing", StageSession, ConstraintTaxComplete, func(f *fixture) { f.session["total_details"] = nil }},
		{"negative_tax", StageSession, ConstraintTaxComplete, func(f *fixture) { f.session["total_details"].(object)["amount_tax"] = -1 }},
		{"overflow_tax", StageSession, ConstraintTaxComplete, func(f *fixture) { f.session["total_details"].(object)["amount_tax"] = int64(math.MaxInt64) }},
		{"line_tax", StageSession, ConstraintTaxComplete, func(f *fixture) { lineItem(f)["amount_tax"] = 41 }},
		{"line_total", StageSession, ConstraintTaxComplete, func(f *fixture) { lineItem(f)["amount_total"] = 541 }},
		{"line_subtotal", StageSession, ConstraintLineItem, func(f *fixture) { lineItem(f)["amount_subtotal"] = 501 }},
		{"line_currency", StageSession, ConstraintLineItem, func(f *fixture) { lineItem(f)["currency"] = "eur" }},
		{"line_quantity", StageSession, ConstraintLineItem, func(f *fixture) { lineItem(f)["quantity"] = 2 }},
		{"line_discount", StageSession, ConstraintLineItem, func(f *fixture) { lineItem(f)["amount_discount"] = 1 }},
		{"price_test", StageSession, ConstraintLineItem, func(f *fixture) { price(f)["livemode"] = false }},
		{"price_amount", StageSession, ConstraintLineItem, func(f *fixture) { price(f)["unit_amount"] = 501 }},
		{"discount", StageSession, ConstraintTax, func(f *fixture) { f.session["total_details"].(object)["amount_discount"] = 1 }},
		{"shipping", StageSession, ConstraintTax, func(f *fixture) { f.session["total_details"].(object)["amount_shipping"] = 1 }},
		{"tax_ids", StageSession, ConstraintTax, func(f *fixture) { f.session["tax_id_collection"] = object{"enabled": true} }},
		{"invoice", StageSession, ConstraintTax, func(f *fixture) { f.session["invoice_creation"] = object{"enabled": true} }},
		{"managed", StageSession, ConstraintTax, func(f *fixture) { f.session["managed_payments"] = object{"enabled": true} }},
		{"pi_test", StageIntent, ConstraintLiveContext, func(f *fixture) { f.intent["livemode"] = false }},
		{"pi_principal_only", StageIntent, ConstraintIntent, func(f *fixture) { f.intent["amount"] = 500 }},
		{"pi_received", StageIntent, ConstraintIntent, func(f *fixture) { f.intent["amount_received"] = 500 }},
		{"pi_currency", StageIntent, ConstraintIntent, func(f *fixture) { f.intent["currency"] = "eur" }},
		{"pi_processing", StageIntent, ConstraintIntent, func(f *fixture) { f.intent["status"] = "processing" }},
		{"pi_3ds", StageIntent, ConstraintIntent, func(f *fixture) { f.intent["status"] = "requires_action" }},
		{"pi_customer", StageIntent, ConstraintIntent, func(f *fixture) { f.intent["customer"] = "cus_synthetic" }},
		{"charge_test", StageCharge, ConstraintLiveContext, func(f *fixture) { f.charge["livemode"] = false }},
		{"charge_principal_only", StageCharge, ConstraintCharge, func(f *fixture) { f.charge["amount"] = 500 }},
		{"charge_captured", StageCharge, ConstraintCharge, func(f *fixture) { f.charge["amount_captured"] = 500 }},
		{"charge_currency", StageCharge, ConstraintCharge, func(f *fixture) { f.charge["currency"] = "eur" }},
		{"charge_customer", StageCharge, ConstraintCharge, func(f *fixture) { f.charge["customer"] = "cus_synthetic" }},
		{"refund_unavailable", StageCharge, ConstraintRefundUnavailable, func(f *fixture) { f.charge["refunds"] = nil }},
	}
	for _, status := range []string{"", "requires_location_inputs", "failed", "future"} {
		status := status
		tests = append(tests, struct {
			name       string
			stage      Stage
			constraint Constraint
			change     func(*fixture)
		}{"status_" + status, StageSession, ConstraintTaxComplete, func(f *fixture) { f.session["automatic_tax"].(object)["status"] = status }})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLiveFixture(t, 500, 40)
			tt.change(f)
			c := liveClient(t, f.server.URL)
			got, err := c.RetrieveEvidence(context.Background(), fixtureLiveSession, f.purchase)
			requireFailure(t, err, tt.stage, tt.constraint, OutcomeReadUnavailable)
			if got != (Evidence{}) {
				t.Fatal("unvalidated evidence escaped")
			}
			r := f.snapshot()
			if len(r) > 3 {
				t.Fatal("GET chain exceeded")
			}
			for _, req := range r {
				if req.method != "GET" {
					t.Fatal("negative read mutated")
				}
			}
		})
	}
}

func TestLiveRefundOnlyValidatedPartialOutcome(t *testing.T) {
	for _, kind := range []string{"partial", "dispute", "listed"} {
		t.Run(kind, func(t *testing.T) {
			f := newLiveFixture(t, 500, 40)
			switch kind {
			case "partial":
				f.charge["amount_refunded"] = 1
			case "dispute":
				f.charge["disputed"] = true
			case "listed":
				f.charge["refunds"].(object)["data"] = []any{object{"id": "re_synthetic", "object": "refund", "amount": 1, "currency": "usd", "charge": fixtureCharge, "payment_intent": fixtureIntent, "status": "future"}}
			}
			c := liveClient(t, f.server.URL)
			got, err := c.RetrieveEvidence(context.Background(), fixtureLiveSession, f.purchase)
			requireFailure(t, err, StageCharge, ConstraintRefund, OutcomeReadUnavailable)
			if got.Tax == nil || got.Tax.TotalCents != 540 || got.Session.GrossCents != 500 || !(got.AmountRefunded != 0 || got.Disputed || got.RefundObserved) {
				t.Fatal("validated block evidence lost")
			}
		})
	}
}

func TestLiveProviderFaultsClosedOnePOSTAndNoReplay(t *testing.T) {
	for _, scenario := range []string{"decode", "status", "conflict", "server", "redirect", "context", "version", "transport", "truncated", "oversize"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Stripe-Notice", hostile)
				w.Header().Set("Stripe-Should-Retry", "true")
				switch scenario {
				case "transport":
					conn, _, _ := w.(http.Hijacker).Hijack()
					_ = conn.Close()
					return
				case "truncated":
					w.Header().Set("Content-Length", "10000")
				case "oversize":
					_, _ = w.Write([]byte(strings.Repeat(hostile, MaxResponseBytes/len(hostile)+2)))
					return
				case "conflict":
					w.WriteHeader(409)
				case "server":
					w.WriteHeader(500)
				case "status":
					w.WriteHeader(429)
				case "redirect":
					w.Header().Set("Location", "/replay")
					w.WriteHeader(307)
				case "context":
					w.Header().Set("Stripe-Context", hostile)
				case "version":
					w.Header().Set("Stripe-Version", "2020-01-01")
				}
				_, _ = w.Write([]byte(`{"error":{"message":"` + hostile + `","type":"future_error"}}`))
				if scenario == "decode" {
					_, _ = w.Write([]byte(hostile))
				}
			}))
			defer s.Close()
			f := newLiveFixture(t, 500, 40)
			c := liveClient(t, s.URL)
			constraint := map[string]Constraint{"decode": ConstraintDecode, "status": ConstraintStatus, "conflict": ConstraintStatus, "server": ConstraintStatus, "redirect": ConstraintRedirect, "context": ConstraintLiveContext, "version": ConstraintLiveContext, "transport": ConstraintTransport, "truncated": ConstraintTransport, "oversize": ConstraintResponseSize}[scenario]
			captureOutput(t, func() {
				got, err := c.CreateCheckout(context.Background(), createInput(f))
				requireFailure(t, err, StageCreate, constraint, OutcomeCreateUnknown)
				if got != (Checkout{}) {
					t.Error("failed create projection")
				}
			})
			if calls.Load() != 1 {
				t.Fatal("live automatic POST replay")
			}
		})
	}
}

func TestLiveWebhookActualSDKSignatureModeVersionAndNoFunding(t *testing.T) {
	f := newLiveFixture(t, 500, 40)
	c := liveClient(t, f.server.URL)
	for _, scenario := range []string{"valid", "test_event", "test_session", "test_id", "account", "context", "version", "old", "wrong_secret", "altered"} {
		t.Run(scenario, func(t *testing.T) {
			e := eventFixture()
			e["livemode"] = true
			s := e["data"].(object)["object"].(object)
			s["id"], s["livemode"] = fixtureLiveSession, true
			constraint := ConstraintLiveContext
			switch scenario {
			case "test_event":
				e["livemode"] = false
			case "test_session":
				s["livemode"] = false
				constraint = ConstraintEvent
			case "test_id":
				s["id"] = fixtureSession
				constraint = ConstraintEvent
			case "account":
				e["account"] = hostile
			case "context":
				e["context"] = hostile
			case "version":
				e["api_version"] = "2026-10-31.endive"
			}
			raw, _ := json.Marshal(e)
			at := time.Now()
			secret := syntheticLiveSecret
			if scenario == "old" {
				at = at.Add(-6 * time.Minute)
				constraint = ConstraintSignature
			}
			if scenario == "wrong_secret" {
				secret = FixtureWebhookSecret
				constraint = ConstraintSignature
			}
			sig := signFixture(raw, secret, at)
			if scenario == "altered" {
				raw = append(raw, ' ')
				constraint = ConstraintSignature
			}
			got, err := c.DecodeWebhook(raw, sig)
			if scenario == "valid" {
				if err != nil || got.Kind != EventCheckoutCompleted || got.SessionID != fixtureLiveSession {
					t.Fatal("live notification selector", err)
				}
			} else {
				d := requireFailure(t, err, StageWebhook, constraint, OutcomeWebhookRejected)
				if d.NextStep != NextWebhookLive {
					t.Fatal("live diagnostic carries fixture instructions")
				}
				if got != (Notification{}) {
					t.Fatal("invalid selector escaped")
				}
			}
		})
	}
	if len(f.snapshot()) != 0 {
		t.Fatal("webhook performed provider work")
	}
}

func TestLiveReadErrorsAreClosedAtEachStageWithoutReplay(t *testing.T) {
	for index, stage := range []Stage{StageSession, StageIntent, StageCharge} {
		for _, status := range []int{200, 500} {
			t.Run(string(stage)+strconv.Itoa(status), func(t *testing.T) {
				f := newLiveFixture(t, 500, 40)
				var calls atomic.Int32
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != "GET" {
						t.Error("read failure mutated provider")
					}
					current, result := StageSession, f.session
					if strings.HasPrefix(r.URL.Path, "/v1/payment_intents/") {
						current, result = StageIntent, f.intent
					}
					if strings.HasPrefix(r.URL.Path, "/v1/charges/") {
						current, result = StageCharge, f.charge
					}
					if current == stage {
						w.Header().Set("Stripe-Should-Retry", "true")
						w.Header().Set("Stripe-Notice", hostile)
						w.WriteHeader(status)
						_, _ = w.Write([]byte(hostile))
						return
					}
					_ = json.NewEncoder(w).Encode(result)
				}))
				defer s.Close()
				c := liveClient(t, s.URL)
				captureOutput(t, func() {
					got, err := c.RetrieveEvidence(context.Background(), fixtureLiveSession, f.purchase)
					constraint := ConstraintDecode
					if status == 500 {
						constraint = ConstraintStatus
					}
					d := requireFailure(t, err, stage, constraint, OutcomeReadUnavailable)
					if got != (Evidence{}) || d.HTTPStatus != status {
						t.Fatal("read failure lost zero evidence/status")
					}
				})
				if calls.Load() != int32(index+1) {
					t.Fatal("read failure replayed or exceeded chain")
				}
			})
		}
	}
}

func TestTestModesRefuseTaxAndLivePresentationFailureNeverReplays(t *testing.T) {
	f := newFixture(t)
	for _, c := range []*Client{offlineClient(t, f.server.URL), sandboxClient(t)} {
		c.sdk = newFixedSDK(FixtureAPIKey, f.server.URL)
		p := createInput(f)
		p.Purchase.TaxCode = fixtureTaxCode
		_, err := c.CreateCheckout(context.Background(), p)
		requireFailure(t, err, StageCreate, ConstraintInput, OutcomeNotAttempted)
		_, err = c.RetrieveEvidence(context.Background(), fixtureSession, p.Purchase)
		requireFailure(t, err, StageSession, ConstraintInput, OutcomeNotAttempted)
	}
	if len(f.snapshot()) != 0 {
		t.Fatal("test-mode tax input dispatched")
	}
	live := newLiveFixture(t, 500, 0)
	c := liveClient(t, live.server.URL)
	live.session["status"], live.session["payment_status"], live.session["payment_intent"] = "open", "unpaid", nil
	live.session["url"] = "https://checkout.stripe.com.invalid/c/pay/" + fixtureLiveSession
	_, err := c.CreateCheckout(context.Background(), createInput(live))
	requireFailure(t, err, StageHostedURL, ConstraintLiveURL, OutcomeCreateUnknown)
	if len(live.snapshot()) != 1 {
		t.Fatal("live presentation failure replayed create")
	}
	live.session["status"], live.session["payment_status"], live.session["payment_intent"] = "complete", "paid", fixtureIntent
	live.session["automatic_tax"].(object)["status"] = "requires_location_inputs"
	got, err := c.RetrieveEvidence(context.Background(), fixtureLiveSession, live.purchase)
	requireFailure(t, err, StageSession, ConstraintTaxComplete, OutcomeReadUnavailable)
	if got != (Evidence{}) || len(live.snapshot()) != 2 {
		t.Fatal("zero tax bypassed completed tax evidence")
	}
}
