package ledger

import "possums-intake/internal/failuretype"

// Code is a closed component-specific category, never an upstream error string.
type Code string

const (
	CodePaymentDisabled Code = "PAYMENT_SYNTHETIC_NOT_INITIALIZED"
	CodePaymentMissing  Code = "PAYMENT_REFERENCE_UNAVAILABLE"
	CodePaymentExpired  Code = "PAYMENT_LINK_EXPIRED"
	CodePaymentConflict Code = "PAYMENT_EVIDENCE_CONFLICT"
	CodePaymentRandom   Code = "PAYMENT_IDENTITY_UNAVAILABLE"
)

const (
	CodeInput               Code = "LEDGER_INPUT_INVALID"
	CodeNativeHold          Code = "LEDGER_NATIVE_BALANCE_HELD"
	CodeKey                 Code = "LEDGER_KEY_INVALID"
	CodeKeyWindow           Code = "LEDGER_KEY_WINDOW"
	CodeAccount             Code = "LEDGER_ACCOUNT_UNKNOWN"
	CodeConflict            Code = "LEDGER_KEY_CONFLICT"
	CodeDisabled            Code = "LEDGER_ADMISSION_DISABLED"
	CodeInsufficient        Code = "LEDGER_CREDIT_INSUFFICIENT"
	CodeOverflow            Code = "LEDGER_AMOUNT_OVERFLOW"
	CodeCharge              Code = "LEDGER_CHARGE_EXCEEDS_HOLD"
	CodeExpired             Code = "LEDGER_SETTLEMENT_EXPIRED"
	CodeRenewalClosed       Code = "LEDGER_RENEWAL_CLOSED"
	CodeRenewalPrecondition Code = "LEDGER_RENEWAL_PRECONDITION"
	CodeRenewalNoProgress   Code = "LEDGER_RENEWAL_NO_PROGRESS"
	CodeEvidence            Code = "LEDGER_EVIDENCE_UNAVAILABLE"
	CodeWriter              Code = "LEDGER_WRITER_UNAVAILABLE"
	CodeWork                Code = "LEDGER_WORK_CANCELLED"
	CodeBegin               Code = "LEDGER_TRANSACTION_BEGIN_UNAVAILABLE"
	CodeStorage             Code = "LEDGER_STORAGE_UNAVAILABLE"
	CodeRandom              Code = "LEDGER_RESERVATION_ID_UNAVAILABLE"
	CodeCorrupt             Code = "LEDGER_STATE_INVALID"
	CodeCommit              Code = "LEDGER_COMMIT_UNKNOWN"
	// CodeBoundary requires an owner's closed stage and numeric classification.
	// It does not imply that an accounting commit was attempted.
	CodeBoundary Code = "INTAKE_BOUNDARY_UNAVAILABLE"
	CodeClosed   Code = "LEDGER_NOT_READY"
	CodeClose    Code = "LEDGER_STORAGE_CLOSE_UNAVAILABLE"
	CodeStartup  Code = "LEDGER_STARTUP_STORAGE"
	CodeMissing  Code = "LEDGER_STARTUP_MISSING_STATE"
	CodeExisting Code = "LEDGER_INIT_NONEMPTY"
	CodeSchema   Code = "LEDGER_STARTUP_SCHEMA"
	CodePath     Code = "LEDGER_STATE_PATH_INVALID"
	CodeRoutes   Code = "LEDGER_HTTP_NOT_IMPLEMENTED"
)

// Diagnostic retains no wrapped cause, offending value or internal stack. HTTP
// plumbing may serialize it directly; do not attach raw errors to this value.
type Diagnostic struct {
	Code       Code   `json:"code"`
	Stage      string `json:"stage"`
	Constraint string `json:"constraint"`
	Outcome    string `json:"outcome"`
	NextStep   string `json:"next_step"`
	kind       failuretype.Type
}

func (d *Diagnostic) Error() string { return string(d.Code) }
func diagnostic(code Code) *Diagnostic {
	d := &Diagnostic{Code: code, kind: defaultFailureKind(code), Outcome: "no_effect", NextStep: "Correct the request; retry only the original identity."}
	switch code {
	case CodePaymentDisabled:
		d.Stage = "payment_setup"
		d.Constraint = "explicit_synthetic_initialization"
		d.NextStep = "Payment schema is absent. Only an explicitly authorized synthetic setup may initialize it; do not initialize as recovery."
	case CodePaymentMissing:
		d.Stage = "payment_lookup"
		d.Constraint = "retained_original_reference_and_account"
		d.Outcome = "unavailable"
		d.NextStep = "No usable original reference is available. This does not establish whether the provider charged; never recreate it to resolve uncertainty."
	case CodePaymentExpired:
		d.Stage = "payment_window"
		d.Constraint = "immutable_original_deadline"
		d.NextStep = "Automatic funding or Checkout creation is unavailable under this identity. Provider payment outcome is not established; seek separate manual resolution."
	case CodePaymentConflict:
		d.Stage = "payment_evidence"
		d.Constraint = "canonical_original_synthetic_purchase"
		d.NextStep = "Evidence does not match the original synthetic purchase. Do not rebind objects, replay creation or issue a replacement credit."
	case CodePaymentRandom:
		d.Stage = "payment_identity"
		d.Constraint = "cryptographic_unique_identifier"
		d.NextStep = "Identity allocation rolled back; cause unknown. Preserve the original form identity and inspect locally."
	case CodeNativeHold:
		d.Stage = "native_account_write"
		d.Constraint = "no_active_holds_for_changed_balance"
		d.NextStep = "Changed absolute synthetic balances require no active holds. Refresh state; enabling or disabling remains permitted."
	case CodeInput:
		d.Stage = "validation"
		d.Constraint = "canonical_money_account_or_timeout"
	case CodeKey:
		d.Stage = "validation"
		d.Constraint = "immutable_v1_key_syntax"
	case CodeKeyWindow:
		d.Stage = "key"
		d.Constraint = "new_effect_key_window"
		d.NextStep = "This identity cannot create an effect. Do not replace an uncertain identity without reconciliation."
	case CodeAccount:
		d.Stage = "account"
		d.Constraint = "existing_account"
	case CodeConflict:
		d.Stage = "key"
		d.Constraint = "original_inputs_match"
	case CodeDisabled:
		d.Stage = "admission"
		d.Constraint = "enabled_account"
	case CodeInsufficient:
		d.Stage = "admission"
		d.Constraint = "available_covers_maximum"
	case CodeOverflow:
		d.Stage = "arithmetic"
		d.Constraint = "uint64_balance"
	case CodeCharge:
		d.Stage = "settlement"
		d.Constraint = "charge_within_maximum"
	case CodeRenewalClosed:
		d.Stage = "renewal"
		d.Constraint = "active_reservation"
		d.Outcome = "terminal"
		d.NextStep = "Inspect the authoritative terminal row; renewal cannot reopen it."
	case CodeRenewalPrecondition:
		d.Stage = "renewal"
		d.Constraint = "expected_not_after_authoritative_deadline"
		d.NextStep = "Inspect the authoritative row; this attempt did not renew the hold."
	case CodeRenewalNoProgress:
		d.Stage = "renewal"
		d.Constraint = "strictly_later_deadline"
		d.NextStep = "The authoritative deadline did not advance; this attempt did not renew the hold."
	case CodeExpired:
		d.Stage = "settlement"
		d.Constraint = "authoritative_deadline"
		d.Outcome = "expired"
		d.NextStep = "Inspect the authoritative expired row; late charging is forbidden."
	case CodeEvidence:
		d.Stage = "lookup"
		d.Constraint = "retained_reservation"
		d.Outcome = "unavailable"
		d.NextStep = "Absence is not evidence of nonexistence or refund. Reconcile privately; do not create a new effect to resolve uncertainty."
	case CodeWriter:
		d.Stage = "writer_gate"
		d.Constraint = "two_second_or_caller_wait"
		d.NextStep = "Retry the same identity later; this attempt entered no accounting transaction."
	case CodeWork:
		d.Stage = "transaction_work"
		d.Constraint = "one_second_or_caller_budget"
		d.NextStep = "Work rolled back. Retry the same identity if still eligible."
	case CodeRandom:
		d.Stage = "reservation_identity"
		d.Constraint = "cryptographic_unique_identifier"
		d.NextStep = "Identity generation failed; cause unknown. No effect committed. Retry the original key later."
	case CodeBegin:
		d.Stage = "transaction_begin"
		d.Constraint = "transaction_started"
		d.NextStep = "No accounting callback ran; underlying cause unknown. Check storage privately and retry the same identity."
	case CodeStorage:
		d.Stage = "transaction_work"
		d.Constraint = "usable_storage"
		d.NextStep = "Storage work rolled back; cause unknown. Check storage privately, then retry the same identity."
	case CodeCorrupt:
		d.Stage = "state_validation"
		d.Constraint = "canonical_consistent_accounting"
		d.NextStep = "Stop admission and inspect storage privately. Do not reset state or delete holds."
	case CodeCommit:
		d.Stage = "transaction_completion"
		d.Constraint = "authoritative_commit_evidence"
		d.Outcome = "unknown"
		d.NextStep = "Cause and effect outcome unknown. Resolve using the same identity; a new identity can duplicate an effect."
	case CodeClosed:
		d.Stage = "readiness"
		d.Constraint = "validated_open_storage"
		d.NextStep = "Restore readiness with the original state; never initialize as recovery."
	case CodeClose:
		d.Stage = "shutdown"
		d.Constraint = "storage_resource_closure"
		d.Outcome = "unavailable"
		d.NextStep = "Closure failed; cause unknown. Previously observed accounting outcomes are unchanged. Inspect resources privately; never reset state."
	case CodeStartup:
		d.Stage = "startup"
		d.Constraint = "usable_original_storage"
		d.Outcome = "unavailable"
		d.NextStep = "Underlying cause unknown. Check storage and permissions privately; preserve partial state and never reset it."
	case CodeMissing:
		d.Stage = "startup"
		d.Constraint = "existing_state_database"
		d.Outcome = "unavailable"
		d.NextStep = "Attach the original state. Only disposable empty local state may use explicit init."
	case CodeExisting:
		d.Stage = "init"
		d.Constraint = "deliberately_selected_empty_directory"
		d.NextStep = "Preserve existing or partial state; choose a different empty disposable directory."
	case CodeSchema:
		d.Stage = "state_validation"
		d.Constraint = "supported_schema_and_metadata"
		d.Outcome = "unavailable"
		d.NextStep = "This source requires the fresh merged schema v3. Preserve incompatible state and inspect privately; no migration, empty reset or financial restore is authorized."
	case CodePath:
		d.Stage = "startup"
		d.Constraint = "explicit_absolute_state_directory"
		d.NextStep = "Supply an absolute state directory; there is no working-directory fallback."
	case CodeRoutes:
		d.Stage = "startup"
		d.Constraint = "route_packet_pending"
		d.NextStep = "Packet 1 provides no HTTP service. Complete and qualify the route packet before serve."
	}
	return d
}

// Failure returns a safe fallback for unexpected errors at an application
// boundary. It does not attempt to classify hostile exception text.
func Failure(err error) *Diagnostic {
	if d, ok := err.(*Diagnostic); ok {
		return d
	}
	return diagnostic(CodeCommit)
}
