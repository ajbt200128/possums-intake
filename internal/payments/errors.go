package payments

import (
	"errors"

	"possums-intake/internal/ledger"
	"possums-intake/internal/stripepay"
)

// Error is content-free and transient. Known ledger/Stripe errors normally pass
// through unchanged. A later create-binding failure retains the typed ledger
// cause, but explicitly does NOT imply that the provider create rolled back.
// There are no raw causes, metrics hooks, logs or identifiers in this boundary.
type Error struct {
	Code         string `json:"code"`
	Stage        string `json:"stage"`
	Constraint   string `json:"constraint"`
	Outcome      string `json:"outcome"`
	NextStep     string `json:"next_step"`
	HTTPStatus   int    `json:"http_status,omitempty"`
	CauseUnknown bool   `json:"cause_unknown"`
	cause        error
}

func (e *Error) Error() string { return e.Code }
func (e *Error) Unwrap() error { return e.cause }

func localFailure(code, stage, constraint, outcome string) *Error {
	return &Error{Code: code, Stage: stage, Constraint: constraint, Outcome: outcome, CauseUnknown: constraint == "component_cause_unknown", NextStep: "Inspect closed fields and local status; do not replay Checkout creation. A new purchase may duplicate payment."}
}
func recoverError(err *error, stage, outcome string) {
	if recover() != nil {
		*err = localFailure("PAYMENT_COMPONENT_CAUSE_UNKNOWN", stage, "component_cause_unknown", outcome)
	}
}
func creationBindingFailure(err error) *Error {
	d := projectFailure(err)
	d.Outcome = "checkout_created_local_binding_unconfirmed"
	d.NextStep = "Checkout was created but local binding is unconfirmed. Check local status; do not replay creation. A new purchase may duplicate payment."
	// Retain only supported closed typed causes, never arbitrary exceptions.
	if e, ok := err.(*ledger.Diagnostic); ok {
		d.cause = e
	}
	return d
}
func unavailableReference(err error) bool {
	var d *ledger.Diagnostic
	return errors.As(err, &d) && (d.Code == ledger.CodePaymentMissing || d.Code == ledger.CodePaymentExpired)
}
func projectFailure(err error) *Error {
	var local *Error
	if errors.As(err, &local) {
		copy := *local
		copy.cause = nil
		return &copy
	}
	var core *ledger.Diagnostic
	if errors.As(err, &core) {
		return &Error{Code: string(core.Code), Stage: core.Stage, Constraint: core.Constraint, Outcome: core.Outcome, NextStep: core.NextStep, CauseUnknown: core.Code == ledger.CodeCommit || core.Code == ledger.CodeStorage || core.Code == ledger.CodeBegin}
	}
	var provider *stripepay.Error
	if errors.As(err, &provider) {
		return &Error{Code: string(provider.Code), Stage: string(provider.Stage), Constraint: string(provider.Constraint), Outcome: string(provider.Outcome), NextStep: string(provider.NextStep), HTTPStatus: provider.HTTPStatus, CauseUnknown: provider.CauseUnknown}
	}
	return localFailure("PAYMENT_COMPONENT_CAUSE_UNKNOWN", "payment_boundary", "component_cause_unknown", "unknown")
}
