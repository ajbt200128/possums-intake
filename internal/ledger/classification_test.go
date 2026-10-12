package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"possums-intake/internal/failuretype"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var diagnosticKindCases = []struct {
	code Code
	kind failuretype.Type
}{
	{CodeInput, failuretype.RequestInvalid},
	{CodeNativeHold, failuretype.PolicyRefused},
	{CodeKey, failuretype.IdempotencyRefused},
	{CodeKeyWindow, failuretype.IdempotencyRefused},
	{CodeAccount, failuretype.AccountMissing},
	{CodeConflict, failuretype.IdempotencyRefused},
	{CodeDisabled, failuretype.AdmissionRefused},
	{CodeInsufficient, failuretype.AdmissionRefused},
	{CodeOverflow, failuretype.AmountRefused},
	{CodeCharge, failuretype.AmountRefused},
	{CodeExpired, failuretype.PolicyRefused},
	{CodeEvidence, failuretype.EvidenceUnavailable},
	{CodeWriter, failuretype.WriterWaitExhausted},
	{CodeWork, failuretype.TransactionWorkUnknown},
	{CodeBegin, failuretype.TransactionBeginUnknown},
	{CodeStorage, failuretype.TransactionWorkUnknown},
	{CodeRandom, failuretype.IdentityUnavailable},
	{CodeCorrupt, failuretype.StateRejected},
	{CodeCommit, failuretype.TransactionCompletionUnknown},
	{CodeClosed, failuretype.NotReady},
	{CodeClose, failuretype.StorageCloseUnknown},
	{CodeStartup, failuretype.InitializationUnknown},
	{CodeMissing, failuretype.StorageOpenUnknown},
	{CodeExisting, failuretype.InitializationUnknown},
	{CodeSchema, failuretype.SchemaRejected},
	{CodePath, failuretype.InitializationUnknown},
	{CodeRoutes, failuretype.RuntimeSetupUnknown},
}

// Wrapper/Error methods deliberately panic if classification formats text.
type classificationWrapper struct{ cause error }

func (classificationWrapper) Error() string   { panic("SYNTHETIC_PRIVATE_ERROR_TEXT") }
func (w classificationWrapper) Unwrap() error { return w.cause }

type classificationRawError struct{}

func (classificationRawError) Error() string { panic("SYNTHETIC_PRIVATE_ERROR_TEXT") }

type classificationTextProbe struct{ calls *int }

func (e classificationTextProbe) Error() string {
	*e.calls = *e.calls + 1
	panic("SYNTHETIC_PRIVATE_ERROR_TEXT")
}

type classificationPanicAs struct{}

func (classificationPanicAs) Error() string { panic("SYNTHETIC_PRIVATE_ERROR_TEXT") }
func (classificationPanicAs) As(any) bool   { panic("SYNTHETIC_PRIVATE_AS_PAYLOAD") }

type classificationPanicIs struct{}

func (classificationPanicIs) Error() string { panic("SYNTHETIC_PRIVATE_ERROR_TEXT") }
func (classificationPanicIs) Is(error) bool { panic("SYNTHETIC_PRIVATE_IS_PAYLOAD") }

type classificationPanicUnwrap struct{}

func (classificationPanicUnwrap) Error() string { panic("SYNTHETIC_PRIVATE_ERROR_TEXT") }
func (classificationPanicUnwrap) Unwrap() error { panic("SYNTHETIC_PRIVATE_UNWRAP_PAYLOAD") }

func TestDiagnosticDefaultFailureKinds(t *testing.T) {
	for _, tc := range diagnosticKindCases {
		t.Run(string(tc.code), func(t *testing.T) {
			d := diagnostic(tc.code)
			if d.kind != tc.kind || d.Kind() != tc.kind || FailureKind(d, failuretype.NativeWriteUnknown) != tc.kind {
				t.Fatal("closed default classification differs")
			}
			// Legacy diagnostics may have no metadata. Only their closed code
			// can provide a default; other exported fields are irrelevant.
			legacy := &Diagnostic{Code: tc.code, Stage: "cancelled", Constraint: "sqlite_busy"}
			if FailureKind(classificationWrapper{legacy}, 0) != tc.kind {
				t.Fatal("wrapped closed-code default was lost")
			}
		})
	}
	if diagnostic(CodeWork).Kind() == failuretype.Cancelled {
		t.Fatal("legacy work code fabricated an actual context cancellation")
	}
	if (&Diagnostic{Stage: "account", Constraint: "existing_account", Outcome: "expired"}).Kind() != 0 {
		t.Fatal("free-form fields supplied numeric classification")
	}
}

func TestCanonicalFailureReconstructsEveryCoreTemplate(t *testing.T) {
	for _, tc := range diagnosticKindCases {
		t.Run(string(tc.code), func(t *testing.T) {
			forged := &Diagnostic{
				Code: tc.code, Stage: "SYNTHETIC_PRIVATE_STAGE", Constraint: "SYNTHETIC_PRIVATE_CONSTRAINT",
				Outcome: "SYNTHETIC_PRIVATE_OUTCOME", NextStep: "SYNTHETIC_PRIVATE_NEXT_STEP",
			}
			before := *forged
			clean := CanonicalFailure(classificationWrapper{classificationWrapper{forged}}, failuretype.ResponseDeliveryUnknown)
			if clean == forged || *clean != *diagnostic(tc.code) || *forged != before {
				t.Fatal("opt-in canonicalization did not independently reconstruct core fields")
			}
			wire, err := json.Marshal(clean)
			if err != nil || strings.Contains(string(wire), "SYNTHETIC_PRIVATE") || strings.Contains(string(wire), "kind") {
				t.Fatal("canonical JSON leaked forged text or private metadata")
			}
			var fields map[string]json.RawMessage
			if json.Unmarshal(wire, &fields) != nil || len(fields) != 5 {
				t.Fatal("classification changed the public JSON shape")
			}
		})
	}
	if CanonicalFailure(diagnostic(CodeExpired), 0).Outcome != "expired" || CanonicalFailure(diagnostic(CodeEvidence), 0).Outcome != "unavailable" {
		t.Fatal("canonicalization erased expired/evidence contracts")
	}
}

func TestDiagnosticPrivateClassificationValidationAndPrecedence(t *testing.T) {
	for _, kind := range []failuretype.Type{0, 40, 255, failuretype.DeadlineExceeded, failuretype.SQLiteBusy} {
		d := diagnostic(CodeStorage)
		d.kind = kind
		want := kind
		if !want.Valid() {
			want = failuretype.TransactionWorkUnknown
		}
		wrapped := &callbackFailure{cause: classificationWrapper{d}}
		if FailureKind(wrapped, failuretype.TransactionBeginUnknown) != want || CanonicalFailure(wrapped, 0).Kind() != want {
			t.Fatal("private numeric metadata was lost or invalid metadata accepted")
		}
		clean := CanonicalFailure(wrapped, 0)
		if clean.Outcome != "no_effect" || clean.Stage != "transaction_work" || clean.Constraint != diagnostic(CodeStorage).Constraint {
			t.Fatal("classification changed authoritative core fields")
		}
	}
	d := diagnostic(CodeCommit)
	d.kind = failuretype.SQLiteBusy
	if FailureKind(errors.Join(d, context.Canceled), 0) != failuretype.SQLiteBusy || CanonicalFailure(d, 0).Outcome != "unknown" {
		t.Fatal("typed metadata lost precedence or invented rollback")
	}
}

func TestFailureKindNilContextUnknownAndHostileMethods(t *testing.T) {
	textCalls := 0
	probe := classificationTextProbe{calls: &textCalls}
	if FailureKind(probe, failuretype.NativeReadUnknown) != failuretype.NativeReadUnknown || CanonicalFailure(probe, failuretype.NativeReadUnknown).Kind() != failuretype.NativeReadUnknown {
		t.Fatal("unknown text probe did not retain the safe fallback")
	}
	if textCalls != 0 {
		t.Fatal("classification formatted an error even though its panic was caught")
	}
	var nilDiagnostic *Diagnostic
	var nilDriver *sqlite.Error
	if FailureKind(nil, 0) != 0 || CanonicalFailure(nil, failuretype.NativeReadUnknown) != nil || nilDiagnostic.Kind() != 0 {
		t.Fatal("nil was classified as a failure")
	}
	for _, err := range []error{
		classificationRawError{}, classificationPanicAs{}, classificationPanicIs{}, classificationPanicUnwrap{},
		classificationWrapper{classificationPanicUnwrap{}}, nilDiagnostic, classificationWrapper{nilDiagnostic}, nilDriver,
		&Diagnostic{Code: "SYNTHETIC_PRIVATE_CODE", Stage: "account", Outcome: "no_effect"}, sql.ErrNoRows,
	} {
		if FailureKind(err, failuretype.NativeReadUnknown) != failuretype.NativeReadUnknown {
			t.Fatal("unknown/hostile error did not use its safe component fallback")
		}
		clean := CanonicalFailure(err, failuretype.NativeReadUnknown)
		if clean.Code != CodeBoundary || clean.Stage != "native_read" || clean.Kind() != failuretype.NativeReadUnknown || clean.Outcome != "unknown" {
			t.Fatal("unknown/hostile canonical diagnostic invented fields or outcome")
		}
		wire, marshalErr := json.Marshal(clean)
		if marshalErr != nil || strings.Contains(string(wire), "SYNTHETIC_PRIVATE") {
			t.Fatal("unknown/hostile canonical diagnostic leaked text")
		}
		for _, invalid := range []failuretype.Type{0, 40, 255} {
			if FailureKind(err, invalid) != failuretype.TransactionCompletionUnknown || CanonicalFailure(err, invalid).Stage != "transaction_completion" {
				t.Fatal("invalid fallback escaped the conservative closed default")
			}
		}
	}
	for _, tc := range []struct {
		err  error
		kind failuretype.Type
	}{
		{context.Canceled, failuretype.Cancelled},
		{context.DeadlineExceeded, failuretype.DeadlineExceeded},
	} {
		err := classificationWrapper{tc.err}
		if FailureKind(err, failuretype.TransactionBeginUnknown) != tc.kind {
			t.Fatal("actual wrapped context cause was lost")
		}
		clean := CanonicalFailure(err, failuretype.TransactionBeginUnknown)
		if clean.Kind() != tc.kind || clean.Stage != "transaction_begin" || clean.Outcome != "unknown" {
			t.Fatal("context classification invented completion or replaced the owner's stage")
		}
	}
}

func TestCanonicalFailureClosedStageFallbacks(t *testing.T) {
	stages := []string{
		"validation", "authentication", "policy", "account", "lookup", "key", "admission", "arithmetic", "readiness",
		"transaction_work", "transaction_work", "writer_gate", "transaction_work", "transaction_work", "transaction_work",
		"state_validation", "state_validation", "reservation_identity", "transaction_begin", "transaction_work",
		"transaction_rollback", "transaction_completion", "transaction_after_completion", "init", "storage_open",
		"state_validation", "storage_close", "native_auth", "native_read", "native_write", "native_asset",
		"request_read", "request_dispatch", "response_encoding", "response_delivery", "maintenance", "runtime_setup", "runtime_serve", "runtime_shutdown",
	}
	if len(stages) != failuretype.Count {
		t.Fatal("stage fixture does not cover the fixed taxonomy")
	}
	for i, stage := range stages {
		kind := failuretype.Type(i + 1)
		clean := CanonicalFailure(classificationRawError{}, kind)
		wantCode := CodeBoundary
		if kind == failuretype.TransactionCompletionUnknown {
			wantCode = CodeCommit
		}
		if clean.Code != wantCode || clean.Stage != stage || clean.Kind() != kind || clean.Outcome != "unknown" || clean.Constraint != "authoritative_boundary_evidence" || !strings.Contains(clean.NextStep, "cause unknown") {
			t.Fatal("fallback stage/classification/outcome or cause-unknown guidance differs")
		}
	}
}

func TestCanonicalFailureRemainsOptInForLegacyInitAndRuntime(t *testing.T) {
	init := initFailure(diagnostic(CodeStorage))
	before := *init
	if Failure(init) != init || init.Stage != "init" || *init != before {
		t.Fatal("legacy init variant changed")
	}
	if CanonicalFailure(init, failuretype.InitializationUnknown).Stage != "transaction_work" || *init != before {
		t.Fatal("opt-in core reconstruction mutated a legacy init variant")
	}
	runtime := &Diagnostic{Code: "LEDGER_HTTP_SERVE_UNAVAILABLE", Stage: "http_serve", Constraint: "usable_listener", Outcome: "unknown", NextStep: "Locally authored runtime guidance."}
	before = *runtime
	if Failure(runtime) != runtime || *runtime != before {
		t.Fatal("legacy custom runtime code changed")
	}
	clean := CanonicalFailure(runtime, failuretype.RuntimeServeUnknown)
	if clean.Code != CodeBoundary || clean.Stage != "runtime_serve" || clean.Kind() != failuretype.RuntimeServeUnknown || *runtime != before {
		t.Fatal("unknown core code escaped canonical fallback or mutated its owner")
	}
}

func assertSQLiteClassification(t *testing.T, err error, primary int, kind failuretype.Type) {
	t.Helper()
	var driver *sqlite.Error
	if !errors.As(err, &driver) || driver == nil || driver.Code()&0xff != primary {
		t.Fatal("synthetic fixture did not produce the expected actual driver code")
	}
	wrapped := &callbackFailure{cause: classificationWrapper{err}}
	if FailureKind(wrapped, failuretype.NativeWriteUnknown) != kind {
		t.Fatal("supported wrapped SQLite category was lost")
	}
	clean := CanonicalFailure(wrapped, failuretype.NativeWriteUnknown)
	if clean.Kind() != kind || clean.Stage != "native_write" || clean.Outcome != "unknown" {
		t.Fatal("SQLite category invented outcome or replaced the known boundary stage")
	}
}

func TestFailureKindActualSQLiteBusyConstraintStorageAndNoRows(t *testing.T) {
	s, dir, _ := testService(t)
	locker, err := connect(filepath.Join(dir, "data.db"), false)
	if err != nil {
		t.Fatal("synthetic lock connector failed")
	}
	defer locker.Close()
	tx, err := locker.Begin()
	if err != nil {
		t.Fatal("synthetic lock begin failed")
	}
	defer tx.Rollback()
	if _, err := tx.NewQuery("UPDATE ledger_meta SET clock_high_water_ms=clock_high_water_ms WHERE singleton=1").Execute(); err != nil {
		t.Fatal("synthetic lock acquisition failed")
	}
	_, err = s.app.DB().NewQuery("UPDATE ledger_meta SET clock_high_water_ms=clock_high_water_ms WHERE singleton=1").Execute()
	assertSQLiteClassification(t, err, sqlite3.SQLITE_BUSY, failuretype.SQLiteBusy)
	if err := tx.Rollback(); err != nil {
		t.Fatal("synthetic lock release failed")
	}
	_, err = s.app.DB().NewQuery("INSERT INTO ledger_accounts(id,account_id,balance,enabled) VALUES('constraint00000','synthetic','-1',1)").Execute()
	assertSQLiteClassification(t, err, sqlite3.SQLITE_CONSTRAINT, failuretype.SQLiteConstraint)
	var constraint *sqlite.Error
	if !errors.As(err, &constraint) || constraint.Code() == sqlite3.SQLITE_CONSTRAINT {
		t.Fatal("constraint fixture did not exercise extended primary-code masking")
	}

	path := filepath.Join(t.TempDir(), "not-sqlite.db")
	if err := os.WriteFile(path, []byte("SYNTHETIC_NOT_A_DATABASE"), 0600); err != nil {
		t.Fatal("synthetic storage fixture write failed")
	}
	db, err := connect(path, false)
	if err != nil {
		t.Fatal("synthetic storage connector failed")
	}
	defer db.Close()
	_, err = db.NewQuery("SELECT 1").Execute()
	assertSQLiteClassification(t, err, sqlite3.SQLITE_NOTADB, failuretype.SQLiteStorageFailure)

	var id string
	err = s.app.DB().NewQuery("SELECT account_id FROM ledger_accounts WHERE account_id='missing'").Row(&id)
	if !errors.Is(err, sql.ErrNoRows) || FailureKind(err, failuretype.NativeReadUnknown) != failuretype.NativeReadUnknown {
		t.Fatal("SQL no-rows was given a fabricated account-missing contract")
	}
	if !s.Ready() {
		t.Fatal("classification changed service readiness")
	}
}

func TestFailureKindActualSQLiteLocked(t *testing.T) {
	db, err := connect(filepath.Join(t.TempDir(), "locked.db"), true)
	if err != nil {
		t.Fatal("synthetic locked connector failed")
	}
	defer db.Close()
	conn, err := db.DB().Conn(background)
	if err != nil {
		t.Fatal("synthetic locked connection failed")
	}
	defer conn.Close()
	if _, err := conn.ExecContext(background, "CREATE TABLE locked_fixture(value INTEGER); INSERT INTO locked_fixture VALUES(1),(2)"); err != nil {
		t.Fatal("synthetic locked table fixture failed")
	}
	rows, err := conn.QueryContext(background, "SELECT value FROM locked_fixture")
	if err != nil {
		t.Fatal("synthetic locked read failed")
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("synthetic locked read produced no live cursor")
	}
	_, err = conn.ExecContext(background, "DROP TABLE locked_fixture")
	assertSQLiteClassification(t, err, sqlite3.SQLITE_LOCKED, failuretype.SQLiteBusy)
}
