package website

import (
	"net/http"
	"strings"
	"testing"

	"possums-intake/internal/stripepay"
)

// This capability is a local spy, never a real sandbox SDK dispatch.
type sandboxTopupFixture struct {
	*topupFixture
	origin string
}

func (f *sandboxTopupFixture) CheckoutOrigin() string { return f.origin }
func (f *sandboxTopupFixture) CheckoutURLAllowed(value string) bool {
	return value == f.result.URL && strings.HasPrefix(value, "https://checkout.stripe.com/c/pay/cs_test_")
}

func TestTopupSandboxOriginAndTransientRedirect(t *testing.T) {
	f := newAccountFixture(t)
	p := &sandboxTopupFixture{
		topupFixture: &topupFixture{result: TopupResult{
			Reference: strings.Repeat("a", 64),
			URL:       "https://checkout.stripe.com/c/pay/cs_test_SyntheticOnly#SyntheticFragment",
		}},
		origin: "https://checkout.stripe.com",
	}
	f.host.payments = p
	if accountLogin(t, f.browser, keyHTTPA) != 303 {
		t.Fatal("SITE_TEST_TOPUP_LOGIN")
	}
	form := paymentForm(t, f.browser)
	response, body := f.request(t, "POST", "/topup/start", form, f.server.URL)
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != p.result.URL || response.Header.Get("Referrer-Policy") != "no-referrer" || p.starts != 1 || strings.Contains(body, p.result.Reference) {
		t.Fatal("SITE_TEST_SANDBOX_REDIRECT")
	}
	if !strings.HasSuffix(response.Header.Get("Content-Security-Policy"), "form-action 'self' https://checkout.stripe.com") {
		t.Fatal("SITE_TEST_SANDBOX_CSP")
	}
}

func TestTopupRejectsOtherExternalCheckoutOriginsBeforeAction(t *testing.T) {
	f := newAccountFixture(t)
	if accountLogin(t, f.browser, keyHTTPA) != 303 {
		t.Fatal("SITE_TEST_TOPUP_LOGIN")
	}
	for _, origin := range []string{
		"http://checkout.stripe.com", "https://checkout.stripe.com:443",
		"https://checkout.stripe.com/", "https://checkout.stripe.com?",
		"https://checkout.stripe.com#private", "https://user@checkout.stripe.com",
		"https://checkout.stripe.com.invalid", "https://api.stripe.com",
		"https://hostile.invalid", "https://CHECKOUT.STRIPE.COM",
	} {
		p := &sandboxTopupFixture{topupFixture: &topupFixture{}, origin: origin}
		f.host.payments = p
		response, _ := f.request(t, "GET", "/topup", nil, "")
		if response.StatusCode != http.StatusServiceUnavailable || p.starts != 0 || p.reads != 0 || strings.Contains(response.Header.Get("Content-Security-Policy"), origin) {
			t.Fatal("SITE_TEST_UNSAFE_SANDBOX_ORIGIN")
		}
	}
}

func TestTopupSandboxDiagnosticsPreserveClosedCategories(t *testing.T) {
	for _, e := range []*stripepay.Error{
		{Code: stripepay.CodeSandboxConfiguration, Stage: stripepay.StageSandboxConfiguration, Constraint: stripepay.ConstraintSandbox, Outcome: stripepay.OutcomeNotAttempted},
		{Code: stripepay.CodeEvidence, Stage: stripepay.StageHostedURL, Constraint: stripepay.ConstraintSandboxURL, Outcome: stripepay.OutcomeCreateUnknown},
	} {
		e.NextStep = "SYNTHETIC_PRIVATE_SENTINEL"
		d := topupProviderFailure(e, "payment_dispatch")
		if d.Constraint != string(e.Constraint) || strings.Contains(d.NextStep, "SYNTHETIC_PRIVATE_SENTINEL") {
			t.Fatal("SITE_TEST_SANDBOX_CLOSED_DIAGNOSTIC")
		}
		if e.Code == stripepay.CodeSandboxConfiguration && (d.Code != "PAYMENT_SANDBOX_CONFIGURATION_INVALID" || d.Stage != "payment_configuration" || d.Outcome != "provider_not_attempted") {
			t.Fatal("SITE_TEST_SANDBOX_CONFIGURATION_DIAGNOSTIC")
		}
	}
}
