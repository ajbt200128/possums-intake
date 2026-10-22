package stripepay

import "fmt"

type Code string
type Stage string
type Constraint string
type Outcome string
type NextStep string

const (
	CodeSandboxConfiguration Code = "STRIPE_SANDBOX_CONFIGURATION_INVALID"
	CodeConfiguration        Code = "STRIPE_OFFLINE_CONFIGURATION_INVALID"
	CodeInput                Code = "STRIPE_INPUT_INVALID"
	CodeProvider             Code = "STRIPE_PROVIDER_UNAVAILABLE"
	CodeDecode               Code = "STRIPE_PROVIDER_DECODE_UNKNOWN"
	CodeEvidence             Code = "STRIPE_EVIDENCE_REJECTED"
	CodeWebhook              Code = "STRIPE_WEBHOOK_REJECTED"
	CodeUnexpected           Code = "STRIPE_COMPONENT_CAUSE_UNKNOWN"

	StageSandboxConfiguration Stage = "sandbox_configuration"
	StageConfiguration        Stage = "offline_configuration"
	StageCreate               Stage = "checkout_create"
	StageHostedURL            Stage = "checkout_presentation"
	StageSession              Stage = "session_read"
	StageIntent               Stage = "intent_read"
	StageCharge               Stage = "charge_read"
	StageWebhook              Stage = "webhook_verify"

	ConstraintSandbox           Constraint = "fixed_sandbox_and_private_test_credentials_required"
	ConstraintSandboxURL        Constraint = "canonical_test_checkout_url_required"
	ConstraintOffline           Constraint = "loopback_fixture_configuration_required"
	ConstraintInput             Constraint = "original_purchase_or_identity_invalid"
	ConstraintTransport         Constraint = "transport_completion_missing"
	ConstraintResponseSize      Constraint = "response_limit_exceeded"
	ConstraintStatus            Constraint = "http_success_required"
	ConstraintRedirect          Constraint = "redirect_forbidden"
	ConstraintDecode            Constraint = "sdk_decoding_cause_unknown"
	ConstraintContext           Constraint = "pinned_direct_test_context_required"
	ConstraintSession           Constraint = "original_session_terms_required"
	ConstraintLineItem          Constraint = "one_original_fixed_price_required"
	ConstraintPaid              Constraint = "completed_paid_session_required"
	ConstraintIntent            Constraint = "succeeded_original_intent_required"
	ConstraintCharge            Constraint = "succeeded_paid_captured_card_required"
	ConstraintRefund            Constraint = "no_refund_or_dispute_evidence_required"
	ConstraintRefundUnavailable Constraint = "complete_validated_refund_evidence_required"
	ConstraintURL               Constraint = "validated_fixture_hosted_url_required"
	ConstraintSignature         Constraint = "fresh_supported_signature_required"
	ConstraintWebhookSize       Constraint = "webhook_limit_exceeded"
	ConstraintEvent             Constraint = "supported_signed_event_required"
	ConstraintUnknown           Constraint = "component_cause_unknown"

	OutcomeNotAttempted    Outcome = "not_attempted"
	OutcomeCreateUnknown   Outcome = "creation_unknown_not_replayed"
	OutcomeReadUnavailable Outcome = "funding_evidence_unavailable"
	OutcomeWebhookRejected Outcome = "notification_rejected_no_funding"

	NextFixSandbox NextStep = "correct_private_sandbox_configuration_before_attempting"
	NextFixFixture NextStep = "correct_offline_fixture_before_attempting"
	NextReconcile  NextStep = "inspect_original_purchase_without_create_replay_new_purchase_may_duplicate_payment"
	NextReadOnly   NextStep = "inspect_closed_code_and_original_purchase_no_funding_or_mutation_inferred"
	NextWebhook    NextStep = "check_signature_version_and_fixture_configuration_no_funding_inferred"
)

// Error is a closed, content-free diagnostic. It deliberately has no wrapped
// cause, raw exception text, provider identifiers, URLs, payloads or headers.
// CauseUnknown is explicit when only a component/stage is known.
type Error struct {
	Code         Code
	Stage        Stage
	Constraint   Constraint
	Outcome      Outcome
	NextStep     NextStep
	HTTPStatus   int
	CauseUnknown bool
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s stage=%s constraint=%s outcome=%s next=%s http_status=%d cause_unknown=%t", e.Code, e.Stage, e.Constraint, e.Outcome, e.NextStep, e.HTTPStatus, e.CauseUnknown)
}

func failure(code Code, stage Stage, constraint Constraint, outcome Outcome, status int) *Error {
	next := NextReadOnly
	switch outcome {
	case OutcomeNotAttempted:
		next = NextFixFixture
		if stage == StageSandboxConfiguration {
			next = NextFixSandbox
		}
	case OutcomeCreateUnknown:
		next = NextReconcile
	case OutcomeWebhookRejected:
		next = NextWebhook
	}
	return &Error{Code: code, Stage: stage, Constraint: constraint, Outcome: outcome, NextStep: next, HTTPStatus: status, CauseUnknown: code == CodeUnexpected || code == CodeDecode || constraint == ConstraintTransport}
}

func recoverBoundary(err *error, stage Stage, outcome Outcome) {
	if recover() != nil {
		*err = failure(CodeUnexpected, stage, ConstraintUnknown, outcome, 0)
	}
}
