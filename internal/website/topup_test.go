package website

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"possums-intake/internal/ledger"
	"possums-intake/internal/payments"
	"possums-intake/internal/stripepay"
)

type topupFixture struct {
	starts, reads int
	account       string
	form          ledger.PaymentForm
	result        TopupResult
	err           error
	panicStart    bool
}

func (f *topupFixture) CheckoutOrigin() string { return "http://127.0.0.1:8099" }
func (f *topupFixture) CheckoutURLAllowed(raw string) bool {
	return raw == "http://127.0.0.1:8099/c/pay/synthetic"
}

func (f *topupFixture) StartTopup(_ context.Context, account string, form ledger.PaymentForm, choice string) (TopupResult, error) {
	f.starts++
	if f.panicStart {
		panic("PRIVATE_PROVIDER_PANIC")
	}
	f.account, f.form = account, form
	return f.result, f.err
}
func (f *topupFixture) TopupStatus(_ context.Context, account, ref string) (TopupResult, error) {
	f.reads++
	f.account = account
	return f.result, f.err
}
func cloneTopupForm(form url.Values) url.Values {
	copy := make(url.Values, len(form))
	for key, values := range form {
		copy[key] = append([]string(nil), values...)
	}
	return copy
}

func paymentForm(t *testing.T, b *browser) url.Values {
	t.Helper()
	response, body := b.request(t, "GET", "/topup", nil, "")
	if response.StatusCode != 200 {
		t.Fatal("SITE_TEST_TOPUP_FORM")
	}
	for _, choice := range []string{"usd5", "usd10", "usd25", "usd50"} {
		if !strings.Contains(body, `value="`+choice+`"`) {
			t.Fatal("SITE_TEST_TOPUP_DENOMINATIONS")
		}
	}
	return url.Values{"csrf_token": {hidden(t, body, "csrf_token")}, "revision": {hidden(t, body, "revision")}, "attempt": {hidden(t, body, "attempt")}, "denomination": {"usd10"}}
}
func TestTopupHTTPAuthCSRFAndLocalStatus(t *testing.T) {
	f := newAccountFixture(t)
	p := &topupFixture{result: TopupResult{Reference: strings.Repeat("a", 64), URL: "http://127.0.0.1:8099/c/pay/synthetic", Status: "awaiting_payment"}}
	f.host.payments = p
	response, _ := f.request(t, "GET", "/topup", nil, "")
	if response.StatusCode != 303 || p.starts != 0 {
		t.Fatal("SITE_TEST_TOPUP_AUTH")
	}
	if accountLogin(t, f.browser, keyHTTPA) != 303 {
		t.Fatal("SITE_TEST_TOPUP_LOGIN")
	}
	form := paymentForm(t, f.browser)
	if p.starts != 0 || p.reads != 0 {
		t.Fatal("SITE_TEST_TOPUP_GET_EFFECT")
	}
	bad := cloneTopupForm(form)
	bad.Set("account_id", accountHTTPB)
	response, _ = f.request(t, "POST", "/topup/start", bad, f.server.URL)
	if response.StatusCode != 400 || p.starts != 0 {
		t.Fatal("SITE_TEST_TOPUP_ACCOUNT_SELECTOR")
	}
	bad = cloneTopupForm(form)
	bad.Set("csrf_token", "bad")
	response, _ = f.request(t, "POST", "/topup/start", bad, f.server.URL)
	if response.StatusCode != 403 || p.starts != 0 {
		t.Fatal("SITE_TEST_TOPUP_CSRF")
	}
	bad = cloneTopupForm(form)
	bad.Set("attempt", strings.Repeat("b", 64))
	response, _ = f.request(t, "POST", "/topup/start", bad, f.server.URL)
	if response.StatusCode != 409 || p.starts != 0 {
		t.Fatal("SITE_TEST_TOPUP_ATTEMPT")
	}
	response, body := f.request(t, "POST", "/topup/start", form, f.server.URL)
	if response.StatusCode != 303 || response.Header.Get("Location") != p.result.URL || response.Header.Get("Referrer-Policy") != "no-referrer" || p.account != accountHTTPA || p.starts != 1 || p.form.Attempt != form.Get("attempt") || strings.Contains(body, p.result.Reference) {
		t.Fatal("SITE_TEST_TOPUP_REDIRECT")
	}
	p.result.Status = "funded"
	for _, path := range []string{"/topup/status", "/topup/return"} {
		response, body = f.request(t, "GET", path, nil, "")
		if response.StatusCode != 200 || !strings.Contains(body, "Credit was added once") || strings.Contains(body, p.result.Reference) || p.starts != 1 {
			t.Fatal("SITE_TEST_TOPUP_PASSIVE_STATUS")
		}
	}
	if p.reads != 2 {
		t.Fatal("SITE_TEST_TOPUP_STATUS_READS")
	}
	response, _ = f.request(t, "GET", "/topup/status?account_id=other", nil, "")
	if response.StatusCode != 400 || p.reads != 2 {
		t.Fatal("SITE_TEST_TOPUP_QUERY_SELECTOR")
	}
}
func TestTopupClosedErrorsAndRedirectOrigin(t *testing.T) {
	f := newAccountFixture(t)
	p := &topupFixture{result: TopupResult{Reference: strings.Repeat("a", 64), URL: "https://hostile.invalid/PRIVATE_URL"}}
	f.host.payments = p
	if accountLogin(t, f.browser, keyHTTPA) != 303 {
		t.Fatal("SITE_TEST_TOPUP_LOGIN")
	}
	form := paymentForm(t, f.browser)
	response, body := f.request(t, "POST", "/topup/start", form, f.server.URL)
	if response.StatusCode != 503 || strings.Contains(body, "PRIVATE_URL") || response.Header.Get("Location") != "" {
		t.Fatal("SITE_TEST_TOPUP_BAD_REDIRECT")
	}
	p.err = errors.New("PRIVATE_PROVIDER_SECRET")
	response, body = f.request(t, "GET", "/topup/status", nil, "")
	if response.StatusCode != 503 || strings.Contains(body, "PRIVATE_PROVIDER_SECRET") || !strings.Contains(body, "PAYMENT_COMPLETION_UNCONFIRMED") {
		t.Fatal("SITE_TEST_TOPUP_ERROR_PRIVACY")
	}
}
func TestTopupDisabledAndReservedPublication(t *testing.T) {
	f := newAccountFixture(t)
	if accountLogin(t, f.browser, keyHTTPA) != 303 {
		t.Fatal("SITE_TEST_TOPUP_LOGIN")
	}
	response, _ := f.request(t, "GET", "/topup", nil, "")
	if response.StatusCode != http.StatusNotFound {
		t.Fatal("SITE_TEST_TOPUP_DEFAULT_DISABLED")
	}
	for _, path := range []string{"/topup", "/topup/start", "/payments/stripe/webhook"} {
		if !reserved(path) {
			t.Fatal("SITE_TEST_TOPUP_RESERVED")
		}
	}
}

func TestTopupBoundaryDiagnosticsRetainKnownCreation(t *testing.T) {
	f := newAccountFixture(t)
	p := &topupFixture{result: TopupResult{Reference: strings.Repeat("a", 64)}}
	f.host.payments = p
	if accountLogin(t, f.browser, keyHTTPA) != 303 {
		t.Fatal("SITE_TEST_TOPUP_LOGIN")
	}
	form := paymentForm(t, f.browser)
	p.err = &payments.Error{Code: "PAYMENT_COMPONENT_CAUSE_UNKNOWN", Stage: "PRIVATE_STAGE", Constraint: "PRIVATE_CONSTRAINT", Outcome: "checkout_created_local_binding_unconfirmed", NextStep: "PRIVATE_TEXT"}
	response, body := f.request(t, "POST", "/topup/start", form, f.server.URL)
	if response.StatusCode != 503 || !strings.Contains(body, "checkout_created_local_binding_unconfirmed") || !strings.Contains(body, "Checkout was created") || strings.Contains(body, "PRIVATE_") {
		t.Fatal("SITE_TEST_TOPUP_CREATION_KNOWN")
	}
	p.err = &stripepay.Error{Code: stripepay.CodeProvider, Stage: stripepay.StageCreate, Constraint: stripepay.ConstraintStatus, Outcome: stripepay.OutcomeCreateUnknown, HTTPStatus: 503}
	response, body = f.request(t, "GET", "/topup/status", nil, "")
	if response.StatusCode != 503 || !strings.Contains(body, "creation_unknown_not_replayed") || !strings.Contains(body, "Observed provider HTTP status: 503") {
		t.Fatal("SITE_TEST_TOPUP_PROVIDER_DIAGNOSTIC")
	}
	p.err = nil
	p.panicStart = true
	response, body = f.request(t, "POST", "/topup/start", form, f.server.URL)
	if response.StatusCode != 503 || !strings.Contains(body, "payment_start") || !strings.Contains(body, "PAYMENT_COMPLETION_UNCONFIRMED") || strings.Contains(body, "PRIVATE_") {
		t.Fatal("SITE_TEST_TOPUP_PANIC_BOUNDARY")
	}
}

func TestTopupCreatedOutcomeSurvivesSessionCommitFailure(t *testing.T) {
	for _, panicValue := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "panic"}[panicValue], func(t *testing.T) {
			f := newAccountFixture(t)
			p := &topupFixture{result: TopupResult{Reference: strings.Repeat("a", 64)}, err: &payments.Error{Code: "PAYMENT_COMPONENT_CAUSE_UNKNOWN", Outcome: "checkout_created_local_binding_unconfirmed"}}
			f.host.payments = p
			if accountLogin(t, f.browser, keyHTTPA) != 303 {
				t.Fatal("SITE_TEST_TOPUP_LOGIN")
			}
			form := paymentForm(t, f.browser)
			f.host.sessions.Store = faultyStore{Store: f.host.store, operation: "commit", panicValue: panicValue}
			response, body := f.request(t, "POST", "/topup/start", form, f.server.URL)
			assertClosedHTTP(t, response, body, "SITE_SESSION_UNAVAILABLE", "checkout_created_local_binding_unconfirmed")
			if p.starts != 1 || strings.Contains(body, "payment_creation_unconfirmed") || strings.Contains(body, p.result.Reference) {
				t.Fatal("SITE_TEST_TOPUP_SESSION_LOST_CREATION_OUTCOME")
			}
		})
	}
}

func TestTopupOfflineBrowserOrigin(t *testing.T) {
	for _, origin := range []string{"http://[::1]:8099", "https://checkout.stripe.com", "http://localhost:8099", "http://127.0.0.1:8099/", "http://127.0.0.1:8099?", "http://127.0.0.1:8099#private", "http://private@127.0.0.1:8099"} {
		if topupOfflineOrigin(origin) {
			t.Fatal("SITE_TEST_TOPUP_UNSUPPORTED_ORIGIN")
		}
	}
	if !topupOfflineOrigin("http://127.0.0.1:8099") {
		t.Fatal("SITE_TEST_TOPUP_SUPPORTED_ORIGIN")
	}
}
