package payments

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"possums-intake/internal/ledger"
	"possums-intake/internal/stripepay"
)

const WebhookPath = "/payments/stripe/webhook"

type webhookReply struct {
	Status string `json:"status"`
	Error  *Error `json:"error,omitempty"`
}

// WebhookHandler is NOT registered by this package or ordinary runtime. The
// owner must mount the exact route outside customer CSRF/session/shared-key
// accounting middleware. Only the supported Stripe signature authenticates it.
func (s *Service) WebhookHandler() http.Handler {
	return http.HandlerFunc(s.webhook)
}

func (s *Service) webhook(w http.ResponseWriter, r *http.Request) {
	outcome := "unknown"
	started := false
	defer func() {
		if recover() != nil && !started {
			writeWebhook(w, http.StatusServiceUnavailable, webhookReply{Status: "unavailable", Error: localFailure("PAYMENT_COMPONENT_CAUSE_UNKNOWN", "payment_webhook", "component_cause_unknown", outcome)})
		}
		// Once delivery starts, no second response or raw panic output. Authoritative
		// state is unchanged; net/http/client delivery may be unavailable. No retry.
	}()
	status, reply := s.webhookResult(r)
	if reply.Error != nil {
		outcome = reply.Error.Outcome
	} else {
		outcome = reply.Status
	}
	started = true
	writeWebhook(w, status, reply)
}

func (s *Service) webhookResult(r *http.Request) (int, webhookReply) {
	refuse := func(status int, code, constraint string) (int, webhookReply) {
		return status, webhookReply{Status: "rejected", Error: localFailure(code, "payment_webhook", constraint, "notification_rejected_no_funding")}
	}
	if r.URL.Path != WebhookPath || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" || r.URL.Opaque != "" || r.URL.Fragment != "" {
		return refuse(http.StatusNotFound, "PAYMENT_WEBHOOK_ROUTE_REFUSED", "fixed_path_without_selectors_required")
	}
	if r.Method != http.MethodPost {
		return refuse(http.StatusMethodNotAllowed, "PAYMENT_WEBHOOK_METHOD_REFUSED", "post_required")
	}
	if !s.configured() {
		return http.StatusServiceUnavailable, webhookReply{Status: "unavailable", Error: localFailure("PAYMENT_OFFLINE_CONFIGURATION_INVALID", "payment_webhook", "explicit_offline_admission_required", "not_attempted")}
	}
	if r.ContentLength > stripepay.MaxWebhookBytes {
		return refuse(http.StatusRequestEntityTooLarge, "PAYMENT_WEBHOOK_BODY_REFUSED", "bounded_raw_body_required")
	}
	if r.Body == nil {
		return refuse(http.StatusBadRequest, "PAYMENT_WEBHOOK_BODY_REFUSED", "complete_raw_body_required")
	}
	raw, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, stripepay.MaxWebhookBytes))
	if err != nil {
		var size *http.MaxBytesError
		if errors.As(err, &size) {
			return refuse(http.StatusRequestEntityTooLarge, "PAYMENT_WEBHOOK_BODY_REFUSED", "bounded_raw_body_required")
		}
		return refuse(http.StatusBadRequest, "PAYMENT_WEBHOOK_BODY_REFUSED", "complete_raw_body_required")
	}
	signatures := r.Header.Values("Stripe-Signature")
	if len(signatures) != 1 {
		return refuse(http.StatusBadRequest, "PAYMENT_WEBHOOK_SIGNATURE_REFUSED", "one_signature_header_required")
	}
	notification, err := s.stripe.DecodeWebhook(raw, signatures[0])
	if err != nil {
		return http.StatusBadRequest, webhookReply{Status: "rejected", Error: projectFailure(err)}
	}
	if notification.Kind == stripepay.EventIrrelevant {
		return http.StatusOK, webhookReply{Status: "ignored"}
	}

	p, err := s.ledger.ReadPaymentForProvider(r.Context(), notification.Reference)
	if unavailableReference(err) {
		return http.StatusOK, webhookReply{Status: "ineligible"}
	}
	if err != nil {
		return http.StatusServiceUnavailable, webhookReply{Status: "unavailable", Error: projectFailure(err)}
	}
	// Signed selectors never authorize a new attempt or a different binding.
	if p.CreateState == "prepared" || (p.SessionID != nil && *p.SessionID != notification.SessionID) {
		return http.StatusOK, webhookReply{Status: "ineligible"}
	}
	expected, err := adapterPurchase(p)
	if err != nil {
		return http.StatusServiceUnavailable, webhookReply{Status: "unavailable", Error: projectFailure(err)}
	}
	evidence, err := s.stripe.RetrieveEvidence(r.Context(), notification.SessionID, expected)
	if err != nil && !canonicalBlock(evidence, err) {
		return http.StatusServiceUnavailable, webhookReply{Status: "unavailable", Error: projectFailure(err)}
	}
	result, err := s.ledger.FundStripePurchase(r.Context(), fundingEvidence(evidence))
	if unavailableReference(err) {
		return http.StatusOK, webhookReply{Status: "ineligible"}
	}
	if err != nil {
		return http.StatusServiceUnavailable, webhookReply{Status: "unavailable", Error: projectFailure(err)}
	}
	switch result.Status {
	case ledger.PaymentFunded:
		return http.StatusOK, webhookReply{Status: "funded"}
	case ledger.PaymentDuplicate:
		return http.StatusOK, webhookReply{Status: "already_funded"}
	case ledger.PaymentBlocked:
		return http.StatusOK, webhookReply{Status: "blocked"}
	default:
		return http.StatusServiceUnavailable, webhookReply{Status: "unavailable", Error: localFailure("PAYMENT_COMPONENT_CAUSE_UNKNOWN", "payment_funding", "component_cause_unknown", "unknown")}
	}
}

func canonicalBlock(e stripepay.Evidence, err error) bool {
	d, ok := err.(*stripepay.Error)
	return ok && d.Code == stripepay.CodeEvidence && d.Stage == stripepay.StageCharge && d.Constraint == stripepay.ConstraintRefund && d.Outcome == stripepay.OutcomeReadUnavailable &&
		(e.Refunded || e.Disputed || e.AmountRefunded != 0 || e.RefundObserved)
}

// A delivery failure has no remaining HTTP channel. Its typed transient result
// preserves the observed funding outcome; it is not logged, retained, exported,
// or sent through website/accounting observers.
func writeWebhook(w http.ResponseWriter, status int, reply webhookReply) (err error) {
	outcome := reply.Status
	if reply.Error != nil {
		outcome = reply.Error.Outcome
	}
	defer recoverError(&err, "payment_webhook_delivery", outcome)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	// The closed DTO has no unsupported values. Transport delivery failure cannot
	// erase a known commit or justify another funding attempt in this handler.
	if json.NewEncoder(w).Encode(reply) != nil {
		return localFailure("PAYMENT_WEBHOOK_DELIVERY_UNAVAILABLE", "payment_webhook_delivery", "response_delivery_unconfirmed", outcome)
	}
	return nil
}
