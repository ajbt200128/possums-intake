package ledger

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"modernc.org/sqlite"
	"possums-intake/internal/failuretype"
)

func TestOperatorCredentialsAndNativeSetupWithoutAuthorityRestrictions(t *testing.T) {
	for _, c := range []OperatorCredentials{{}, {Email: "SYNTHETIC_PRIVATE_EMAIL", Password: "SYNTHETIC_PRIVATE_PASSWORD"}, {Email: "fake@example.test", Password: "short"}} {
		validationDiagnostic(t, c.Validate(), "LEDGER_OPERATOR_SETUP_INVALID", failuretype.RequestInvalid, "operator_setup", "no_effect")
	}
	s, _, _ := testService(t)
	// No new account is created on serve, even with zero native superusers.
	app, err := s.OperatorApp()
	if err != nil || app != s.app {
		t.Fatal("native default authority policy refused")
	}
	count, err := app.CountRecords("_superusers")
	if err != nil || count != 0 {
		t.Fatal("operator access created credentials")
	}
	c, err := app.FindCollectionByNameOrId("_superusers")
	if err != nil {
		t.Fatal("native collection unavailable")
	}
	c.OTP.Enabled = true
	c.MFA.Enabled = true
	if app.Save(c) != nil {
		t.Fatal("native auth configuration unavailable")
	}
	if _, err = s.OperatorApp(); err != nil {
		t.Fatal("native multi-factor configuration blocked access")
	}
	if err := s.run(background, func(tx *transaction) error { return tx.exec("DELETE FROM _collections WHERE name='_superusers'", nil) }, false); err != nil {
		t.Fatal("missing collection fixture unavailable")
	}
	app, err = s.OperatorApp()
	d := validationDiagnostic(t, err, "LEDGER_OPERATOR_SETUP_INVALID", failuretype.PolicyRefused, "operator_setup", "no_effect")
	if app != nil || d.Constraint != "native_superuser_collection_required" {
		t.Fatal("missing native collection classification changed")
	}
}

func TestOperatorReadFaultsRetainOnlyNumericCause(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "operator")
	credentials := OperatorCredentials{Email: "fake@example.test", Password: "synthetic-only-password"}
	if InitOperator(background, dir, credentials) != nil {
		t.Fatal("operator init unavailable")
	}
	s, err := Open(background, dir)
	if err != nil {
		t.Fatal("operator reopen unavailable")
	}
	defer s.Close()
	base := s.app
	defer func() { s.app = base }()
	if app, err := s.OperatorApp(); err != nil || app != base {
		t.Fatal("operator did not return same app")
	}
	var value int
	_, driverErr := base.DB().NewQuery("SELECT FROM").Execute()
	physical, err := connect(filepath.Join(t.TempDir(), "absent.db"), false)
	if err != nil {
		t.Fatal("readonly connector unavailable")
	}
	defer physical.Close()
	storageErr := physical.NewQuery("SELECT 1").Row(&value)
	if FailureKind(storageErr, failuretype.NativeReadUnknown) != failuretype.SQLiteStorageFailure {
		t.Fatal("storage fault unavailable")
	}
	calls := 0
	for _, cause := range []error{classificationTextProbe{calls: &calls}, errors.New("SYNTHETIC_PRIVATE_SECRET_ERROR"), classificationPanicAs{}, classificationPanicIs{}, classificationPanicUnwrap{}, (*Diagnostic)(nil), (*sqlite.Error)(nil), sql.ErrNoRows, classificationWrapper{context.Canceled}, context.DeadlineExceeded, driverErr, storageErr, &Diagnostic{Code: CodeStorage, Stage: "SYNTHETIC_PRIVATE_STAGE", Constraint: "SYNTHETIC_PRIVATE_CONSTRAINT", Outcome: "SYNTHETIC_PRIVATE_OUTCOME", NextStep: "SYNTHETIC_PRIVATE_NEXT_STEP", kind: failuretype.SQLiteBusy}} {
		hit := false
		wrapped := &validationReadApp{App: base, find: func(string) (*core.Collection, error) { hit = true; return nil, cause }}
		s.app = wrapped
		app, err := s.OperatorApp()
		if cause == sql.ErrNoRows {
			validationDiagnostic(t, err, "LEDGER_OPERATOR_SETUP_INVALID", failuretype.PolicyRefused, "operator_setup", "no_effect")
		} else {
			validationDiagnostic(t, err, CodeBoundary, FailureKind(cause, failuretype.NativeReadUnknown), "native_read", "unavailable")
		}
		if !hit || app != nil || !s.Ready() {
			t.Fatal("read fault exposed app or changed readiness")
		}
	}
	if calls != 0 {
		t.Fatal("classification formatted hostile text")
	}
	// The obsolete one-superuser count gate must not even perform a count.
	s.app = &validationReadApp{App: base, count: func(any) (int64, error) { t.Fatal("obsolete superuser count gate executed"); return 0, nil }}
	if _, err = s.OperatorApp(); err != nil {
		t.Fatal("removed authority gate remains")
	}
	s.app = base
	count, err := base.CountRecords("_superusers")
	if err != nil || count != 1 {
		t.Fatal("operator boundary changed credentials")
	}
}
