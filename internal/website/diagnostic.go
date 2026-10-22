package website

import (
	"context"
	"fmt"
	"net/http"

	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

// HTTP failures are closed here, never constructed from exception text. The
// request owner observes each boundary once; a later delivery failure is distinct.
func httpFault(code string) *ledger.Diagnostic {
	stage, constraint, next, kind := "website_request", "registered_native_form", "Reload the native form; no action was replayed.", failuretype.RequestInvalid
	switch code {
	case "SITE_SESSION_UNAVAILABLE":
		stage, constraint, next, kind = "website_session", "store_completion_cause_unknown", "Session completion is unknown. Close this browser session and sign in again; do not assume revocation.", failuretype.NativeWriteUnknown
	case "SITE_CSRF_REFUSED":
		constraint, next, kind = "same_origin_csrf", "Enable first-party cookies and same-origin form requests, then reload this form.", failuretype.PolicyRefused
	case "SITE_ROUTE_REFUSED":
		constraint = "canonical_path_without_query"
	case "SITE_METHOD_REFUSED":
		constraint = "registered_method"
	case "SITE_PAGE_MISSING", "SITE_ASSET_MISSING":
		constraint = "registered_resource"
	case "SITE_HTTPS_REQUIRED":
		constraint, next = "tls_required", "Open this website over HTTPS."
	case "SITE_FORM_REFUSED":
		constraint = "bounded_single_native_fields"
	case "SITE_REVISION_STALE":
		constraint, next = "current_revision", "Reload this older form before submitting."
	case "SITE_BUSY":
		constraint, kind, next = "bounded_admission", failuretype.AdmissionRefused, "Local website admission is full; try later."
	case "SITE_AUTH_REQUIRED", "SITE_AUTH_REFUSED":
		stage, constraint, next, kind = "website_auth", "current_authorized_credential", "Sign in with valid credentials; no website session was granted by this refusal.", failuretype.AuthenticationRefused
	case "SITE_AUTH_MFA_UNSUPPORTED":
		stage, constraint, next, kind = "website_auth", "supported_auth_configuration", "Contact the operator for a supported sign-in method; no website session was created.", failuretype.PolicyRefused
	case "SITE_AUTH_CONFIG":
		stage, constraint, next, kind = "website_auth", "quiet_fixed_customer_collection", "Ask the operator to check the supported customer auth configuration privately; no website session was created.", failuretype.PolicyRefused
	case "SITE_AUTH_INTERRUPTED":
		stage, constraint, next, kind = "website_auth", "bounded_sdk_context", "Authentication completion is unconfirmed after interruption. Inspect privately before another attempt.", failuretype.Cancelled
	case "SITE_AUTH_UNAVAILABLE":
		stage, constraint, next, kind = "website_auth", "sdk_completion_cause_unknown", "Authentication completion may be unknown. Inspect availability privately before another attempt.", failuretype.NativeAuthUnknown
	case "SITE_ACTION_REFUSED":
		constraint, kind = "approved_backend_action", failuretype.PolicyRefused
	case "SITE_RENDER_UNAVAILABLE":
		stage, constraint, kind = "website_render", "complete_page_cause_unknown", failuretype.ResponseEncodingUnknown
	case "SITE_ASSET_UNAVAILABLE":
		stage, constraint, kind = "website_asset", "embedded_read_cause_unknown", failuretype.NativeAssetUnknown
	case "SITE_DELIVERY_UNAVAILABLE":
		stage, constraint, next, kind = "website_delivery", "response_delivery_cause_unknown", "Delivery is unconfirmed; do not assume login or logout was undone. No action was replayed.", failuretype.ResponseDeliveryUnknown
	case "SITE_DISPATCH_UNAVAILABLE":
		stage, constraint, next, kind = "website_dispatch", "handler_completion_cause_unknown", "Request completion is unknown. Close this browser session before signing in again; no action was replayed.", failuretype.RequestDispatchUnknown
	default:
		return httpFault("SITE_DISPATCH_UNAVAILABLE")
	}
	return fault(code, stage, constraint, next, kind)
}

func classifiedHTTPFault(code string, cause error) *ledger.Diagnostic {
	local := httpFault(code)
	clean := ledger.CanonicalFailure(cause, local.Kind())
	if clean == nil {
		return local
	}
	clean.Code, clean.Stage, clean.Constraint, clean.Outcome, clean.NextStep = local.Code, local.Stage, local.Constraint, local.Outcome, local.NextStep
	return clean
}

// Preserve the runtime's closed evidence, not arbitrary diagnostic strings.
// HTTP still owns emission and independently established authentication outcome.
func renderHTTPFault(cause error) *ledger.Diagnostic {
	if d, ok := cause.(*ledger.Diagnostic); ok && d != nil {
		stage, valid := "page_evaluation", false
		switch d.Code {
		case "SITE_EXECUTION_LIMIT":
			valid = d.Kind() == failuretype.PolicyRefused && (d.Constraint == "step_budget" || d.Constraint == "evaluation_nesting")
		case "SITE_EXECUTION_INTERRUPTED":
			valid = d.Kind() == failuretype.Cancelled && d.Constraint == "context_cancelled" || d.Kind() == failuretype.DeadlineExceeded && d.Constraint == "context_deadline"
		case "SITE_PAGE_REFUSED":
			valid = d.Kind() == failuretype.PolicyRefused && d.Constraint == "declared_presentation_capability"
		case "SITE_PAGE_VIEW_REFUSED":
			valid = d.Kind() == failuretype.PolicyRefused && d.Constraint == "bounded_string_view"
		case "SITE_PAGE_TEXT_LIMIT":
			if d.Kind() == failuretype.PolicyRefused && d.Stage == "page_evaluation" && d.Constraint == "complete_page_text_budget" {
				return fault("SITE_PAGE_TEXT_LIMIT", d.Stage, d.Constraint, "The complete page exceeds presentation limits. Ask the operator to inspect the page privately; no data was truncated or action replayed.", failuretype.PolicyRefused)
			}
		case "SITE_ACCOUNT_VIEW_LIMIT":
			if d.Kind() == failuretype.PolicyRefused && d.Stage == "account_snapshot_view" && d.Constraint == "complete_bounded_snapshot" {
				return accountViewLimit()
			}
		case "SITE_ACCOUNT_MONEY_UNAVAILABLE":
			if d.Kind() == failuretype.NativeReadUnknown && d.Stage == "account_snapshot_validation" && d.Constraint == "canonical_uint64_microunits" {
				return accountMoneyFault()
			}
		case "SITE_ACCOUNT_SCOPE_UNAVAILABLE":
			if d.Kind() == failuretype.NativeReadUnknown && d.Stage == "account_snapshot_validation" && d.Constraint == "matching_account_and_pending_rows" {
				return accountScopeFault()
			}
		case "SITE_RENDER_UNAVAILABLE":
			valid = d.Kind() == failuretype.ResponseEncodingUnknown && d.Constraint == "bounded_complete_page_cause_unknown"
		case "SITE_DOCUMENT_UNAVAILABLE":
			stage = "document_render"
			valid = d.Kind() == failuretype.ResponseEncodingUnknown && d.Constraint == "bounded_document_cause_unknown"
		}
		if valid && d.Stage == stage {
			return fault(string(d.Code), stage, d.Constraint, "Reload the page; no backend action was replayed. Report only this code if the problem continues.", d.Kind())
		}
	}
	return classifiedHTTPFault("SITE_RENDER_UNAVAILABLE", cause)
}

func diagnosticText(d *ledger.Diagnostic) string {
	return fmt.Sprintf("%s — stage=%s; constraint=%s; outcome=%s. %s", d.Code, d.Stage, d.Constraint, d.Outcome, d.NextStep)
}

type responseKey struct{}
type responseState struct {
	http.ResponseWriter
	host                           *Host
	outcome                        string
	panicCode                      string
	sessionStage                   string
	wrote, blocked, deliveryFailed bool
}

func responseFor(r *http.Request) *responseState {
	s, _ := r.Context().Value(responseKey{}).(*responseState)
	return s
}
func (s *responseState) observe(d *ledger.Diagnostic) {
	failureowner.Observe(s.host.observer, d.Kind())
}
func (s *responseState) deliveryFailure() {
	if s.deliveryFailed {
		return
	}
	s.deliveryFailed = true
	s.observe(httpFault("SITE_DELIVERY_UNAVAILABLE"))
}
func (s *responseState) WriteHeader(status int) {
	if s.blocked || s.wrote {
		return
	}
	s.wrote = true
	defer func() {
		if recover() != nil {
			s.deliveryFailure()
			s.blocked = true
		}
	}()
	s.ResponseWriter.WriteHeader(status)
}
func (s *responseState) Write(b []byte) (n int, err error) {
	if s.blocked {
		return len(b), nil
	}
	if !s.wrote {
		s.WriteHeader(200)
	}
	if s.blocked {
		return 0, httpFault("SITE_DELIVERY_UNAVAILABLE")
	}
	defer func() {
		if recover() != nil {
			n, err = 0, httpFault("SITE_DELIVERY_UNAVAILABLE")
		}
		if err != nil || n != len(b) {
			s.deliveryFailure()
			err = httpFault("SITE_DELIVERY_UNAVAILABLE")
		}
	}()
	return s.ResponseWriter.Write(b)
}
func (h *Host) ownResponse(w http.ResponseWriter, r *http.Request) (*responseState, *http.Request) {
	s := &responseState{ResponseWriter: w, host: h, outcome: "no_action"}
	return s, r.WithContext(context.WithValue(r.Context(), responseKey{}, s))
}
func (h *Host) report(r *http.Request, d *ledger.Diagnostic) *ledger.Diagnostic {
	clean := *d
	if s := responseFor(r); s != nil {
		clean.Outcome = s.outcome
		s.observe(&clean)
	}
	return &clean
}
func setOutcome(r *http.Request, outcome string) {
	if s := responseFor(r); s != nil {
		s.outcome = outcome
	}
}
func sessionBoundary(r *http.Request, stage string) {
	if s := responseFor(r); s != nil {
		s.sessionStage = stage
	}
}
func sessionFault(r *http.Request, cause error) *ledger.Diagnostic {
	local := httpFault("SITE_SESSION_UNAVAILABLE")
	kind := local.Kind()
	if s := responseFor(r); s != nil {
		switch s.sessionStage {
		case "load":
			local.Stage, kind = "website_session_load", failuretype.NativeReadUnknown
		case "renew":
			local.Stage = "website_session_renew"
		case "delete":
			local.Stage = "website_session_delete"
		case "commit":
			local.Stage = "website_session_commit"
		}
	}
	clean := ledger.CanonicalFailure(cause, kind)
	if clean == nil {
		clean = ledger.CanonicalFailure(&ledger.Diagnostic{}, kind)
	}
	clean.Code, clean.Stage, clean.Constraint, clean.Outcome, clean.NextStep = local.Code, local.Stage, local.Constraint, local.Outcome, local.NextStep
	return clean
}
func panicStage(r *http.Request, code string) {
	if s := responseFor(r); s != nil {
		s.panicCode = code
	}
}

// SCS invokes ErrorFunc while its response writer is committing. Write directly
// to the outer writer to avoid recursive commits, then suppress SCS's original
// response. This changes delivery only, not stock session/store semantics.
func (h *Host) sessionError(w http.ResponseWriter, r *http.Request, err error) {
	d := h.report(r, sessionFault(r, err))
	if s := responseFor(r); s != nil {
		if !s.wrote {
			s.Header().Del("Location")
			h.plain(s, 503, d)
		}
		s.blocked = true
		return
	}
	h.plain(w, 503, d)
}
func (h *Host) recoverHTTP(w *responseState, r *http.Request) {
	if recover() == nil {
		return
	}
	code := w.panicCode
	if code == "" {
		code = "SITE_DISPATCH_UNAVAILABLE"
	}
	diagnostic := httpFault(code)
	if code == "SITE_SESSION_UNAVAILABLE" {
		diagnostic = sessionFault(r, nil)
	}
	d := h.report(r, diagnostic)
	if !w.wrote {
		w.Header().Del("Location")
		h.plain(w, 503, d)
	}
	w.blocked = true
}
