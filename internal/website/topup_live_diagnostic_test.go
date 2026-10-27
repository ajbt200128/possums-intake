package website

import (
	"net/http"
	"strings"
	"testing"

	"possums-intake/internal/stripepay"
)

func TestTopupHTTPPreservesLiveProviderConstraints(t *testing.T) {
	for _, tc := range []struct {
		name       string
		constraint stripepay.Constraint
		stage      stripepay.Stage
		outcome    stripepay.Outcome
		wantStage  string
	}{
		{"context", stripepay.ConstraintLiveContext, stripepay.StageCreate, stripepay.OutcomeCreateUnknown, "payment_checkout_create"},
		{"url", stripepay.ConstraintLiveURL, stripepay.StageHostedURL, stripepay.OutcomeCreateUnknown, "payment_checkout_presentation"},
		{"tax", stripepay.ConstraintTax, stripepay.StageCreate, stripepay.OutcomeCreateUnknown, "payment_checkout_create"},
		{"tax-complete", stripepay.ConstraintTaxComplete, stripepay.StageSession, stripepay.OutcomeReadUnavailable, "payment_session_read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAccountFixture(t)
			p := &topupFixture{result: TopupResult{Reference: strings.Repeat("a", 64)}, err: &stripepay.Error{
				Code: stripepay.CodeEvidence, Stage: tc.stage, Constraint: tc.constraint,
				Outcome: tc.outcome, NextStep: "PRIVATE_UPSTREAM_TEXT", HTTPStatus: 503,
			}}
			f.host.payments = p
			if accountLogin(t, f.browser, keyHTTPA) != http.StatusSeeOther {
				t.Fatal("SITE_TEST_LIVE_DIAGNOSTIC_LOGIN")
			}
			form := paymentForm(t, f.browser)
			response, body := f.request(t, "POST", "/topup/start", form, f.server.URL)
			if response.StatusCode != http.StatusServiceUnavailable ||
				response.Header.Get("Content-Type") != "text/plain; charset=utf-8" ||
				!strings.Contains(body, "PAYMENT_PROVIDER_EVIDENCE_REJECTED") ||
				!strings.Contains(body, "constraint="+string(tc.constraint)) ||
				!strings.Contains(body, "stage="+tc.wantStage) ||
				!strings.Contains(body, "outcome="+string(tc.outcome)) ||
				!strings.Contains(body, "Observed provider HTTP status: 503.") ||
				strings.Contains(body, "PRIVATE_") || strings.Contains(body, p.result.Reference) ||
				p.starts != 1 || p.reads != 0 {
				t.Fatal("SITE_TEST_LIVE_DIAGNOSTIC_CONSTRAINT_PRIVACY_OR_OWNERSHIP")
			}
		})
	}
}

func TestTopupHTTPUnknownProviderFieldsStayClosed(t *testing.T) {
	f := newAccountFixture(t)
	p := &topupFixture{result: TopupResult{Reference: strings.Repeat("a", 64)}, err: &stripepay.Error{
		Code: "PRIVATE_CODE", Stage: "PRIVATE_STAGE", Constraint: "PRIVATE_CONSTRAINT",
		Outcome: "PRIVATE_OUTCOME", NextStep: "PRIVATE_NEXT", HTTPStatus: 999,
	}}
	f.host.payments = p
	if accountLogin(t, f.browser, keyHTTPA) != http.StatusSeeOther {
		t.Fatal("SITE_TEST_UNKNOWN_DIAGNOSTIC_LOGIN")
	}
	form := paymentForm(t, f.browser)
	response, body := f.request(t, "POST", "/topup/start", form, f.server.URL)
	if response.StatusCode != http.StatusServiceUnavailable ||
		!strings.Contains(body, "PAYMENT_PROVIDER_CAUSE_UNKNOWN") ||
		!strings.Contains(body, "stage=payment_start") ||
		!strings.Contains(body, "constraint=component_cause_unknown") ||
		!strings.Contains(body, "outcome=unknown") || strings.Contains(body, "PRIVATE_") ||
		strings.Contains(body, p.result.Reference) || strings.Contains(body, "Observed provider HTTP status:") ||
		p.starts != 1 || p.reads != 0 {
		t.Fatal("SITE_TEST_UNKNOWN_DIAGNOSTIC_PRIVACY_OR_OWNERSHIP")
	}
}
