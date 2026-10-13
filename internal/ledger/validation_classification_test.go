package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"possums-intake/internal/failuretype"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"modernc.org/sqlite"
)

// Supported app/query seams route only a selected read. RunInTransaction and
// completion/rollback still use the actual pinned PocketBase implementation.
// Injected LastError/method results are source seams, not physical driver faults.
type validationReadApp struct {
	core.App
	queryHook func(string, *dbx.Query) *dbx.Query
	find      func(string) (*core.Collection, error)
	count     func(any) (int64, error)
}

type validationReadBuilder struct {
	dbx.Builder
	hook func(string, *dbx.Query) *dbx.Query
}

func (b validationReadBuilder) NewQuery(statement string) *dbx.Query {
	q := b.Builder.NewQuery(statement)
	if b.hook != nil {
		return b.hook(statement, q)
	}
	return q
}

func (a *validationReadApp) DB() dbx.Builder {
	return validationReadBuilder{a.App.DB(), a.queryHook}
}
func (a *validationReadApp) RunInTransaction(fn func(core.App) error) error {
	return a.App.RunInTransaction(func(txApp core.App) error {
		clone := *a
		clone.App = txApp
		return fn(&clone)
	})
}
func (a *validationReadApp) FindCollectionByNameOrId(id string) (*core.Collection, error) {
	if a.find != nil {
		return a.find(id)
	}
	return a.App.FindCollectionByNameOrId(id)
}
func (a *validationReadApp) CountRecords(c any, exprs ...dbx.Expression) (int64, error) {
	if a.count != nil {
		return a.count(c)
	}
	return a.App.CountRecords(c, exprs...)
}

func validationDiagnostic(t *testing.T, err error, code Code, kind failuretype.Type, stage, outcome string) *Diagnostic {
	t.Helper()
	d, ok := err.(*Diagnostic)
	if !ok || d == nil || d.Code != code || d.Kind() != kind || d.Stage != stage || d.Outcome != outcome {
		t.Fatal("validation boundary classification or authoritative outcome differs")
	}
	wire, marshalErr := json.Marshal(d)
	if marshalErr != nil || strings.Contains(string(wire), "SYNTHETIC_PRIVATE") || strings.Contains(string(wire), "kind") || !strings.Contains(d.NextStep, "unknown") && code == CodeBoundary {
		t.Fatal("diagnostic leaked source fields or lacks closed unknown-cause guidance")
	}
	if errors.Unwrap(d) != nil {
		t.Fatal("diagnostic retained raw cause")
	}
	return d
}

func validationDatabase(t *testing.T) *dbx.DB {
	t.Helper()
	db, err := connect(filepath.Join(t.TempDir(), "validation.db"), true)
	if err != nil {
		t.Fatal("synthetic database connector unavailable")
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.NewQuery(schemaV1).Execute(); err != nil {
		t.Fatal("synthetic exact DDL unavailable")
	}
	return db
}

func TestValidationDDLReadFaultsAndRequiredAbsence(t *testing.T) {
	db := validationDatabase(t)
	query := func(statement string, params dbx.Params) *dbx.Query {
		return db.NewQuery(statement).Bind(params)
	}
	if err := validateDDL(query); err != nil {
		t.Fatal("exact DDL rejected")
	}
	for _, row := range []bool{false, true} {
		for _, cause := range []error{classificationRawError{}, classificationPanicAs{}, errors.New("SYNTHETIC_PRIVATE_SECRET_ERROR"), sql.ErrNoRows, (*Diagnostic)(nil), (*sqlite.Error)(nil), &Diagnostic{Code: CodeStorage, Stage: "SYNTHETIC_PRIVATE_STAGE", Constraint: "SYNTHETIC_PRIVATE_CONSTRAINT", Outcome: "SYNTHETIC_PRIVATE_OUTCOME", NextStep: "SYNTHETIC_PRIVATE_NEXT_STEP"}} {
			queryFault := func(statement string, params dbx.Params) *dbx.Query {
				q := query(statement, params)
				if strings.HasPrefix(statement, "SELECT sql") == row {
					q.LastError = cause
				}
				return q
			}
			err := validateDDL(queryFault)
			if row && cause == sql.ErrNoRows {
				validationDiagnostic(t, err, CodeSchema, failuretype.SchemaRejected, "state_validation", "unavailable")
				continue
			}
			validationDiagnostic(t, err, CodeBoundary, FailureKind(cause, failuretype.StorageValidationUnknown), "state_validation", "unavailable")
		}
		ctx, cancel := context.WithCancel(background)
		cancel()
		err := validateDDL(func(statement string, params dbx.Params) *dbx.Query {
			q := query(statement, params)
			if strings.HasPrefix(statement, "SELECT sql") == row {
				q.WithContext(ctx)
			}
			return q
		})
		validationDiagnostic(t, err, CodeBoundary, failuretype.Cancelled, "state_validation", "unavailable")
	}
	// Nine successful objects do not prove that a particular required row exists.
	missing, err := connect(filepath.Join(t.TempDir(), "missing.db"), true)
	if err != nil {
		t.Fatal("synthetic absence database unavailable")
	}
	defer missing.Close()
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		if _, err := missing.NewQuery("CREATE TABLE ledger_" + name + " (value TEXT)").Execute(); err != nil {
			t.Fatal("synthetic absence fixture unavailable")
		}
	}
	err = validateDDL(func(statement string, params dbx.Params) *dbx.Query { return missing.NewQuery(statement).Bind(params) })
	validationDiagnostic(t, err, CodeSchema, failuretype.SchemaRejected, "state_validation", "unavailable")
	if _, err := db.NewQuery("DROP INDEX ledger_credits_prune; CREATE INDEX ledger_credits_prune ON ledger_credits(committed_at)").Execute(); err != nil {
		t.Fatal("synthetic DDL mismatch unavailable")
	}
	validationDiagnostic(t, validateDDL(query), CodeSchema, failuretype.SchemaRejected, "state_validation", "unavailable")
}

// The SDK must marshal field metadata before comparison. Unsupported numeric
// metadata is a real marshal failure, not a successfully observed mismatch.
type invalidValidationMetadata struct {
	*core.TextField
	Value float64 `json:"value"`
}

func TestValidationCollectionFaultsAndMarshalFailure(t *testing.T) {
	s, _, _ := testService(t)
	base := s.app
	defer func() { s.app = base }()
	calls := 0
	for _, cause := range []error{classificationTextProbe{calls: &calls}, classificationPanicAs{}, (*Diagnostic)(nil), (*sqlite.Error)(nil), sql.ErrNoRows, classificationWrapper{context.Canceled}, context.DeadlineExceeded, &Diagnostic{Code: CodeStorage, Stage: "SYNTHETIC_PRIVATE_STAGE", Outcome: "SYNTHETIC_PRIVATE_OUTCOME", NextStep: "SYNTHETIC_PRIVATE_SECRET"}} {
		s.app = &validationReadApp{App: base, find: func(string) (*core.Collection, error) { return nil, cause }}
		// This scopes the report to one run, never to Init/Open's multiple runs.
		ctx, report := WithFailureReport(background)
		err := s.validate(ctx)
		kind := FailureKind(cause, failuretype.StorageValidationUnknown)
		code, stage, outcome := CodeStorage, "transaction_work", "no_effect"
		if cause == sql.ErrNoRows {
			kind, code, stage, outcome = failuretype.SchemaRejected, CodeSchema, "state_validation", "unavailable"
		}
		validationDiagnostic(t, err, code, kind, stage, outcome)
		kinds, count, valid := report.Take()
		if !valid || count != 1 || kinds[0] != kind {
			t.Fatal("collection failure lost once-only numeric evidence")
		}
		// Successful missing metadata retains the original fail-closed policy;
		// the other read failures must not pretend they proved schema rejection.
		if s.Ready() == (cause == sql.ErrNoRows) {
			t.Fatal("collection read changed mismatch readiness policy")
		}
		s.ready.Store(true) // reset only this synthetic schema-refusal fixture
	}
	if calls != 0 {
		t.Fatal("collection classification formatted hostile source text")
	}
	s.app = &validationReadApp{App: base, find: func(id string) (*core.Collection, error) {
		c, err := base.FindCollectionByNameOrId(id)
		if err == nil {
			c.Fields = append(c.Fields, &invalidValidationMetadata{TextField: &core.TextField{Name: "SYNTHETIC_PRIVATE_FIELD"}, Value: math.NaN()})
		}
		return c, err
	}}
	validationDiagnostic(t, s.validate(background), CodeStorage, failuretype.StorageValidationUnknown, "transaction_work", "no_effect")
	if !s.Ready() {
		t.Fatal("metadata marshal failure asserted schema rejection")
	}
}

func TestValidationSuccessfulCollectionMismatchAndAbsence(t *testing.T) {
	for _, statement := range []string{
		"UPDATE _collections SET fields='[]' WHERE id='ledgeraccounts1'",
		"DELETE FROM _collections WHERE id='ledgeraccounts1'",
	} {
		t.Run(statement, func(t *testing.T) {
			s, _, _ := testService(t)
			if err := s.run(background, func(tx *transaction) error { return tx.exec(statement, nil) }, false); err != nil {
				t.Fatal("synthetic persisted metadata mutation unavailable")
			}
			validationDiagnostic(t, s.validate(background), CodeSchema, failuretype.SchemaRejected, "state_validation", "unavailable")
			if s.Ready() {
				t.Fatal("successful metadata refusal no longer fails closed")
			}
		})
	}
	s, _, _ := testService(t)
	if err := s.app.Save(core.NewBaseCollection("ledger_extra")); err != nil {
		t.Fatal("synthetic extra collection unavailable")
	}
	// Isolate the collection-count gate; full validation also rejects its DDL.
	err := s.run(background, validateAccountCollection, false)
	validationDiagnostic(t, err, CodeSchema, failuretype.SchemaRejected, "state_validation", "unavailable")
}

func TestValidationMalformedStoredMetadataIsReadFailureNotMismatch(t *testing.T) {
	s, _, _ := testService(t)
	if err := s.run(background, func(tx *transaction) error {
		return tx.exec("UPDATE _collections SET fields='SYNTHETIC_PRIVATE_INVALID_JSON' WHERE id='ledgeraccounts1'", nil)
	}, false); err != nil {
		t.Fatal("synthetic stored decoding fixture unavailable")
	}
	validationDiagnostic(t, s.validate(background), CodeStorage, failuretype.StorageValidationUnknown, "transaction_work", "no_effect")
}

func TestValidationUnrelatedCountNoRowsIsNotSchemaOrAccountAbsence(t *testing.T) {
	s, _, _ := testService(t)
	base := s.app
	defer func() { s.app = base }()
	s.app = &validationReadApp{App: base, queryHook: func(statement string, q *dbx.Query) *dbx.Query {
		if strings.HasPrefix(statement, "SELECT count(*) FROM _collections") {
			q.LastError = sql.ErrNoRows
		}
		return q
	}}
	validationDiagnostic(t, s.validate(background), CodeStorage, failuretype.StorageValidationUnknown, "transaction_work", "no_effect")
}

func TestValidationDDLActualSQLiteBusy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy.db")
	// A disposable rollback-journal fixture allows a real exclusive read lock;
	// the production WAL/DSN and budgets are unchanged.
	db, err := dbx.Open("sqlite", path)
	if err != nil {
		t.Fatal("synthetic lock database unavailable")
	}
	defer db.Close()
	db.DB().SetMaxOpenConns(1)
	if _, err := db.NewQuery(schemaV1).Execute(); err != nil {
		t.Fatal("synthetic lock schema unavailable")
	}
	if _, err := db.NewQuery("BEGIN EXCLUSIVE").Execute(); err != nil {
		t.Fatal("synthetic exclusive lock unavailable")
	}
	defer db.NewQuery("ROLLBACK").Execute()
	reader, err := dbx.Open("sqlite", path+"?_pragma=busy_timeout(250)")
	if err != nil {
		t.Fatal("synthetic read connector unavailable")
	}
	defer reader.Close()
	err = validateDDL(func(statement string, params dbx.Params) *dbx.Query { return reader.NewQuery(statement).Bind(params) })
	validationDiagnostic(t, err, CodeBoundary, failuretype.SQLiteBusy, "state_validation", "unavailable")
}

func TestValidationPreflightActualStorageAndContextFaults(t *testing.T) {
	dir := t.TempDir()
	// dbx.Open is lazy: the actual readonly CANTOPEN occurs at the first query,
	// so its locally known stage is validation, not connection establishment.
	validationDiagnostic(t, preflightLayout(background, dir), CodeBoundary, failuretype.SQLiteStorageFailure, "state_validation", "unavailable")
	if err := os.WriteFile(filepath.Join(dir, "data.db"), []byte("SYNTHETIC_PRIVATE_NOT_SQLITE"), 0600); err != nil {
		t.Fatal("synthetic physical storage fixture unavailable")
	}
	validationDiagnostic(t, preflightLayout(background, dir), CodeBoundary, failuretype.SQLiteStorageFailure, "state_validation", "unavailable")
	ctx, cancel := context.WithCancel(background)
	cancel()
	validationDiagnostic(t, preflightLayout(ctx, dir), CodeBoundary, failuretype.Cancelled, "state_validation", "unavailable")
}

func TestValidationOpenPhysicalReadFaultExposesNoService(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"data.db", "auxiliary.db"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("SYNTHETIC_PRIVATE_NOT_SQLITE"), 0600); err != nil {
			t.Fatal("synthetic physical startup fixture unavailable")
		}
	}
	s, err := Open(background, dir)
	validationDiagnostic(t, err, CodeBoundary, failuretype.SQLiteStorageFailure, "state_validation", "unavailable")
	if s != nil {
		t.Fatal("failed startup exposed a service")
	}
	data, err := os.ReadFile(filepath.Join(dir, "data.db"))
	if err != nil || string(data) != "SYNTHETIC_PRIVATE_NOT_SQLITE" {
		t.Fatal("failed startup repaired or reset storage")
	}
}

func TestValidationReadsPreserveObservedRollbackAndNumericMetadata(t *testing.T) {
	s, _, now := testService(t)
	fund(t, s, "synthetic", "10", now)
	base := s.app
	defer func() { s.app = base }()
	cancelled, cancel := context.WithCancel(background)
	cancel()
	deadline, cancelDeadline := context.WithDeadline(background, time.Unix(1, 0))
	defer cancelDeadline()
	for _, target := range []string{
		"PRAGMA journal_mode", "PRAGMA synchronous", "PRAGMA foreign_keys", "PRAGMA busy_timeout", "PRAGMA temp_store",
		"PRAGMA foreign_key_check", "PRAGMA quick_check",
		"SELECT count(*) FROM sqlite_schema WHERE (name LIKE 'ledger_%' OR tbl_name LIKE 'ledger_%') AND sql IS NOT NULL", "SELECT sql FROM sqlite_schema WHERE name={:name}",
		"SELECT count(*) FROM _collections WHERE name LIKE 'ledger_%'", "SELECT count(*) FROM ledger_meta",
		"SELECT schema_version,clock_high_water_ms FROM ledger_meta WHERE singleton=1",
		"SELECT CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)",
		"SELECT account_id,balance,enabled FROM ledger_accounts ORDER BY account_id",
		"SELECT " + reservationColumns + " FROM ledger_reservations WHERE account_id={:id} AND state='active' ORDER BY reservation_id",
		"SELECT " + reservationColumns + " FROM ledger_reservations",
		"SELECT " + creditColumns + " FROM ledger_credits",
	} {
		for _, tc := range []struct {
			name string
			ctx  context.Context
			kind failuretype.Type
		}{{"cancelled", cancelled, failuretype.Cancelled}, {"deadline", deadline, failuretype.DeadlineExceeded}, {"sqlite_query", nil, failuretype.StorageValidationUnknown}} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				hit := false
				s.app = &validationReadApp{App: base, queryHook: func(statement string, q *dbx.Query) *dbx.Query {
					if !hit && statement == target {
						hit = true
						q.WithExecHook(func(q *dbx.Query, op func() error) error {
							if tc.ctx != nil {
								q.WithContext(tc.ctx)
								return op()
							}
							// An actual pinned-driver syntax fault, routed through a
							// test query hook; not an induced physical disk outage.
							var value int
							return base.DB().NewQuery("SELECT FROM").Row(&value)
						})
					}
					return q
				}}
				ctx, report := WithFailureReport(background) // exactly one validate/run
				err := s.validate(ctx)
				code := CodeStorage
				// Boundary fallbacks from the shared preflight validator and
				// clock/active helpers let core select its context-work template.
				if tc.ctx != nil && (strings.Contains(target, "sqlite_schema") || strings.Contains(target, "ledger_meta") || strings.HasPrefix(target, "SELECT CAST") || strings.Contains(target, "WHERE account_id=")) {
					code = CodeWork
				}
				validationDiagnostic(t, err, code, tc.kind, "transaction_work", "no_effect")
				kinds, count, valid := report.Take()
				if !hit || !valid || count != 1 || kinds[0] != tc.kind || !s.Ready() {
					t.Fatal("read fault lost once-only classification or changed readiness policy")
				}
				s.app = base
				if err := s.validate(background); err != nil {
					t.Fatal("read fault altered valid persisted state")
				}
			})
		}
	}
}
