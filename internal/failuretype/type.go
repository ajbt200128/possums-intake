// Package failuretype defines data-only classifications at intake interfaces.
package failuretype

// Type is numeric so observations cannot carry diagnostic or request text.
// Zero is invalid, not a healthy result or a cause-unknown classification.
type Type uint8

const (
	RequestInvalid                    Type = 1
	AuthenticationRefused             Type = 2
	PolicyRefused                     Type = 3
	AccountMissing                    Type = 4
	EvidenceUnavailable               Type = 5
	IdempotencyRefused                Type = 6
	AdmissionRefused                  Type = 7
	AmountRefused                     Type = 8
	NotReady                          Type = 9
	Cancelled                         Type = 10
	DeadlineExceeded                  Type = 11
	WriterWaitExhausted               Type = 12
	SQLiteBusy                        Type = 13
	SQLiteConstraint                  Type = 14
	SQLiteStorageFailure              Type = 15
	SchemaRejected                    Type = 16
	StateRejected                     Type = 17
	IdentityUnavailable               Type = 18
	TransactionBeginUnknown           Type = 19
	TransactionWorkUnknown            Type = 20
	TransactionRollbackUnknown        Type = 21
	TransactionCompletionUnknown      Type = 22
	TransactionAfterCompletionUnknown Type = 23
	InitializationUnknown             Type = 24
	StorageOpenUnknown                Type = 25
	StorageValidationUnknown          Type = 26
	StorageCloseUnknown               Type = 27
	NativeAuthUnknown                 Type = 28
	NativeReadUnknown                 Type = 29
	NativeWriteUnknown                Type = 30
	NativeAssetUnknown                Type = 31
	RequestReadUnknown                Type = 32
	RequestDispatchUnknown            Type = 33
	ResponseEncodingUnknown           Type = 34
	ResponseDeliveryUnknown           Type = 35
	MaintenanceUnknown                Type = 36
	RuntimeSetupUnknown               Type = 37
	RuntimeServeUnknown               Type = 38
	RuntimeShutdownUnknown            Type = 39

	Count = 39
)

func (t Type) Valid() bool { return t >= RequestInvalid && t <= RuntimeShutdownUnknown }

// Label returns only compiled labels; invalid values have no label.
func (t Type) Label() string {
	switch t {
	case RequestInvalid:
		return "request_invalid"
	case AuthenticationRefused:
		return "authentication_refused"
	case PolicyRefused:
		return "policy_refused"
	case AccountMissing:
		return "account_missing"
	case EvidenceUnavailable:
		return "evidence_unavailable"
	case IdempotencyRefused:
		return "idempotency_refused"
	case AdmissionRefused:
		return "admission_refused"
	case AmountRefused:
		return "amount_refused"
	case NotReady:
		return "not_ready"
	case Cancelled:
		return "cancelled"
	case DeadlineExceeded:
		return "deadline_exceeded"
	case WriterWaitExhausted:
		return "writer_wait_exhausted"
	case SQLiteBusy:
		return "sqlite_busy"
	case SQLiteConstraint:
		return "sqlite_constraint"
	case SQLiteStorageFailure:
		return "sqlite_storage_failure"
	case SchemaRejected:
		return "schema_rejected"
	case StateRejected:
		return "state_rejected"
	case IdentityUnavailable:
		return "identity_unavailable"
	case TransactionBeginUnknown:
		return "transaction_begin_unknown"
	case TransactionWorkUnknown:
		return "transaction_work_unknown"
	case TransactionRollbackUnknown:
		return "transaction_rollback_unknown"
	case TransactionCompletionUnknown:
		return "transaction_completion_unknown"
	case TransactionAfterCompletionUnknown:
		return "transaction_after_completion_unknown"
	case InitializationUnknown:
		return "initialization_unknown"
	case StorageOpenUnknown:
		return "storage_open_unknown"
	case StorageValidationUnknown:
		return "storage_validation_unknown"
	case StorageCloseUnknown:
		return "storage_close_unknown"
	case NativeAuthUnknown:
		return "native_auth_unknown"
	case NativeReadUnknown:
		return "native_read_unknown"
	case NativeWriteUnknown:
		return "native_write_unknown"
	case NativeAssetUnknown:
		return "native_asset_unknown"
	case RequestReadUnknown:
		return "request_read_unknown"
	case RequestDispatchUnknown:
		return "request_dispatch_unknown"
	case ResponseEncodingUnknown:
		return "response_encoding_unknown"
	case ResponseDeliveryUnknown:
		return "response_delivery_unknown"
	case MaintenanceUnknown:
		return "maintenance_unknown"
	case RuntimeSetupUnknown:
		return "runtime_setup_unknown"
	case RuntimeServeUnknown:
		return "runtime_serve_unknown"
	case RuntimeShutdownUnknown:
		return "runtime_shutdown_unknown"
	default:
		return ""
	}
}
