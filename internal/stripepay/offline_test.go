package stripepay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v87"
)

func TestOfflineConfigurationAndInvalidInputsNeverDispatch(t *testing.T) {
	f := newFixture(t)
	valid := OfflineConfig{Endpoint: f.server.URL, ReturnOrigin: f.server.URL, APIKey: FixtureAPIKey, WebhookSecret: FixtureWebhookSecret}
	for _, endpoint := range []string{"https://api.stripe.com", "http://localhost:8080", "http://example.invalid:8080", "http://192.0.2.1:8080", "http://127.0.0.1", f.server.URL + "/", f.server.URL + "?", f.server.URL + "#private", "http://user@127.0.0.1:8080", "http://127.0.0.1:00080", "http://[::1%25lo0]:8080", "https://127.0.0.1:8080"} {
		cfg := valid
		cfg.Endpoint = endpoint
		_, err := NewOffline(cfg)
		requireFailure(t, err, StageConfiguration, ConstraintOffline, OutcomeNotAttempted)
	}
	for _, origin := range []string{"https://example.invalid:443", "http://example.invalid:80", "https://127.0.0.1:8443/path", f.server.URL + "?selector=private", f.server.URL + "#private"} {
		cfg := valid
		cfg.ReturnOrigin = origin
		_, err := NewOffline(cfg)
		requireFailure(t, err, StageConfiguration, ConstraintOffline, OutcomeNotAttempted)
	}
	for _, key := range []string{"", "sk_live_not_a_secret", "sk_test_not_a_secret", hostile} {
		cfg := valid
		cfg.APIKey = key
		_, err := NewOffline(cfg)
		requireFailure(t, err, StageConfiguration, ConstraintOffline, OutcomeNotAttempted)
	}
	cfg := valid
	cfg.WebhookSecret = "whsec_not_a_secret"
	_, err := NewOffline(cfg)
	requireFailure(t, err, StageConfiguration, ConstraintOffline, OutcomeNotAttempted)
	// No environment lookup may turn an empty configuration into an active one.
	t.Setenv("STRIPE_SECRET_KEY", FixtureAPIKey)
	t.Setenv("STRIPE_WEBHOOK_SECRET", FixtureWebhookSecret)
	_, err = NewOffline(OfflineConfig{})
	requireFailure(t, err, StageConfiguration, ConstraintOffline, OutcomeNotAttempted)
	c := offlineClient(t, f.server.URL)
	for _, change := range []func(*CreateRequest){
		func(p *CreateRequest) { p.Purchase.Reference = hostile },
		func(p *CreateRequest) { p.IdempotencyKey = hostile },
		func(p *CreateRequest) { p.IdempotencyKey = p.Purchase.Reference },
		func(p *CreateRequest) { p.Purchase.GrossCents = 0 },
		func(p *CreateRequest) { p.Purchase.GrossCents = 501 },
		func(p *CreateRequest) { p.Purchase.GrossCents = ^uint64(0) },
		func(p *CreateRequest) { p.Purchase.ExpiresAt = p.Purchase.ExpiresAt.Add(time.Second) },
		func(p *CreateRequest) {
			p.Purchase.IssuedAt = time.Now().Add(-10 * time.Minute)
			p.Purchase.ExpiresAt = p.Purchase.IssuedAt.Add(time.Hour)
		},
	} {
		in := createInput(f)
		change(&in)
		_, err := c.CreateCheckout(context.Background(), in)
		requireFailure(t, err, StageCreate, ConstraintInput, OutcomeNotAttempted)
	}
	for _, id := range []string{"", "cs_live_synthetic", fixtureSession + "/../capture", hostile} {
		_, err := c.RetrieveEvidence(context.Background(), id, f.purchase)
		requireFailure(t, err, StageSession, ConstraintInput, OutcomeNotAttempted)
	}
	var zero Client
	_, err = zero.CreateCheckout(context.Background(), createInput(f))
	requireFailure(t, err, StageCreate, ConstraintInput, OutcomeNotAttempted)
	_, err = c.CreateCheckout(nil, createInput(f))
	requireFailure(t, err, StageCreate, ConstraintInput, OutcomeNotAttempted)
	if len(f.snapshot()) != 0 {
		t.Fatal("invalid inputs dispatched network work")
	}
}

func TestCreateWirePrivacyAndFixedTerms(t *testing.T) {
	f := newFixture(t)
	makeCreateFixture(f)
	c := offlineClient(t, f.server.URL)
	got, err := c.CreateCheckout(context.Background(), createInput(f))
	if err != nil {
		t.Fatal(err)
	}
	if got.HostedURL != f.session["url"] || got.Session.PaymentIntentID != "" {
		t.Fatal("create projection mismatch")
	}
	r := f.snapshot()[0]
	want := map[string]string{
		"mode": "payment", "ui_mode": "hosted_page", "customer_creation": "if_required", "client_reference_id": f.purchase.Reference,
		"success_url": f.server.URL + ReturnPath, "cancel_url": f.server.URL + ReturnPath,
		"expires_at":                      strconv.FormatInt(f.purchase.ExpiresAt.Unix(), 10),
		"allowed_payment_method_types[0]": "card", "payment_intent_data[capture_method]": "automatic",
		"wallet_options[link][display]": "never",
		"adaptive_pricing[enabled]": "false", "allow_promotion_codes": "false",
		"after_expiration[recovery][enabled]": "false", "invoice_creation[enabled]": "false",
		"line_items[0][quantity]": "1", "line_items[0][adjustable_quantity][enabled]": "false",
		"line_items[0][price_data][currency]": "usd", "line_items[0][price_data][unit_amount]": "500",
		"line_items[0][price_data][product_data][name]": "Synthetic prepaid credit", "expand[0]": "line_items.data.price",
	}
	if len(r.form) != len(want) {
		t.Fatal("unexpected outbound request field")
	}
	for key, value := range want {
		if len(r.form[key]) != 1 || r.form.Get(key) != value {
			t.Fatalf("fixed outbound field mismatch: %s", key)
		}
	}
	if r.method != http.MethodPost || r.header.Get("Idempotency-Key") != createInput(f).IdempotencyKey || r.header.Get("Stripe-Version") != APIVersion || r.header.Get("Stripe-Account") != "" || r.header.Get("Stripe-Context") != "" {
		t.Fatal("SDK request contract mismatch")
	}
	for _, key := range []string{"payment_method_data[allow_redisplay]", "payment_intent_data[setup_future_usage]", "saved_payment_method_options[payment_method_save]", "customer", "customer_account"} {
		if r.form.Has(key) {
			t.Fatal("unsupported redisplay override or saved-customer configuration reached creation request")
		}
	}
	if strings.Contains(r.form.Encode(), hostile) {
		t.Fatal("personal/provider details reached creation request")
	}
	for _, key := range []string{"success_url", "cancel_url"} {
		if strings.Contains(r.form.Get(key), "?") || strings.Contains(r.form.Get(key), f.purchase.Reference) {
			t.Fatal("return URL carries selector")
		}
	}
}

func TestSDKNoOutputTelemetryOrAutomaticRetry(t *testing.T) {
	// Null logging must suppress BOTH notice branches, regardless of notice
	// suppression, actual agent detection or test/sandbox provider behavior.
	for _, key := range []string{"ANTIGRAVITY_CLI_ALIAS", "CLAUDECODE", "CLINE_ACTIVE", "CODEX_SANDBOX", "CODEX_THREAD_ID", "CODEX_SANDBOX_NETWORK_DISABLED", "CODEX_CI", "CURSOR_AGENT", "GEMINI_CLI", "HERMES_AGENT", "OPENCLAW_SHELL", "OPENCODE"} {
		t.Setenv(key, "")
	}
	originalLogger := stripe.DefaultLeveledLogger
	for _, agent := range []string{"", "true"} {
		for _, suppress := range []string{"false", "true"} {
			for _, otel := range []string{"false", "true"} {
				t.Setenv("CODEX_CI", agent)
				t.Setenv("STRIPE_SUPPRESS_NOTICES", suppress)
				t.Setenv("OTEL_SDK_DISABLED", otel)
				f := newFixture(t)
				makeCreateFixture(f)
				c := offlineClient(t, f.server.URL)
				captureOutput(t, func() {
					for i := 0; i < 3; i++ {
						if _, err := c.CreateCheckout(context.Background(), createInput(f)); err != nil {
							t.Error(err)
						}
					}
				})
				requests := f.snapshot()
				if len(requests) != 3 {
					t.Fatal("unexpected dispatch count")
				}
				for i, r := range requests {
					if r.header.Get("X-Stripe-Client-Telemetry") != "" {
						t.Fatal("SDK prior-request telemetry escaped")
					}
					if !r.close {
						t.Fatal("transport connection reuse enabled")
					}
					if i > 0 && r.remote == requests[i-1].remote {
						t.Fatal("connection unexpectedly reused")
					}
				}
			}
		}
	}
	if stripe.DefaultLeveledLogger != originalLogger {
		t.Fatal("global logger mutated")
	}
}

func TestCreateProviderFaultsOneDispatchAndClosedUncertainty(t *testing.T) {
	for _, scenario := range []string{"transport", "truncated", "oversize", "decode", "sdk_error", "unknown_error", "redirect301", "redirect302", "redirect303", "redirect307", "redirect308", "conflict", "throttle", "server_error", "wrong_context", "wrong_version"} {
		t.Run(scenario, func(t *testing.T) {
			var calls, redirectCalls atomic.Int32
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path == "/redirect-target" {
					redirectCalls.Add(1)
					return
				}
				w.Header().Set("Stripe-Notice", hostile)
				w.Header().Set("Stripe-Should-Retry", "true")
				switch scenario {
				case "transport":
					conn, _, _ := w.(http.Hijacker).Hijack()
					_ = conn.Close()
				case "truncated":
					w.Header().Set("Content-Length", "500")
					w.WriteHeader(200)
					_, _ = w.Write([]byte("{"))
				case "oversize":
					_, _ = w.Write([]byte(strings.Repeat(hostile, MaxResponseBytes/len(hostile)+2)))
				case "decode":
					_, _ = w.Write([]byte(hostile))
				case "sdk_error":
					w.WriteHeader(401)
					_, _ = w.Write([]byte(`{"error":{"type":"authentication_error","message":"` + hostile + `"}}`))
				case "unknown_error":
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"error":{"type":"future_error","message":"` + hostile + `"}}`))
				case "conflict":
					w.WriteHeader(409)
					_, _ = w.Write([]byte(hostile))
				case "throttle":
					w.WriteHeader(429)
					_, _ = w.Write([]byte(hostile))
				case "server_error":
					w.WriteHeader(500)
					_, _ = w.Write([]byte(hostile))
				case "wrong_context":
					w.Header().Set("Stripe-Context", hostile)
					_, _ = w.Write([]byte("{}"))
				case "wrong_version":
					w.Header().Set("Stripe-Version", "2020-01-01")
					_, _ = w.Write([]byte("{}"))
				default:
					status := map[string]int{"redirect301": 301, "redirect302": 302, "redirect303": 303, "redirect307": 307, "redirect308": 308}[scenario]
					w.Header().Set("Location", server.URL+"/redirect-target")
					w.WriteHeader(status)
					_, _ = w.Write([]byte(hostile))
				}
			}))
			defer server.Close()
			f := newFixture(t)
			c := offlineClient(t, server.URL)
			captureOutput(t, func() {
				_, err := c.CreateCheckout(context.Background(), createInput(f))
				constraint := ConstraintStatus
				switch scenario {
				case "transport", "truncated":
					constraint = ConstraintTransport
				case "oversize":
					constraint = ConstraintResponseSize
				case "decode":
					constraint = ConstraintDecode
				case "wrong_context", "wrong_version":
					constraint = ConstraintContext
				}
				if strings.HasPrefix(scenario, "redirect") {
					constraint = ConstraintRedirect
				}
				d := requireFailure(t, err, StageCreate, constraint, OutcomeCreateUnknown)
				if scenario == "sdk_error" && d.HTTPStatus != 401 {
					t.Error("observed HTTP status lost")
				}
				if scenario == "decode" && !d.CauseUnknown {
					t.Error("decode cause incorrectly claimed known")
				}
			})
			if calls.Load() != 1 || redirectCalls.Load() != 0 {
				t.Fatal("automatic mutation replay or redirect dispatched")
			}
		})
	}
}

func TestCreatePresentationFailurePreservesUnknownOutcome(t *testing.T) {
	for _, suffix := range []string{"", "https://checkout.stripe.com/private", "http://localhost:8080/private", "http://127.0.0.1:8080/private", "@user", "/checkout/synthetic?session=private", "/checkout/synthetic#private", "/%2fprivate"} {
		f := newFixture(t)
		makeCreateFixture(f)
		value := suffix
		if strings.HasPrefix(suffix, "/") {
			value = f.server.URL + suffix
		}
		f.session["url"] = value
		c := offlineClient(t, f.server.URL)
		_, err := c.CreateCheckout(context.Background(), createInput(f))
		requireFailure(t, err, StageHostedURL, ConstraintURL, OutcomeCreateUnknown)
		if len(f.snapshot()) != 1 {
			t.Fatal("display failure retried create")
		}
	}
}

func TestProcessEnvironmentNeverSelectsProxy(t *testing.T) {
	var proxyCalls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { proxyCalls.Add(1) }))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("ALL_PROXY", proxy.URL)
	f := newFixture(t)
	c := offlineClient(t, f.server.URL)
	_, err := c.RetrieveEvidence(context.Background(), fixtureSession, f.purchase)
	if err != nil || proxyCalls.Load() != 0 {
		t.Fatal("fixture proxy independence failed")
	}
}

func TestPanicFallbackDoesNotRetainPayload(t *testing.T) {
	var err error
	func() { defer recoverBoundary(&err, StageCharge, OutcomeReadUnavailable); panic(hostile) }()
	d := requireFailure(t, err, StageCharge, ConstraintUnknown, OutcomeReadUnavailable)
	if !d.CauseUnknown || d.Code != CodeUnexpected {
		t.Fatal("missing cause-unknown fallback")
	}
}

// Assert the test harness itself has no dependency on real environment secrets.
func TestFixturePlaceholdersAreNotEnvironmentDerived(t *testing.T) {
	t.Setenv("STRIPE_API_KEY", hostile)
	f := newFixture(t)
	makeCreateFixture(f)
	c := offlineClient(t, f.server.URL)
	_, err := c.CreateCheckout(context.Background(), createInput(f))
	if err != nil {
		t.Fatal(err)
	}
	if f.snapshot()[0].header.Get("Authorization") != "Bearer "+FixtureAPIKey || os.Getenv("STRIPE_API_KEY") != hostile {
		t.Fatal("fixture credential boundary changed")
	}
	b, _ := json.Marshal(f.purchase)
	if strings.Contains(string(b), FixtureAPIKey) {
		t.Fatal("credential in purchase")
	}
}
