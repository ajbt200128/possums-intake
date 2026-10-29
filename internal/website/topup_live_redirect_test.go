package website

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"possums-intake/internal/ledger"
	"possums-intake/internal/stripepay"
)

// Only the action DTO is stubbed. The actual NewLive constructor owns the URL
// guard; no network-capable SDK method is invoked. The host still authenticates
// through PocketBase, checks the session/CSRF/form and serves an actual TLS POST.
type liveRedirectFixture struct {
	client        *stripepay.Client
	value         string
	starts, reads int
	account       string
}

func (p *liveRedirectFixture) IsLive() bool           { return p.client.IsLive() }
func (p *liveRedirectFixture) CheckoutOrigin() string { return p.client.CheckoutOrigin() }
func (p *liveRedirectFixture) CheckoutURLAllowed(value string) bool {
	return p.client.CheckoutURLAllowed(value)
}
func (p *liveRedirectFixture) StartTopup(_ context.Context, account string, _ ledger.PaymentForm, _ string) (TopupResult, error) {
	p.starts++
	p.account = account
	return TopupResult{Reference: strings.Repeat("a", 64), URL: p.value, Status: "awaiting_payment"}, nil
}
func (p *liveRedirectFixture) TopupStatus(_ context.Context, account, _ string) (TopupResult, error) {
	p.reads++
	p.account = account
	return TopupResult{Status: "awaiting_payment"}, nil
}

func newLiveRedirectFixture(t *testing.T) *liveRedirectFixture {
	t.Helper()
	client, err := stripepay.NewLive(stripepay.LiveConfig{
		ReturnOrigin:  "https://payments.example.com",
		APIKey:        "sk_live_SyntheticOnlyNotACredential123456",
		WebhookSecret: "whsec_SyntheticOnlyNotASigningSecret123456",
		TaxCode:       "txcd_10000000",
	})
	if err != nil || !client.IsLive() {
		t.Fatal("SITE_TEST_LIVE_REDIRECT_CONFIGURATION")
	}
	return &liveRedirectFixture{client: client}
}

func TestTopupLiveHTTPRedirectEncodingBoundary(t *testing.T) {
	const origin = "https://checkout.stripe.com"
	const opaque = "/%73ynthetic/%2froute/"
	const query = "?opaque=SyntheticOpaqueQuery%2f%00%ff&unicode="
	const fragment = "#SyntheticOpaqueFragment%41%2f-"
	long := strings.Repeat("A", 2049)
	for _, tc := range []struct{ name, value, location string }{
		{"ascii", origin + opaque + "ascii" + query + "ascii" + fragment + long,
			origin + opaque + "ascii" + query + "ascii" + fragment + long},
		// Pinned net/http.Redirect escapes raw UTF-8 bytes using lowercase hex;
		// the unrelated existing percent-encoded bytes must remain untouched.
		{"unicode", origin + opaque + "é" + query + "é" + fragment + "é" + long,
			origin + opaque + "%c3%a9" + query + "%c3%a9" + fragment + "%c3%a9" + long},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAccountFixture(t)
			p := newLiveRedirectFixture(t)
			p.value = tc.value
			f.host.payments = p
			if !p.CheckoutURLAllowed(p.value) {
				t.Fatal("SITE_TEST_LIVE_REDIRECT_INPUT_REFUSED")
			}
			captured := captureWebsiteOutput(t, func() {
				if accountLogin(t, f.browser, keyHTTPA) != http.StatusSeeOther {
					t.Fatal("SITE_TEST_LIVE_REDIRECT_LOGIN")
				}
				form := paymentForm(t, f.browser)
				response, body := f.request(t, "POST", "/topup/start", form, f.server.URL)
				if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != tc.location || body != "" || p.starts != 1 || p.reads != 0 || p.account != accountHTTPA {
					t.Fatal("SITE_TEST_LIVE_REDIRECT_WIRE_ENCODING")
				}
				for name, values := range response.Header {
					if name != "Location" {
						for _, value := range values {
							assertLiveRedirectPrivate(t, value, p.value, tc.location)
						}
					}
				}
				if response.Header.Get("Referrer-Policy") != "no-referrer" || !strings.HasSuffix(response.Header.Get("Content-Security-Policy"), "form-action 'self' "+origin) {
					t.Fatal("SITE_TEST_LIVE_REDIRECT_PRESENTATION_POLICY")
				}
				before, err := url.Parse(p.value)
				if err != nil {
					t.Fatal("SITE_TEST_LIVE_REDIRECT_SOURCE_PARSE")
				}
				after, err := url.Parse(response.Header.Get("Location"))
				if err != nil || after.Scheme != "https" || after.Host != "checkout.stripe.com" || after.User != nil || after.Opaque != "" || after.Path != before.Path || after.Fragment != before.Fragment {
					t.Fatal("SITE_TEST_LIVE_REDIRECT_ORIGIN_OR_COMPONENTS")
				}
				beforeQuery, beforeErr := url.QueryUnescape(before.RawQuery)
				afterQuery, afterErr := url.QueryUnescape(after.RawQuery)
				if beforeErr != nil || afterErr != nil || beforeQuery != afterQuery || !p.CheckoutURLAllowed(response.Header.Get("Location")) {
					t.Fatal("SITE_TEST_LIVE_REDIRECT_QUERY_SEMANTICS")
				}
				for _, path := range []string{"/topup/status", "/topup/return"} {
					response, body = f.request(t, "GET", path, nil, "")
					if response.StatusCode != http.StatusOK || response.Header.Get("Location") != "" || !strings.Contains(body, "funding are not confirmed") {
						t.Fatal("SITE_TEST_LIVE_REDIRECT_PASSIVE_STATUS")
					}
					assertLiveRedirectPrivate(t, body, p.value, tc.location)
				}
				assertLiveRedirectSessionAndLogs(t, f, p.value, tc.location)
				balance, err := f.service.Balance(context.Background(), accountHTTPA)
				if err != nil || balance.AccountBalance.Balance != "0" {
					t.Fatal("SITE_TEST_LIVE_REDIRECT_IS_NOT_FUNDING")
				}
			})
			if captured != "" || p.starts != 1 || p.reads != 2 || len(f.observer.counts()) != 0 {
				t.Fatal("SITE_TEST_LIVE_REDIRECT_OUTPUT_OR_ACTION_COUNT")
			}
		})
	}
}

func assertLiveRedirectPrivate(t *testing.T, value string, private ...string) {
	t.Helper()
	for _, forbidden := range append(private, "SyntheticOpaqueQuery", "SyntheticOpaqueFragment") {
		if strings.Contains(value, forbidden) {
			t.Fatal("SITE_TEST_LIVE_REDIRECT_PRIVATE_VALUE_RETAINED")
		}
	}
}

func assertLiveRedirectSessionAndLogs(t *testing.T, f *accountFixture, private ...string) {
	t.Helper()
	sessions, err := f.host.store.All()
	if err != nil || len(sessions) != 1 {
		t.Fatal("SITE_TEST_LIVE_REDIRECT_SESSION_STORE")
	}
	for token, data := range sessions {
		assertLiveRedirectPrivate(t, token, private...)
		assertLiveRedirectPrivate(t, string(data), private...)
	}
	var count int
	if f.sdk.AuxDB().NewQuery("SELECT count(*) FROM _logs").Row(&count) != nil || count != 0 {
		t.Fatal("SITE_TEST_LIVE_REDIRECT_LOG_RECORDS")
	}
	if f.sdk.DB().NewQuery("SELECT count(*) FROM _authOrigins").Row(&count) != nil || count != 0 {
		t.Fatal("SITE_TEST_LIVE_REDIRECT_AUTH_ORIGIN_RECORDS")
	}
}

func TestTopupLiveHTTPRejectsUnsafeURLBeforeLocation(t *testing.T) {
	f := newAccountFixture(t)
	p := newLiveRedirectFixture(t)
	f.host.payments = p
	captured := captureWebsiteOutput(t, func() {
		if accountLogin(t, f.browser, keyHTTPA) != http.StatusSeeOther {
			t.Fatal("SITE_TEST_LIVE_REDIRECT_LOGIN")
		}
		form := paymentForm(t, f.browser)
		for name, value := range map[string]string{
			"raw_control":    "https://checkout.stripe.com/synthetic?opaque=SyntheticOpaqueQuery\x00",
			"raw_newline":    "https://checkout.stripe.com/synthetic#SyntheticOpaqueFragment\n",
			"raw_space":      "https://checkout.stripe.com/synthetic?opaque=SyntheticOpaqueQuery ",
			"raw_del":        "https://checkout.stripe.com/synthetic#SyntheticOpaqueFragment\x7f",
			"backslash":      "https://checkout.stripe.com/synthetic\\SyntheticOpaqueQuery",
			"http":           "http://checkout.stripe.com/synthetic?opaque=SyntheticOpaqueQuery",
			"port":           "https://checkout.stripe.com:443/synthetic?opaque=SyntheticOpaqueQuery",
			"userinfo":       "https://synthetic@checkout.stripe.com/synthetic?opaque=SyntheticOpaqueQuery",
			"subdomain":      "https://sub.checkout.stripe.com/synthetic?opaque=SyntheticOpaqueQuery",
			"other_origin":   "https://checkout.stripe.com.invalid/synthetic?opaque=SyntheticOpaqueQuery",
			"uppercase_host": "https://CHECKOUT.STRIPE.COM/synthetic?opaque=SyntheticOpaqueQuery",
		} {
			t.Run(name, func(t *testing.T) {
				p.value = value
				before := p.starts
				response, body := f.request(t, "POST", "/topup/start", form, f.server.URL)
				if response.StatusCode != http.StatusServiceUnavailable || response.Header.Get("Location") != "" || !strings.Contains(body, "PAYMENT_COMPLETION_UNCONFIRMED") || !strings.Contains(body, "stage=payment_redirect") || p.starts != before+1 {
					t.Fatal("SITE_TEST_LIVE_REDIRECT_UNSAFE_LOCATION")
				}
				assertLiveRedirectPrivate(t, body, value)
				assertLiveRedirectSessionAndLogs(t, f, value)
			})
		}
	})
	if captured != "" || p.reads != 0 || len(f.observer.counts()) != 0 {
		t.Fatal("SITE_TEST_LIVE_REDIRECT_REFUSAL_OUTPUT_OR_ACTION")
	}
}
