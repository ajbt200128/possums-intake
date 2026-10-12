package failuretype

import "testing"

func TestExactNumericTypesAndFixedLabels(t *testing.T) {
	if Count != 39 {
		t.Fatal("classification contract changed")
	}
	cases := []struct {
		kind   Type
		number uint8
		label  string
	}{
		{RequestInvalid, 1, "request_invalid"},
		{AuthenticationRefused, 2, "authentication_refused"},
		{PolicyRefused, 3, "policy_refused"},
		{AccountMissing, 4, "account_missing"},
		{EvidenceUnavailable, 5, "evidence_unavailable"},
		{IdempotencyRefused, 6, "idempotency_refused"},
		{AdmissionRefused, 7, "admission_refused"},
		{AmountRefused, 8, "amount_refused"},
		{NotReady, 9, "not_ready"},
		{Cancelled, 10, "cancelled"},
		{DeadlineExceeded, 11, "deadline_exceeded"},
		{WriterWaitExhausted, 12, "writer_wait_exhausted"},
		{SQLiteBusy, 13, "sqlite_busy"},
		{SQLiteConstraint, 14, "sqlite_constraint"},
		{SQLiteStorageFailure, 15, "sqlite_storage_failure"},
		{SchemaRejected, 16, "schema_rejected"},
		{StateRejected, 17, "state_rejected"},
		{IdentityUnavailable, 18, "identity_unavailable"},
		{TransactionBeginUnknown, 19, "transaction_begin_unknown"},
		{TransactionWorkUnknown, 20, "transaction_work_unknown"},
		{TransactionRollbackUnknown, 21, "transaction_rollback_unknown"},
		{TransactionCompletionUnknown, 22, "transaction_completion_unknown"},
		{TransactionAfterCompletionUnknown, 23, "transaction_after_completion_unknown"},
		{InitializationUnknown, 24, "initialization_unknown"},
		{StorageOpenUnknown, 25, "storage_open_unknown"},
		{StorageValidationUnknown, 26, "storage_validation_unknown"},
		{StorageCloseUnknown, 27, "storage_close_unknown"},
		{NativeAuthUnknown, 28, "native_auth_unknown"},
		{NativeReadUnknown, 29, "native_read_unknown"},
		{NativeWriteUnknown, 30, "native_write_unknown"},
		{NativeAssetUnknown, 31, "native_asset_unknown"},
		{RequestReadUnknown, 32, "request_read_unknown"},
		{RequestDispatchUnknown, 33, "request_dispatch_unknown"},
		{ResponseEncodingUnknown, 34, "response_encoding_unknown"},
		{ResponseDeliveryUnknown, 35, "response_delivery_unknown"},
		{MaintenanceUnknown, 36, "maintenance_unknown"},
		{RuntimeSetupUnknown, 37, "runtime_setup_unknown"},
		{RuntimeServeUnknown, 38, "runtime_serve_unknown"},
		{RuntimeShutdownUnknown, 39, "runtime_shutdown_unknown"},
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if uint8(c.kind) != c.number || !c.kind.Valid() || c.kind.Label() != c.label || seen[c.label] {
			t.Fatal("numeric or fixed-label contract violated")
		}
		seen[c.label] = true
	}
	for i := 0; i <= 255; i++ {
		kind := Type(i)
		valid := i >= 1 && i <= Count
		if kind.Valid() != valid || (kind.Label() != "") != valid {
			t.Fatal("invalid/unset classification has a label")
		}
	}
}
