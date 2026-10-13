package ledger

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"possums-intake/internal/failuretype"

	"github.com/pocketbase/pocketbase/core"
	"modernc.org/sqlite"
)

func TestOperatorCredentialsAndProvenPolicyRefusalsClassified(t *testing.T) {
	for _, c := range []OperatorCredentials{{}, {Email: "SYNTHETIC_PRIVATE_EMAIL", Password: "SYNTHETIC_PRIVATE_PASSWORD"}, {Email: "fake@example.test", Password: "short"}} {
		validationDiagnostic(t, c.Validate(), "LEDGER_OPERATOR_SETUP_INVALID", failuretype.RequestInvalid, "operator_setup", "no_effect")
	}
	s, _, _ := testService(t)
	// Actual default native configuration is not explicit password-only setup.
	app, err := s.OperatorApp()
	validationDiagnostic(t, err, "LEDGER_OPERATOR_SETUP_INVALID", failuretype.PolicyRefused, "operator_setup", "no_effect")
	if app != nil {
		t.Fatal("unqualified operator app exposed")
	}
	c, err := s.app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
	if err != nil {
		t.Fatal("native collection unavailable")
	}
	c.AuthAlert.Enabled, c.OTP.Enabled, c.MFA.Enabled, c.OAuth2.Enabled = false, false, false, false
	c.PasswordAuth.Enabled = true
	c.PasswordAuth.IdentityFields = []string{"email"}
	if err := s.app.Save(c); err != nil {
		t.Fatal("synthetic password-only fixture unavailable")
	}
	// Successful actual CountRecords proves zero, rather than a failed read.
	app, err = s.OperatorApp()
	d := validationDiagnostic(t, err, "LEDGER_OPERATOR_SETUP_INVALID", failuretype.PolicyRefused, "operator_setup", "no_effect")
	if app != nil || d.Constraint != "one_explicitly_initialized_native_superuser" {
		t.Fatal("successful native count refusal changed")
	}
	if err := s.run(background, func(tx *transaction) error {
		return tx.exec("DELETE FROM _collections WHERE name='_superusers'", nil)
	}, false); err != nil {
		t.Fatal("synthetic missing native collection fixture unavailable")
	}
	app, err = s.OperatorApp()
	d = validationDiagnostic(t, err, "LEDGER_OPERATOR_SETUP_INVALID", failuretype.PolicyRefused, "operator_setup", "no_effect")
	if app != nil || d.Constraint != "explicit_password_only_initialization_required" {
		t.Fatal("required native collection absence changed")
	}
}

func TestOperatorReadFaultsRetainOnlyNumericCause(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "operator")
	credentials := OperatorCredentials{Email: "fake@example.test", Password: "synthetic-only-password"}
	if err := InitOperator(background, dir, credentials); err != nil {
		t.Fatal("explicit synthetic operator initialization unavailable")
	}
	s, err := Open(background, dir)
	if err != nil {
		t.Fatal("synthetic operator reopen unavailable")
	}
	defer s.Close()
	base := s.app
	defer func() { s.app = base }()
	app, err := s.OperatorApp()
	if err != nil || app != base {
		t.Fatal("operator success no longer returns the same app")
	}
	var value int
	_, driverErr := base.DB().NewQuery("SELECT FROM").Execute()
	// The driver category is known from an actual query; method injection below
	// is a source seam, not a physical failure of the superuser lookup itself.
	if FailureKind(driverErr, failuretype.NativeReadUnknown) != failuretype.NativeReadUnknown {
		t.Fatal("synthetic unknown driver fault unavailable")
	}
	physical, err := connect(filepath.Join(t.TempDir(), "absent.db"), false)
	if err != nil {
		t.Fatal("synthetic readonly-missing connector unavailable")
	}
	defer physical.Close()
	storageErr := physical.NewQuery("SELECT 1").Row(&value)
	if FailureKind(storageErr, failuretype.NativeReadUnknown) != failuretype.SQLiteStorageFailure {
		t.Fatal("synthetic actual storage fault unavailable")
	}
	calls := 0
	for _, cause := range []error{
		classificationTextProbe{calls: &calls}, errors.New("SYNTHETIC_PRIVATE_SECRET_ERROR"), classificationPanicAs{}, classificationPanicIs{}, classificationPanicUnwrap{},
		(*Diagnostic)(nil), (*sqlite.Error)(nil), sql.ErrNoRows,
		classificationWrapper{context.Canceled}, context.DeadlineExceeded, driverErr, storageErr,
		&Diagnostic{Code: CodeStorage, Stage: "SYNTHETIC_PRIVATE_STAGE", Constraint: "SYNTHETIC_PRIVATE_CONSTRAINT", Outcome: "SYNTHETIC_PRIVATE_OUTCOME", NextStep: "SYNTHETIC_PRIVATE_NEXT_STEP", kind: failuretype.SQLiteBusy},
	} {
		for _, read := range []string{"collection", "count"} {
			hit := false
			wrapped := &validationReadApp{App: base}
			if read == "collection" {
				wrapped.find = func(string) (*core.Collection, error) { hit = true; return nil, cause }
			} else {
				wrapped.count = func(any) (int64, error) { hit = true; return 1, cause }
			}
			s.app = wrapped
			app, err := s.OperatorApp()
			if read == "collection" && cause == sql.ErrNoRows {
				validationDiagnostic(t, err, "LEDGER_OPERATOR_SETUP_INVALID", failuretype.PolicyRefused, "operator_setup", "no_effect")
			} else {
				validationDiagnostic(t, err, CodeBoundary, FailureKind(cause, failuretype.NativeReadUnknown), "native_read", "unavailable")
			}
			if !hit || app != nil || !s.Ready() {
				t.Fatal("native read fault exposed app or changed readiness")
			}
		}
	}
	if calls != 0 {
		t.Fatal("native classification formatted hostile error text")
	}
	s.app = base
	app, err = s.OperatorApp()
	if err != nil || app != base {
		t.Fatal("read faults changed native setup")
	}
	count, err := base.CountRecords(core.CollectionNameSuperusers)
	if err != nil || count != 1 {
		t.Fatal("read-only operator boundary mutated credentials")
	}
}
