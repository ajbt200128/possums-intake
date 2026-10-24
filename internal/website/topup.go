package website

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/justinas/nosurf"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
	"possums-intake/internal/payments"
	"possums-intake/internal/siteauth"
	"possums-intake/internal/stripepay"
)

// TopupActions is a trusted host capability, never supplied by Lisp or a browser.
// References stay in the memory-only authenticated session, not URLs or pages.
// Status must be observational and must not call a payment mutation.
type TopupActions interface {
	StartTopup(context.Context, string, ledger.PaymentForm, string) (TopupResult, error)
	TopupStatus(context.Context, string, string) (TopupResult, error)
	CheckoutURLAllowed(string) bool // fixed trusted provider origin, never browser-selected
	CheckoutOrigin() string
}

type TopupResult = payments.TopupResult

var _ TopupActions = (*payments.Service)(nil)

// Mode is presentation-only trusted Go configuration, never a browser/Lisp
// input or funding receipt. Legacy capabilities retain the test-only notice.
func topupMode(actions TopupActions) string {
	if mode, ok := actions.(interface{ IsLive() bool }); ok && mode.IsLive() {
		return "live"
	}
	return "test"
}

func topupPath(path string) bool {
	switch path {
	case "/topup", "/topup/start", "/topup/status", "/topup/return":
		return true
	}
	return false
}

func fixedTopupRoute(path, method string) (Route, bool) {
	if !topupPath(path) {
		return Route{}, false
	}
	want := "GET"
	if path == "/topup/start" {
		want = "POST"
	}
	return Route{Path: path, Method: want, Policy: "session"}, method == want
}

func (h *Host) topup(w http.ResponseWriter, r *http.Request) {
	stage := "payment_dispatch"
	defer func() {
		if recover() != nil {
			h.topupFailure(w, r, nil, stage)
		}
	}()
	w.Header().Set("Referrer-Policy", "no-referrer")
	if h.payments == nil {
		h.plain(w, http.StatusNotFound, httpFault("SITE_PAGE_MISSING"))
		return
	}
	origin := h.payments.CheckoutOrigin()
	if origin != "https://checkout.stripe.com" && !topupOfflineOrigin(origin) {
		h.topupFailure(w, r, nil, "payment_configuration")
		return
	}
	// CSP is enforced from the originating form document, not just the 303.
	// Permit exactly the trusted fixture or Checkout origin, never a form value.
	w.Header().Set("Content-Security-Policy", strings.Replace(w.Header().Get("Content-Security-Policy"), "form-action 'self'", "form-action 'self' "+origin, 1))
	principal, ok := r.Context().Value(principalKey{}).(siteauth.Principal)
	if !ok || principal.AccountID == "" {
		h.plain(w, http.StatusForbidden, httpFault("SITE_AUTH_REQUIRED"))
		return
	}
	if r.Method == "POST" {
		allowed := map[string]bool{"csrf_token": true, "revision": true, "attempt": true, "denomination": true}
		for key, values := range r.PostForm {
			if !allowed[key] || len(values) != 1 {
				h.plain(w, 400, httpFault("SITE_FORM_REFUSED"))
				return
			}
		}
		attempt := h.sessions.GetString(r.Context(), "payment_form")
		issued := h.sessions.GetInt64(r.Context(), "payment_form_issued")
		expires := h.sessions.GetInt64(r.Context(), "payment_form_expires")
		now := time.Now().UnixMilli()
		if attempt == "" || r.PostForm.Get("attempt") != attempt || issued > now || now >= expires {
			h.plain(w, 409, httpFault("SITE_FORM_REFUSED"))
			return
		}
		choice := r.PostForm.Get("denomination")
		if _, ok := ledger.PaymentDenomination(choice); !ok {
			h.plain(w, 400, httpFault("SITE_FORM_REFUSED"))
			return
		}
		setOutcome(r, "payment_creation_unconfirmed")
		stage = "payment_start"
		result, err := h.payments.StartTopup(r.Context(), principal.AccountID, ledger.PaymentForm{Attempt: attempt, IssuedAt: ledger.Millis(issued), ExpiresAt: ledger.Millis(expires)}, choice)
		if result.Reference != "" {
			h.sessions.Put(r.Context(), "payment_reference", result.Reference)
		}
		if err != nil {
			h.topupFailure(w, r, err, "payment_start")
			return
		}
		if result.URL != "" {
			if !h.payments.CheckoutURLAllowed(result.URL) {
				h.topupFailure(w, r, nil, "payment_redirect")
				return
			}
			setOutcome(r, "checkout_created_not_payment_confirmation")
			http.Redirect(w, r, result.URL, http.StatusSeeOther)
			return
		}
		if result.Status == "funded" {
			setOutcome(r, "original_funding_confirmed_not_availability")
		}
		stage = "payment_page"
		h.topupRender(w, r, false, paymentState(result.Status), "")
		return
	}
	if r.URL.Path == "/topup" {
		stage = "payment_form"
		if r.Method != "GET" {
			h.topupRender(w, r, false, "form", "")
			return
		}
		var token [32]byte
		if _, err := rand.Read(token[:]); err != nil {
			h.topupFailure(w, r, nil, "payment_form")
			return
		}
		attempt := hex.EncodeToString(token[:])
		now := time.Now()
		expires := min(now.Add(10*time.Minute).UnixMilli(), h.sessions.Deadline(r.Context()).UnixMilli())
		if expires <= now.UnixMilli() {
			h.plain(w, 401, httpFault("SITE_AUTH_REQUIRED"))
			return
		}
		h.sessions.Put(r.Context(), "payment_form", attempt)
		h.sessions.Put(r.Context(), "payment_form_issued", now.UnixMilli())
		h.sessions.Put(r.Context(), "payment_form_expires", expires)
		h.topupRender(w, r, true, "form", attempt)
		return
	}
	ref := h.sessions.GetString(r.Context(), "payment_reference")
	if ref == "" {
		h.topupRender(w, r, false, "no-reference", "")
		return
	}
	stage = "payment_status"
	result, err := h.payments.TopupStatus(r.Context(), principal.AccountID, ref)
	if err != nil {
		h.topupFailure(w, r, err, "payment_status")
		return
	}
	if result.Status == "funded" {
		setOutcome(r, "original_funding_confirmed_not_availability")
	}
	stage = "payment_page"
	h.topupRender(w, r, false, paymentState(result.Status), "")
}

func topupOfflineOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.Opaque != "" || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Port() == "" {
		return false
	}
	ip, err := netip.ParseAddr(u.Hostname())
	// Chromium does not accept an IPv6 literal as a CSP form-action source.
	// Refuse that fixture configuration before a payment can be created.
	return err == nil && ip.Is4() && ip.IsLoopback() && ip.Zone() == "" && u.String() == origin
}

// Only closed presentation states enter Lisp; never provider fields or objects.
func paymentState(status string) string {
	switch status {
	case "funded", "awaiting_payment", "support_required", "unavailable_after_unlink":
		return status
	default:
		return "unknown"
	}
}

func (h *Host) topupFailure(w http.ResponseWriter, r *http.Request, err error, stage string) {
	d := ledger.CanonicalFailure(err, failuretype.NativeWriteUnknown)
	var provider *stripepay.Error
	if errors.As(err, &provider) && provider != nil {
		d = topupProviderFailure(provider, stage)
	}
	if d == nil || d.Code == ledger.CodeBoundary {
		d = fault("PAYMENT_COMPLETION_UNCONFIRMED", stage, "completion_evidence_unavailable", "Underlying cause or payment outcome may be unknown. Check local status; do not create another payment to diagnose this attempt.", failuretype.NativeWriteUnknown)
		d.Outcome = "unknown"
	}
	var boundary *payments.Error
	if errors.As(err, &boundary) && boundary != nil {
		if boundary.Outcome == "checkout_created_local_binding_unconfirmed" {
			d.Outcome = "checkout_created_local_binding_unconfirmed"
			d.NextStep = "Checkout was created but its local binding is unconfirmed. Check local status; do not replay creation. A new purchase may duplicate payment."
		} else {
			switch boundary.Code {
			case "PAYMENT_OFFLINE_CONFIGURATION_INVALID", "PAYMENT_SANDBOX_CONFIGURATION_INVALID", "PAYMENT_PURGE_UNAVAILABLE", "PAYMENT_TERMS_INVALID", "PAYMENT_COMPONENT_CAUSE_UNKNOWN":
				d = fault(boundary.Code, stage, "component_cause_unknown", "Local payment processing is unavailable; the underlying cause or outcome may be unknown. Check local status without replaying creation.", failuretype.NativeWriteUnknown)
				d.Outcome = "unknown"
			}
		}
	}
	if stage == "payment_page" {
		// Presentation failure is separate from payment completion. Use the
		// existing closed projection without observing payment activity.
		d = renderHTTPFault(err)
		d.Outcome = "unknown"
		if state := responseFor(r); state != nil && state.outcome == "payment_creation_unconfirmed" {
			d.Outcome = state.outcome
		}
	}
	if state := responseFor(r); state != nil {
		switch state.outcome {
		case "original_funding_confirmed_not_availability", "checkout_created_not_payment_confirmation", "checkout_created_local_binding_unconfirmed":
			d.Outcome = state.outcome
		}
	}
	// A later session commit or delivery failure must retain known creation.
	setOutcome(r, d.Outcome)
	// No payment error/activity metric family is authorized. Preserve closed
	// diagnostics without sending them through generic website error observers.
	h.plain(w, http.StatusServiceUnavailable, d)
}

// Select locally authored text from closed SDK adapter categories, never its
// public string fields or a raw upstream error. Known boundaries remain useful.
func topupProviderFailure(e *stripepay.Error, fallback string) *ledger.Diagnostic {
	code, constraint := "PAYMENT_PROVIDER_CAUSE_UNKNOWN", "component_cause_unknown"
	switch e.Code {
	case stripepay.CodeConfiguration:
		code, constraint = "PAYMENT_OFFLINE_CONFIGURATION_INVALID", "loopback_synthetic_fixture_required"
	case stripepay.CodeSandboxConfiguration:
		code, constraint = "PAYMENT_SANDBOX_CONFIGURATION_INVALID", "fixed_sandbox_and_private_test_credentials_required"
	case stripepay.CodeInput:
		code, constraint = "PAYMENT_PROVIDER_INPUT_INVALID", "original_purchase_required"
	case stripepay.CodeProvider:
		code, constraint = "PAYMENT_PROVIDER_UNAVAILABLE", "provider_response_unavailable"
	case stripepay.CodeDecode:
		code, constraint = "PAYMENT_PROVIDER_DECODE_UNKNOWN", "sdk_decoding_cause_unknown"
	case stripepay.CodeEvidence:
		code, constraint = "PAYMENT_PROVIDER_EVIDENCE_REJECTED", "canonical_original_payment_required"
	case stripepay.CodeWebhook:
		code, constraint = "PAYMENT_WEBHOOK_REJECTED", "verified_supported_notification_required"
	}
	switch e.Constraint {
	case stripepay.ConstraintSandbox, stripepay.ConstraintSandboxURL,
		stripepay.ConstraintOffline, stripepay.ConstraintInput, stripepay.ConstraintTransport,
		stripepay.ConstraintResponseSize, stripepay.ConstraintStatus, stripepay.ConstraintRedirect,
		stripepay.ConstraintDecode, stripepay.ConstraintContext, stripepay.ConstraintSession,
		stripepay.ConstraintLineItem, stripepay.ConstraintPaid, stripepay.ConstraintIntent,
		stripepay.ConstraintCharge, stripepay.ConstraintRefund, stripepay.ConstraintRefundUnavailable, stripepay.ConstraintURL,
		stripepay.ConstraintSignature, stripepay.ConstraintWebhookSize, stripepay.ConstraintEvent,
		stripepay.ConstraintUnknown:
		constraint = string(e.Constraint)
	}
	stage := fallback
	switch e.Stage {
	case stripepay.StageConfiguration, stripepay.StageSandboxConfiguration:
		stage = "payment_configuration"
	case stripepay.StageCreate:
		stage = "payment_checkout_create"
	case stripepay.StageHostedURL:
		stage = "payment_checkout_presentation"
	case stripepay.StageSession:
		stage = "payment_session_read"
	case stripepay.StageIntent:
		stage = "payment_intent_read"
	case stripepay.StageCharge:
		stage = "payment_charge_read"
	case stripepay.StageWebhook:
		stage = "payment_webhook_verify"
	}
	d := fault(code, stage, constraint, "The operation is unconfirmed. Underlying cause may be unknown; inspect the original local status without replaying creation. A new purchase may create another payment.", failuretype.NativeWriteUnknown)
	d.Outcome = "unknown"
	switch e.Outcome {
	case stripepay.OutcomeNotAttempted:
		d.Outcome = "provider_not_attempted"
	case stripepay.OutcomeCreateUnknown:
		d.Outcome = "creation_unknown_not_replayed"
	case stripepay.OutcomeReadUnavailable:
		d.Outcome = "funding_evidence_unavailable"
	case stripepay.OutcomeWebhookRejected:
		d.Outcome = "notification_rejected_no_funding"
	}
	if e.HTTPStatus >= 100 && e.HTTPStatus <= 599 {
		d.NextStep += " Observed provider HTTP status: " + strconv.Itoa(e.HTTPStatus) + "."
	}
	return d
}

// The original payment receipt and this current account read are independent.
// Never replace a known funding outcome or recount accountSnapshot's failures.
func (h *Host) topupBalanceView(r *http.Request) map[string]string {
	principal, ok := r.Context().Value(principalKey{}).(siteauth.Principal)
	if !ok || principal.AccountID == "" {
		h.report(r, accountScopeFault())
		return nil
	}
	if h.balances == nil {
		h.report(r, accountReadDiagnostic(nil))
		return nil
	}
	snapshot, d := h.accountSnapshot(r.Context(), principal.AccountID)
	if d != nil {
		return nil // the read boundary already owns this classification
	}
	view, err := snapshotBalanceView(principal.AccountID, snapshot)
	if err != nil {
		h.report(r, renderHTTPFault(err))
		return nil
	}
	view["balance-state"] = "available"
	return view
}

func (h *Host) topupRender(w http.ResponseWriter, r *http.Request, form bool, state, attempt string) {
	// A slot in any other page, return, status or POST response cannot grant a
	// purchase form. The active revision must supply this named page; no fallback
	// bypasses publication with an unrevisioned built-in document.
	if form && (r.Method != "GET" || r.URL.Path != "/topup" || state != "form") {
		h.topupFailure(w, r, documentRenderFault(), "payment_page")
		return
	}
	view := map[string]string{"payment-state": state, "payment-mode": topupMode(h.payments)}
	if !form && state != "form" {
		view["balance-state"] = "unavailable"
		for key, value := range h.topupBalanceView(r) {
			view[key] = value
		}
	}
	revision := requestRevision(r)
	page, err := revision.PageWithView(r.Context(), "topup-page", view)
	if err != nil {
		h.topupFailure(w, r, err, "payment_page")
		return
	}
	body, err := revision.render(page, nosurf.Token(r), "", hostBindings{paymentsEnabled: h.payments != nil, formEnabled: form, attempt: attempt})
	if err != nil {
		h.topupFailure(w, r, err, "payment_page")
		return
	}
	sessionBoundary(r, "commit")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != "HEAD" {
		_, _ = w.Write(body)
	}
}
