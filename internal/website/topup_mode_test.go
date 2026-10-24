package website

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"possums-intake/internal/payments"
	"possums-intake/internal/stripepay"
)

const sandboxNotice = "Sandbox/test only. Use synthetic payment details; do not make real payments."

var liveNotices = []string{
	"Payments are live. The selected USD amount becomes inference credit; tax is added at Stripe Checkout and does not become credit.",
	"Stripe processes payment and tax details and keeps its own records. Card and tax-address details are not stored in our local sales records.",
	"Local purchase links and sales records share a deletion cutoff 20 days after checkout starts. Maintenance delays and storage remnants can retain data longer; this does not delete Stripe records.",
}

// A public host fake, not an SDK endpoint/transport override.
type modeTopupFixture struct {
	*topupFixture
	live, panicMode bool
}

func (f *modeTopupFixture) IsLive() bool {
	if f.panicMode {
		panic("PRIVATE_MODE_PANIC")
	}
	return f.live
}

func assertPaymentNotice(t *testing.T, body string, live bool) {
	t.Helper()
	if strings.Contains(body, sandboxNotice) == live {
		t.Fatal("SITE_TEST_PAYMENT_SANDBOX_NOTICE_MODE")
	}
	for _, notice := range liveNotices {
		if strings.Contains(body, notice) != live {
			t.Fatal("SITE_TEST_PAYMENT_LIVE_NOTICE_MODE")
		}
	}
	if live && strings.Contains(strings.ToLower(body), "sandbox") {
		t.Fatal("SITE_TEST_PAYMENT_LIVE_SANDBOX_CLAIM")
	}
}

func TestTopupModeClosedAndLegacyDefault(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actions TopupActions
		want    string
	}{
		{"nil", nil, "test"},
		{"legacy", &topupFixture{}, "test"},
		{"test", &modeTopupFixture{topupFixture: &topupFixture{}}, "test"},
		{"live", &modeTopupFixture{topupFixture: &topupFixture{}, live: true}, "live"},
		{"nil-service", (*payments.Service)(nil), "test"},
		{"zero-service", new(payments.Service), "test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if topupMode(tc.actions) != tc.want {
				t.Fatal("SITE_TEST_PAYMENT_MODE_CLOSED")
			}
		})
	}
}

func TestTopupBundledModeStatesAndInertBindings(t *testing.T) {
	r := prepared(t)
	for _, mode := range []string{"", "test", "live", "PRIVATE_HOSTILE_MODE"} {
		for state, phrase := range map[string]string{
			"form":                     "Choose one fixed credit amount.",
			"funded":                   "Credit was added once.",
			"awaiting_payment":         "payment and account funding are not confirmed.",
			"unknown":                  "Payment creation or funding is unconfirmed. No creation attempt was replayed.",
			"support_required":         "this is not confirmation of a refund.",
			"unavailable_after_unlink": "This does not establish whether a charge or credit occurred.",
		} {
			page, err := r.PageWithView(context.Background(), "topup-page", map[string]string{
				"payment-mode": mode, "payment-state": state, "balance-state": "available",
				"balance-usd": "$3.00", "available-usd": "$2.00",
			})
			if err != nil {
				t.Fatal("SITE_TEST_PAYMENT_MODE_PAGE")
			}
			bindings := hostBindings{paymentsEnabled: true, formEnabled: state == "form"}
			if bindings.formEnabled {
				bindings.attempt = strings.Repeat("a", 64)
			}
			body, err := r.render(page, "csrf", "", bindings)
			if err != nil || !strings.Contains(string(body), phrase) {
				t.Fatal("SITE_TEST_PAYMENT_MODE_RENDER")
			}
			text := string(body)
			assertPaymentNotice(t, text, mode == "live")
			if !strings.Contains(text, "A new submission may create another payment even if an earlier outcome is unknown.") {
				t.Fatal("SITE_TEST_PAYMENT_MODE_NO_REPLAY_COPY")
			}
			if state == "form" {
				for _, button := range []string{`value="usd5">$5.00`, `value="usd10">$10.00`, `value="usd25">$25.00`, `value="usd50">$50.00`} {
					if !strings.Contains(text, button) {
						t.Fatal("SITE_TEST_PAYMENT_MODE_PRINCIPAL_CHOICES")
					}
				}
			} else {
				assertTopupBalance(t, text, "$3.00", "$2.00")
			}
			// Even an explicitly live Lisp view cannot enable host capabilities.
			body, err = r.Render(page, "csrf", "")
			if err != nil || strings.Contains(string(body), `action="/topup/start"`) || strings.Contains(string(body), `href="/topup/status"`) {
				t.Fatal("SITE_TEST_PAYMENT_MODE_IS_NOT_AUTHORITY")
			}
		}
	}
	page, err := r.Page(context.Background(), "topup-page")
	if err != nil {
		t.Fatal("SITE_TEST_PAYMENT_LEGACY_EMPTY_VIEW")
	}
	body, err := r.Render(page, "csrf", "")
	if err != nil {
		t.Fatal("SITE_TEST_PAYMENT_LEGACY_RENDER")
	}
	assertPaymentNotice(t, string(body), false)
}

func TestTopupModeHTTPTrustedCapabilityAndInputRefusal(t *testing.T) {
	for _, live := range []bool{false, true} {
		t.Run(map[bool]string{false: "test", true: "live"}[live], func(t *testing.T) {
			f := newAccountFixture(t)
			p := &modeTopupFixture{topupFixture: &topupFixture{result: TopupResult{Reference: strings.Repeat("a", 64), Status: "funded"}}, live: live}
			f.host.payments = p
			if accountLogin(t, f.browser, keyHTTPA) != http.StatusSeeOther {
				t.Fatal("SITE_TEST_PAYMENT_MODE_LOGIN")
			}
			response, body := f.request(t, "GET", "/topup", nil, "")
			if response.StatusCode != http.StatusOK || p.starts != 0 || p.reads != 0 {
				t.Fatal("SITE_TEST_PAYMENT_MODE_FORM_INERT")
			}
			assertPaymentNotice(t, body, live)
			form := paymentForm(t, f.browser)
			for _, value := range []string{"live", "test", "PRIVATE_MODE"} {
				bad := cloneTopupForm(form)
				bad.Set("payment-mode", value)
				response, body = f.request(t, "POST", "/topup/start", bad, f.server.URL)
				if response.StatusCode != http.StatusBadRequest || !strings.Contains(body, "SITE_FORM_REFUSED") || p.starts != 0 {
					t.Fatal("SITE_TEST_PAYMENT_MODE_BROWSER_REFUSED")
				}
			}
			response, _ = f.request(t, "GET", "/topup?payment-mode=live", nil, "")
			if response.StatusCode != http.StatusBadRequest || p.starts != 0 || p.reads != 0 {
				t.Fatal("SITE_TEST_PAYMENT_MODE_QUERY_REFUSED")
			}
			response, body = f.request(t, "POST", "/topup/start", form, f.server.URL)
			if response.StatusCode != http.StatusOK || p.starts != 1 || !strings.Contains(body, "Credit was added once.") {
				t.Fatal("SITE_TEST_PAYMENT_MODE_FUNDED")
			}
			assertPaymentNotice(t, body, live)
			assertTopupBalance(t, body, "$0.00", "$0.00") // independent actual account snapshot
			p.result.Status = "awaiting_payment"
			for _, state := range []string{"awaiting_payment", "PRIVATE_STATE"} {
				p.result.Status = state
				response, body = f.request(t, "GET", "/topup/status", nil, "")
				if response.StatusCode != http.StatusOK || p.starts != 1 || strings.Contains(body, p.result.Reference) || strings.Contains(body, "PRIVATE_") || strings.Contains(body, keyHTTPA) {
					t.Fatal("SITE_TEST_PAYMENT_MODE_STATUS_PRIVATE")
				}
				assertPaymentNotice(t, body, live)
				assertTopupBalance(t, body, "$0.00", "$0.00")
			}
			f.host.balances = nil
			p.result.Status = "funded"
			response, body = f.request(t, "GET", "/topup/status", nil, "")
			if response.StatusCode != http.StatusOK || !strings.Contains(body, "Credit was added once.") || !strings.Contains(body, "Current balance is unavailable.") || strings.Contains(body, "$0.00") {
				t.Fatal("SITE_TEST_PAYMENT_MODE_BALANCE_UNAVAILABLE_NO_ZERO")
			}
			assertPaymentNotice(t, body, live)
			// A broken trusted accessor closes through the existing host boundary;
			// it cannot leak the panic or erase already confirmed funding evidence.
			p.result.Status, p.panicMode = "funded", true
			response, body = f.request(t, "GET", "/topup/status", nil, "")
			assertClosedHTTP(t, response, body, "SITE_RENDER_UNAVAILABLE", "original_funding_confirmed_not_availability")
			if strings.Contains(body, "PRIVATE_") || p.starts != 1 {
				t.Fatal("SITE_TEST_PAYMENT_MODE_PANIC_PRIVATE")
			}
		})
	}
}

func TestTopupModeActualLiveCoordinatorIsInert(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	if f.service.InitializeLiveStripePayments(ctx) != nil {
		t.Fatal("SITE_TEST_PAYMENT_LIVE_SCHEMA")
	}
	// Authored shape-only credentials; no SDK Create/Retrieve method is called.
	client, err := stripepay.NewLive(stripepay.LiveConfig{
		ReturnOrigin: "https://payments.example.com", APIKey: "sk_live_SyntheticOnlyNotACredential123456",
		WebhookSecret: "whsec_SyntheticOnlyNotASigningSecret123456", TaxCode: "txcd_10000000",
	})
	if err != nil {
		t.Fatal("SITE_TEST_PAYMENT_LIVE_CLIENT")
	}
	service, err := payments.NewLive(ctx, f.service, client)
	if err != nil || !service.IsLive() {
		t.Fatal("SITE_TEST_PAYMENT_LIVE_ADMISSION")
	}
	f.host.payments = service
	if accountLogin(t, f.browser, keyHTTPA) != http.StatusSeeOther {
		t.Fatal("SITE_TEST_PAYMENT_LIVE_LOGIN")
	}
	before := f.counter.calls.Load()
	for _, path := range []string{"/topup", "/topup/status", "/topup/return"} {
		response, body := f.request(t, "GET", path, nil, "")
		if response.StatusCode != http.StatusOK {
			t.Fatal("SITE_TEST_PAYMENT_LIVE_INERT_RENDER")
		}
		assertPaymentNotice(t, body, true)
		for _, private := range []string{"txcd_10000000", "sk_live_", "whsec_", keyHTTPA, accountHTTPA} {
			if strings.Contains(body, private) {
				t.Fatal("SITE_TEST_PAYMENT_LIVE_PRIVATE_CONFIGURATION")
			}
		}
	}
	if f.counter.calls.Load() != before+2 {
		t.Fatal("SITE_TEST_PAYMENT_LIVE_FORM_EXTRA_READ")
	}
	var purchases, sales int
	if f.sdk.DB().NewQuery("SELECT count(*) FROM payment_purchases").Row(&purchases) != nil ||
		f.sdk.DB().NewQuery("SELECT count(*) FROM payment_sales").Row(&sales) != nil || purchases != 0 || sales != 0 {
		t.Fatal("SITE_TEST_PAYMENT_LIVE_RENDER_MUTATED_PAYMENTS")
	}
}
