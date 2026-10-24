package ledger

import (
	"context"
	"errors"

	"possums-intake/internal/failuretype"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Kind exposes only validated numeric metadata or a closed core-code default.
// It does not establish an accounting outcome.
func (d *Diagnostic) Kind() failuretype.Type {
	if d == nil {
		return 0
	}
	if d.kind.Valid() {
		return d.kind
	}
	return defaultFailureKind(d.Code)
}

func defaultFailureKind(code Code) failuretype.Type {
	switch code {
	case CodeInput:
		return failuretype.RequestInvalid
	case CodeNativeHold, CodeExpired, CodeRenewalClosed, CodeRenewalNoProgress, CodePaymentDisabled, CodePaymentExpired, CodePaymentConflict:
		return failuretype.PolicyRefused
	case CodeKey, CodeKeyWindow, CodeConflict, CodeRenewalPrecondition:
		return failuretype.IdempotencyRefused
	case CodeAccount:
		return failuretype.AccountMissing
	case CodeDisabled, CodeInsufficient:
		return failuretype.AdmissionRefused
	case CodeOverflow, CodeCharge:
		return failuretype.AmountRefused
	case CodeEvidence, CodePaymentMissing:
		return failuretype.EvidenceUnavailable
	case CodeWriter:
		return failuretype.WriterWaitExhausted
	case CodeWork, CodeStorage:
		// CodeWork alone retains no actual context cause. Its legacy public
		// wording is unchanged until transaction owners map their evidence.
		return failuretype.TransactionWorkUnknown
	case CodeBegin:
		return failuretype.TransactionBeginUnknown
	case CodeRandom, CodePaymentRandom:
		return failuretype.IdentityUnavailable
	case CodeCorrupt:
		return failuretype.StateRejected
	case CodeCommit:
		return failuretype.TransactionCompletionUnknown
	case CodeClosed:
		return failuretype.NotReady
	case CodeClose:
		return failuretype.StorageCloseUnknown
	case CodeStartup, CodeExisting, CodePath:
		return failuretype.InitializationUnknown
	case CodeMissing:
		return failuretype.StorageOpenUnknown
	case CodeSchema:
		return failuretype.SchemaRejected
	case CodeRoutes:
		return failuretype.RuntimeSetupUnknown
	default:
		return 0
	}
}

func validFallback(kind failuretype.Type) failuretype.Type {
	if kind.Valid() {
		return kind
	}
	return failuretype.TransactionCompletionUnknown
}

// FailureKind preserves trusted numeric metadata and supported typed causes
// through standard error wrappers. nil is not a failure. No error text is read,
// and SQL no-rows requires the caller's lookup contract, not a global mapping.
func FailureKind(err error, fallback failuretype.Type) failuretype.Type {
	_, kind := classifiedFailure(err, fallback)
	return kind
}

// classifiedFailure retains a diagnostic only for the duration of inspection.
// Hostile As/Is/Unwrap methods cannot escape this boundary with panic payloads.
func classifiedFailure(err error, fallback failuretype.Type) (d *Diagnostic, kind failuretype.Type) {
	if err == nil {
		return nil, 0
	}
	kind = validFallback(fallback)
	defer func() {
		if recover() != nil {
			d = nil
			kind = validFallback(fallback)
		}
	}()
	if errors.As(err, &d) && d != nil {
		if known := d.Kind(); known.Valid() {
			return d, known
		}
	}
	if errors.Is(err, context.Canceled) {
		return nil, failuretype.Cancelled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, failuretype.DeadlineExceeded
	}
	var driver *sqlite.Error
	if errors.As(err, &driver) && driver != nil {
		switch driver.Code() & 0xff {
		case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
			return nil, failuretype.SQLiteBusy
		case sqlite3.SQLITE_CONSTRAINT:
			return nil, failuretype.SQLiteConstraint
		case sqlite3.SQLITE_CORRUPT, sqlite3.SQLITE_NOTADB, sqlite3.SQLITE_IOERR, sqlite3.SQLITE_FULL, sqlite3.SQLITE_CANTOPEN:
			return nil, failuretype.SQLiteStorageFailure
		}
	}
	return nil, kind
}

// CanonicalFailure is opt-in: legacy Failure and its callers are unchanged.
// Only closed core codes and schema reasons select public templates; public
// fields are never copied. Owners of init/runtime variants adapt separately.
func CanonicalFailure(err error, fallback failuretype.Type) *Diagnostic {
	if err == nil {
		return nil
	}
	d, kind := classifiedFailure(err, fallback)
	if d != nil && defaultFailureKind(d.Code).Valid() {
		clean := diagnostic(d.Code)
		if d.Code == CodeSchema {
			clean = schemaFailure(schemaReason(d.Constraint))
		}
		clean.kind = kind
		return clean
	}
	// The failing stage comes from the owner's fallback, not from the cause
	// category. Neither a category nor delivery interruption proves rollback.
	stageKind := validFallback(fallback)
	code := CodeBoundary
	if stageKind == failuretype.TransactionCompletionUnknown {
		code = CodeCommit
	}
	return &Diagnostic{
		Code: code, Stage: failureStage(stageKind), Constraint: "authoritative_boundary_evidence",
		Outcome: "unknown", kind: kind,
		NextStep: "Underlying cause unknown. Previously observed accounting outcomes are unchanged. Inspect this component privately; preserve original state and do not replay a mutation to diagnose it.",
	}
}

func failureStage(kind failuretype.Type) string {
	switch kind {
	case failuretype.RequestInvalid:
		return "validation"
	case failuretype.AuthenticationRefused:
		return "authentication"
	case failuretype.PolicyRefused:
		return "policy"
	case failuretype.AccountMissing:
		return "account"
	case failuretype.EvidenceUnavailable:
		return "lookup"
	case failuretype.IdempotencyRefused:
		return "key"
	case failuretype.AdmissionRefused:
		return "admission"
	case failuretype.AmountRefused:
		return "arithmetic"
	case failuretype.NotReady:
		return "readiness"
	case failuretype.Cancelled, failuretype.DeadlineExceeded, failuretype.SQLiteBusy, failuretype.SQLiteConstraint, failuretype.SQLiteStorageFailure, failuretype.TransactionWorkUnknown:
		return "transaction_work"
	case failuretype.WriterWaitExhausted:
		return "writer_gate"
	case failuretype.SchemaRejected, failuretype.StateRejected, failuretype.StorageValidationUnknown:
		return "state_validation"
	case failuretype.IdentityUnavailable:
		return "reservation_identity"
	case failuretype.TransactionBeginUnknown:
		return "transaction_begin"
	case failuretype.TransactionRollbackUnknown:
		return "transaction_rollback"
	case failuretype.TransactionAfterCompletionUnknown:
		return "transaction_after_completion"
	case failuretype.InitializationUnknown:
		return "init"
	case failuretype.StorageOpenUnknown:
		return "storage_open"
	case failuretype.StorageCloseUnknown:
		return "storage_close"
	case failuretype.NativeAuthUnknown:
		return "native_auth"
	case failuretype.NativeReadUnknown:
		return "native_read"
	case failuretype.NativeWriteUnknown:
		return "native_write"
	case failuretype.NativeAssetUnknown:
		return "native_asset"
	case failuretype.RequestReadUnknown:
		return "request_read"
	case failuretype.RequestDispatchUnknown:
		return "request_dispatch"
	case failuretype.ResponseEncodingUnknown:
		return "response_encoding"
	case failuretype.ResponseDeliveryUnknown:
		return "response_delivery"
	case failuretype.MaintenanceUnknown:
		return "maintenance"
	case failuretype.RuntimeSetupUnknown:
		return "runtime_setup"
	case failuretype.RuntimeServeUnknown:
		return "runtime_serve"
	case failuretype.RuntimeShutdownUnknown:
		return "runtime_shutdown"
	default:
		return "transaction_completion"
	}
}
