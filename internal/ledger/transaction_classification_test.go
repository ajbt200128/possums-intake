package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"possums-intake/internal/failuretype"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type completionSignalApp struct {
	core.App
	completed chan struct{}
}

func (a completionSignalApp) RunInTransaction(fn func(core.App) error) error {
	err := a.App.RunInTransaction(fn)
	close(a.completed)
	return err
}

type completionFaultApp struct {
	core.App
	panicAfter bool
}

func (a completionFaultApp) RunInTransaction(fn func(core.App) error) error {
	if err := a.App.RunInTransaction(fn); err != nil {
		return err
	}
	if a.panicAfter {
		panic(nonComparableCallbackFailure{1})
	}
	return nonComparableCallbackFailure{1} // Error() panics if project code formats it
}

type panicBeginApp struct{ core.App }

func (panicBeginApp) RunInTransaction(func(core.App) error) error {
	panic(nonComparableCallbackFailure{1})
}

func TestTransactionClassifiedBeginPanicPreservesPublicResult(t *testing.T) {
	s, _, _ := testService(t)
	original := s.app
	s.app = panicBeginApp{original}
	defer func() { s.app = original }()
	ctx, report := WithFailureReport(background)
	err := s.run(ctx, func(*transaction) error {
		t.Fatal("begin panic entered accounting callback")
		return nil
	}, false)
	kindIs(t, err, CodeCommit, failuretype.TransactionBeginUnknown)
	if s.Ready() || Failure(err).Outcome != "unknown" {
		t.Fatal("public panic result or readiness policy changed")
	}
	takeKinds(t, report, failuretype.TransactionBeginUnknown)
}

type completionPropagationApp struct {
	core.App
	join bool
}

func (a completionPropagationApp) RunInTransaction(fn func(core.App) error) error {
	err := a.App.RunInTransaction(fn)
	if err == nil {
		return nil
	}
	if a.join {
		return errors.Join(err)
	}
	return fmt.Errorf("synthetic propagation: %w", err)
}

// These are actual pinned PocketBase OnComplete callbacks after our observer,
// not synthetic driver rollback failures. PocketBase itself formats returned
// hook errors in core/db_tx.go; only the project-owned boundary is content-free.
type completionHooksApp struct {
	core.App
	count      int
	panicAfter bool
}

func (a completionHooksApp) RunInTransaction(fn func(core.App) error) error {
	return a.App.RunInTransaction(func(tx core.App) error {
		err := fn(tx)
		for range a.count {
			tx.TxInfo().OnComplete(func(error) error {
				return errors.New("SYNTHETIC_PRIVATE_AFTER_HOOK")
			})
		}
		if a.panicAfter {
			tx.TxInfo().OnComplete(func(error) error { panic("SYNTHETIC_PRIVATE_HOOK_PANIC") })
		}
		return err
	})
}

func kindIs(t *testing.T, err error, code Code, kind failuretype.Type) {
	t.Helper()
	codeIs(t, err, code)
	if Failure(err).Kind() != kind {
		t.Fatalf("expected kind %d, got %d", kind, Failure(err).Kind())
	}
}

func TestTransactionClassifiedWriterRefusals(t *testing.T) {
	s, _, _ := testService(t)
	s.gate <- struct{}{}
	defer func() { <-s.gate }()
	ctx, cancel := context.WithCancel(background)
	cancel()
	ctx, report := WithFailureReport(ctx)
	_, err := s.Provision(ctx, "cancelled-writer")
	kindIs(t, err, CodeWriter, failuretype.Cancelled)
	takeKinds(t, report, failuretype.Cancelled)
	ctx, cancel = context.WithTimeout(background, 10*time.Millisecond)
	defer cancel()
	ctx, report = WithFailureReport(ctx)
	_, err = s.Provision(ctx, "deadline-writer")
	kindIs(t, err, CodeWriter, failuretype.DeadlineExceeded)
	takeKinds(t, report, failuretype.DeadlineExceeded)
	ctx, report = WithFailureReport(background)
	_, err = s.Provision(ctx, "exhausted-writer")
	kindIs(t, err, CodeWriter, failuretype.WriterWaitExhausted)
	takeKinds(t, report, failuretype.WriterWaitExhausted)
	if !s.Ready() {
		t.Fatal("writer refusal changed readiness")
	}
}

func TestTransactionClassifiedKnownContextBudgetCauses(t *testing.T) {
	s, _, _ := testService(t)
	ctx, cancel := context.WithCancel(background)
	ctx, report := WithFailureReport(ctx)
	err := s.run(ctx, func(tx *transaction) error {
		if err := tx.exec("INSERT INTO ledger_accounts(id,account_id,balance,enabled) VALUES('reportcancel000','report-cancel','0',1)", nil); err != nil {
			return err
		}
		cancel()
		return nil
	}, false)
	kindIs(t, err, CodeWork, failuretype.Cancelled)
	takeKinds(t, report, failuretype.Cancelled)
	for _, callerBudget := range []bool{true, false} {
		ctx = background
		if callerBudget {
			ctx, cancel = context.WithTimeout(ctx, 15*time.Millisecond)
			defer cancel()
		}
		ctx, report = WithFailureReport(ctx)
		err = s.run(ctx, func(tx *transaction) error { <-tx.ctx.Done(); return nil }, false)
		kindIs(t, err, CodeWork, failuretype.DeadlineExceeded)
		takeKinds(t, report, failuretype.DeadlineExceeded)
	}
	_, err = s.Balance(background, "report-cancel")
	codeIs(t, err, CodeAccount)
	if !s.Ready() {
		t.Fatal("observed budget rollback changed readiness")
	}
}

func actualFatalError(t *testing.T) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "invalid.db")
	if err := os.WriteFile(path, []byte("SYNTHETIC_INVALID_SQLITE"), 0600); err != nil {
		t.Fatal("fixture write failed")
	}
	db, err := connect(path, false)
	if err != nil {
		t.Fatal("fixture connect failed")
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.NewQuery("SELECT 1").Execute()
	if FailureKind(err, failuretype.TransactionWorkUnknown) != failuretype.SQLiteStorageFailure {
		t.Fatal("fixture did not produce actual typed storage failure")
	}
	return err
}

func TestTransactionClassifiedActualSQLiteWork(t *testing.T) {
	t.Run("constraint", func(t *testing.T) {
		s, _, _ := testService(t)
		ctx, report := WithFailureReport(background)
		err := s.run(ctx, func(tx *transaction) error {
			return tx.exec("INSERT INTO ledger_meta(singleton,schema_version,clock_high_water_ms) VALUES(1,1,0)", nil)
		}, false)
		kindIs(t, err, CodeStorage, failuretype.SQLiteConstraint)
		takeKinds(t, report, failuretype.SQLiteConstraint)
		if !s.Ready() {
			t.Fatal("constraint rollback cleared readiness")
		}
	})
	t.Run("busy", func(t *testing.T) {
		s, dir, _ := testService(t)
		db, err := connect(filepath.Join(dir, "data.db"), false)
		if err != nil {
			t.Fatal("fixture connect failed")
		}
		defer db.Close()
		lock, err := db.Begin()
		if err != nil {
			t.Fatal("fixture begin failed")
		}
		defer lock.Rollback()
		if _, err := lock.NewQuery("UPDATE ledger_meta SET clock_high_water_ms=clock_high_water_ms").Execute(); err != nil {
			t.Fatal("fixture lock failed")
		}
		ctx, report := WithFailureReport(background)
		err = s.run(ctx, func(tx *transaction) error {
			return tx.exec("UPDATE ledger_meta SET clock_high_water_ms=clock_high_water_ms", nil)
		}, false)
		kindIs(t, err, CodeStorage, failuretype.SQLiteBusy)
		takeKinds(t, report, failuretype.SQLiteBusy)
		if !s.Ready() {
			t.Fatal("busy rollback cleared readiness")
		}
	})
	t.Run("storage", func(t *testing.T) {
		s, _, _ := testService(t)
		cause := actualFatalError(t)
		ctx, report := WithFailureReport(background)
		err := s.run(ctx, func(*transaction) error { return cause }, false)
		kindIs(t, err, CodeStorage, failuretype.SQLiteStorageFailure)
		takeKinds(t, report, failuretype.SQLiteStorageFailure)
		if s.Ready() {
			t.Fatal("fatal storage failure retained readiness")
		}
	})
}

func TestTransactionClassifiedBeginWorkAndCompletionUnknowns(t *testing.T) {
	s, _, _ := testService(t)
	original := s.app
	s.app = failedBegin{original, nonComparableCallbackFailure{1}}
	ctx, report := WithFailureReport(background)
	_, err := s.Provision(ctx, "not-begun")
	kindIs(t, err, CodeBegin, failuretype.TransactionBeginUnknown)
	takeKinds(t, report, failuretype.TransactionBeginUnknown)
	s.app = original
	ctx, report = WithFailureReport(background)
	err = s.run(ctx, func(*transaction) error { return nonComparableCallbackFailure{1} }, false)
	kindIs(t, err, CodeStorage, failuretype.TransactionWorkUnknown)
	takeKinds(t, report, failuretype.TransactionWorkUnknown)
	if !s.Ready() {
		t.Fatal("observed noncomparable rollback cleared readiness")
	}
	// A lost observer yields one completion failure, not inferred rollback or
	// two counts for an observed callback and its wrapper propagation.
	s.app = lostCompletionApp{original}
	ctx, report = WithFailureReport(background)
	err = s.run(ctx, func(*transaction) error { return diagnostic(CodeInput) }, false)
	kindIs(t, err, CodeCommit, failuretype.TransactionCompletionUnknown)
	takeKinds(t, report, failuretype.TransactionCompletionUnknown)
	s.app = original
	ctx, report = WithFailureReport(background)
	err = s.run(ctx, func(*transaction) error { panic(nonComparableCallbackFailure{1}) }, false)
	kindIs(t, err, CodeCommit, failuretype.TransactionCompletionUnknown)
	takeKinds(t, report, failuretype.TransactionCompletionUnknown)
	if s.Ready() {
		t.Fatal("unobserved panic completion retained readiness")
	}
}

func TestTransactionReportCoreRefusalCanonicalAndOnceOnly(t *testing.T) {
	s, _, _ := testService(t)
	for _, code := range []Code{CodeInput, CodeAccount, CodeConflict, CodeDisabled, CodeInsufficient, CodeOverflow, CodeExpired, CodeEvidence, CodeSchema, CodeCorrupt} {
		// Restore synthetic service readiness between deliberate state refusals.
		s.ready.Store(true)
		ctx, report := WithFailureReport(background)
		d := diagnostic(code)
		d.Stage = "SYNTHETIC_PRIVATE_STAGE"
		d.Constraint = "SYNTHETIC_PRIVATE_CONSTRAINT"
		d.NextStep = "SYNTHETIC_PRIVATE_NEXT_STEP"
		err := s.run(ctx, func(*transaction) error { return d }, false)
		kindIs(t, err, code, d.Kind())
		takeKinds(t, report, d.Kind())
		wire, marshalErr := json.Marshal(err)
		if marshalErr != nil || strings.Contains(string(wire), "SYNTHETIC_PRIVATE") || strings.Contains(string(wire), "kind") {
			t.Fatal("transaction returned hostile diagnostic metadata")
		}
		if s.Ready() != (code != CodeSchema && code != CodeCorrupt) {
			t.Fatal("core state refusal readiness changed")
		}
	}
	ctx, report := WithFailureReport(background)
	_, err := s.Provision(ctx, "not-ready")
	kindIs(t, err, CodeClosed, failuretype.NotReady)
	takeKinds(t, report, failuretype.NotReady)
}

func TestTransactionWrappedStateClassificationPreservesReadinessPolicy(t *testing.T) {
	s, _, _ := testService(t)
	for _, code := range []Code{CodeSchema, CodeCorrupt} {
		ctx, report := WithFailureReport(background)
		err := s.run(ctx, func(*transaction) error {
			return classificationWrapper{diagnostic(code)}
		}, false)
		kindIs(t, err, code, defaultFailureKind(code))
		takeKinds(t, report, defaultFailureKind(code))
		if !s.Ready() {
			t.Fatal("wrapper classification expanded direct-diagnostic readiness policy")
		}
	}
}

func TestTransactionReportPropagationWrappersDoNotRecountPrimary(t *testing.T) {
	s, _, _ := testService(t)
	original := s.app
	defer func() { s.app = original }()
	for _, join := range []bool{true, false} {
		s.app = completionPropagationApp{App: original, join: join}
		ctx, report := WithFailureReport(background)
		err := s.run(ctx, func(*transaction) error { return nonComparableCallbackFailure{1} }, false)
		kindIs(t, err, CodeStorage, failuretype.TransactionWorkUnknown)
		takeKinds(t, report, failuretype.TransactionWorkUnknown)
	}
}

func TestTransactionKnownCommitRetainsRowsAndReportsLaterFault(t *testing.T) {
	for _, panicAfter := range []bool{false, true} {
		s, _, now := testService(t)
		fund(t, s, "a", "100", now)
		original := s.app
		s.app = completionFaultApp{App: original, panicAfter: panicAfter}
		ctx, report := WithFailureReport(background)
		row, err := s.Credit(ctx, CreditInput{"a", "5", key(now, 2)})
		if err != nil || row.BalanceAfter != "105" || !s.Ready() {
			t.Fatal("known commit/result/readiness lost after later fault")
		}
		takeKinds(t, report, failuretype.TransactionAfterCompletionUnknown)
		s.app = original
		if balance(t, s, "a").AccountBalance.Balance != "105" {
			t.Fatal("known committed row absent")
		}
		// Same accounting API and classification even without a report owner.
		s.app = completionFaultApp{App: original, panicAfter: panicAfter}
		row, err = s.Credit(background, CreditInput{"a", "1", key(now, 3)})
		if err != nil || row.BalanceAfter != "106" {
			t.Fatal("optional report affected accounting")
		}
		s.app = original
	}
}

func TestTransactionActualPocketBaseAfterHooksAndOverflow(t *testing.T) {
	s, _, _ := testService(t)
	original := s.app
	for _, count := range []int{1, 3, 4} {
		s.app = completionHooksApp{App: original, count: count}
		ctx, report := WithFailureReport(background)
		err := s.run(ctx, func(*transaction) error { return nil }, false)
		if err != nil {
			t.Fatal("after hooks erased known success")
		}
		if count <= 3 {
			want := make([]failuretype.Type, count)
			for i := range want {
				want[i] = failuretype.TransactionAfterCompletionUnknown
			}
			takeKinds(t, report, want...)
		} else if _, _, valid := report.Take(); valid {
			t.Fatal("actual SDK after-hook overflow silently lost observations")
		}
	}
	s.app = completionHooksApp{App: original, count: 1}
	ctx, report := WithFailureReport(background)
	err := s.run(ctx, func(*transaction) error { return diagnostic(CodeInput) }, false)
	kindIs(t, err, CodeInput, failuretype.RequestInvalid)
	takeKinds(t, report, failuretype.RequestInvalid, failuretype.TransactionAfterCompletionUnknown)
	s.app = completionHooksApp{App: original, panicAfter: true}
	ctx, report = WithFailureReport(background)
	err = s.run(ctx, func(*transaction) error { return nil }, false)
	if err != nil || !s.Ready() {
		t.Fatal("actual after-hook panic erased known commit")
	}
	takeKinds(t, report, failuretype.TransactionAfterCompletionUnknown)
	ctx, report = WithFailureReport(background)
	err = s.run(ctx, func(*transaction) error { return diagnostic(CodeInput) }, false)
	kindIs(t, err, CodeCommit, failuretype.RequestInvalid)
	if Failure(err).Outcome != "unknown" || s.Ready() {
		t.Fatal("legacy public panic result or readiness policy changed")
	}
	takeKinds(t, report, failuretype.RequestInvalid, failuretype.TransactionAfterCompletionUnknown)
	s.app = original
}

func TestTransactionReportDoesNotAddSQLRetriesOrReadinessEffects(t *testing.T) {
	s, _, _ := testService(t)
	original := s.app
	db := original.NonconcurrentDB().(*dbx.DB)
	var reads, writes atomic.Int32
	db.QueryLogFunc = func(context.Context, time.Duration, string, *sql.Rows, error) { reads.Add(1) }
	db.ExecLogFunc = func(context.Context, time.Duration, string, sql.Result, error) { writes.Add(1) }
	defer func() { db.QueryLogFunc = nil; db.ExecLogFunc = nil; s.app = original }()
	for _, boundary := range []string{"success", "callback", "after-completion"} {
		if boundary == "after-completion" {
			s.app = completionFaultApp{App: original}
		} else {
			s.app = original
		}
		var baselineReads, baselineWrites int32
		for _, reporting := range []bool{false, true} {
			reads.Store(0)
			writes.Store(0)
			ctx := background
			var report *FailureReport
			if reporting {
				ctx, report = WithFailureReport(ctx)
			}
			err := s.run(ctx, func(tx *transaction) error {
				if boundary == "callback" {
					return nonComparableCallbackFailure{1}
				}
				var value int
				return tx.query("SELECT 1", nil).Row(&value)
			}, true)
			if boundary == "callback" {
				kindIs(t, err, CodeStorage, failuretype.TransactionWorkUnknown)
			} else if err != nil {
				t.Fatal("report changed known success")
			}
			if !s.Ready() {
				t.Fatal("report changed readiness")
			}
			if !reporting {
				baselineReads, baselineWrites = reads.Load(), writes.Load()
				if baselineReads == 0 || baselineWrites == 0 {
					t.Fatal("SQL observation fixture did not run")
				}
				continue
			}
			if reads.Load() != baselineReads || writes.Load() != baselineWrites {
				t.Fatal("report introduced extra SQL or replay")
			}
			switch boundary {
			case "success":
				takeKinds(t, report)
			case "callback":
				takeKinds(t, report, failuretype.TransactionWorkUnknown)
			case "after-completion":
				takeKinds(t, report, failuretype.TransactionAfterCompletionUnknown)
			}
			wire, marshalErr := json.Marshal(report)
			if marshalErr != nil || string(wire) != "{}" {
				t.Fatal("numeric report exposed retained state")
			}
		}
	}
}

// Only disposable synthetic tables are changed. The service's existing DSN
// enables foreign_keys on every connection; the failing callback verifies it
// again on the actual transaction connection, without changing driver setup.
func deferredCommitService(t *testing.T) *Service {
	t.Helper()
	s, _, _ := testService(t)
	err := s.run(background, func(tx *transaction) error {
		return tx.exec(`CREATE TABLE synthetic_commit_parent (id INTEGER PRIMARY KEY);
CREATE TABLE synthetic_commit_child (
 parent_id INTEGER REFERENCES synthetic_commit_parent(id) DEFERRABLE INITIALLY DEFERRED
);`, nil)
	}, false)
	if err != nil {
		t.Fatal("deferred commit fixture setup failed")
	}
	return s
}

func deferredCommitInsert(t *testing.T, tx *transaction) error {
	t.Helper()
	var enabled int
	if err := tx.query("PRAGMA foreign_keys", nil).Row(&enabled); err != nil || enabled != 1 {
		t.Fatal("foreign keys not enabled on actual transaction connection")
	}
	if err := tx.exec("INSERT INTO synthetic_commit_child(parent_id) VALUES(1)", nil); err != nil {
		t.Fatal("deferred constraint failed before commit")
	}
	return nil
}

func verifyDeferredCommitCause(t *testing.T, err error) {
	t.Helper()
	var driver *sqlite.Error
	if !errors.As(err, &driver) || driver == nil || driver.Code() != sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY {
		t.Fatal("completion did not observe actual typed deferred foreign-key commit failure")
	}
}

func TestTransactionActualFailedCommitAfterHooks(t *testing.T) {
	for _, nonformatting := range []bool{false, true} {
		for _, count := range []int{0, 1, 2, 3} {
			t.Run(fmt.Sprintf("nonformatting=%t/hooks=%d", nonformatting, count), func(t *testing.T) {
				s := deferredCommitService(t)
				ctx, report := WithFailureReport(background)
				var completionSeen bool
				var hookCalls int
				err := s.run(ctx, func(tx *transaction) error {
					tx.app.TxInfo().OnComplete(func(cause error) error {
						verifyDeferredCommitCause(t, cause)
						completionSeen = true
						return nil
					})
					for range count {
						tx.app.TxInfo().OnComplete(func(cause error) error {
							verifyDeferredCommitCause(t, cause)
							hookCalls++
							if nonformatting {
								return nonComparableCallbackFailure{1}
							}
							return diagnostic(CodeInput)
						})
					}
					return deferredCommitInsert(t, tx)
				}, false)
				kindIs(t, err, CodeCommit, failuretype.SQLiteConstraint)
				if !completionSeen || hookCalls != count || Failure(err).Outcome != "unknown" || !s.Ready() {
					t.Fatal("actual failed completion/hook evidence or public policy changed")
				}
				if count == 3 {
					if kinds, n, valid := report.Take(); valid || n != 0 || kinds != ([3]failuretype.Type{}) {
						t.Fatal("failed commit plus hooks overflow supplied partial evidence")
					}
					return
				}
				want := []failuretype.Type{failuretype.SQLiteConstraint}
				for range count {
					kind := failuretype.RequestInvalid
					if nonformatting {
						kind = failuretype.TransactionAfterCompletionUnknown
					}
					want = append(want, kind)
				}
				takeKinds(t, report, want...)
			})
		}
	}
}

func TestTransactionActualFailedCommitPropagation(t *testing.T) {
	for _, join := range []bool{false, true} {
		t.Run(fmt.Sprintf("join=%t", join), func(t *testing.T) {
			s := deferredCommitService(t)
			s.app = completionPropagationApp{App: s.app, join: join}
			ctx, report := WithFailureReport(background)
			err := s.run(ctx, func(tx *transaction) error { return deferredCommitInsert(t, tx) }, false)
			kindIs(t, err, CodeCommit, failuretype.SQLiteConstraint)
			takeKinds(t, report, failuretype.SQLiteConstraint)
			if !s.Ready() || Failure(err).Outcome != "unknown" {
				t.Fatal("propagation changed completion policy")
			}
		})
	}
}

func TestTransactionActualFailedCommitThenHookPanic(t *testing.T) {
	for _, returnsBeforePanic := range []int{0, 2} {
		t.Run(fmt.Sprintf("returned-hooks=%d", returnsBeforePanic), func(t *testing.T) {
			s := deferredCommitService(t)
			ctx, report := WithFailureReport(background)
			var returnedCalls int
			var panicSeen bool
			err := s.run(ctx, func(tx *transaction) error {
				for range returnsBeforePanic {
					tx.app.TxInfo().OnComplete(func(cause error) error {
						verifyDeferredCommitCause(t, cause)
						returnedCalls++
						return diagnostic(CodeInput)
					})
				}
				tx.app.TxInfo().OnComplete(func(cause error) error {
					verifyDeferredCommitCause(t, cause)
					panicSeen = true
					panic(nonComparableCallbackFailure{1})
				})
				return deferredCommitInsert(t, tx)
			}, false)
			kindIs(t, err, CodeCommit, failuretype.SQLiteConstraint)
			if !panicSeen || returnedCalls != returnsBeforePanic || s.Ready() || Failure(err).Outcome != "unknown" {
				t.Fatal("failed commit then actual hook panic lost evidence or changed public policy")
			}
			// External visibility limit: PB runAfterFuncs discards its local errs
			// when a later hook panics. The project sees only completion + panic;
			// these two slots are NOT evidence that earlier hooks returned nil.
			// No hook interception or SDK-internal inspection is authorized.
			takeKinds(t, report, failuretype.SQLiteConstraint, failuretype.TransactionAfterCompletionUnknown)
		})
	}
}

func TestTransactionFailedCommitUnsupportedAfterShapeInvalid(t *testing.T) {
	s := deferredCommitService(t)
	var completion error
	err := s.run(background, func(tx *transaction) error {
		tx.app.TxInfo().OnComplete(func(cause error) error { completion = cause; return nil })
		return deferredCommitInsert(t, tx)
	}, false)
	kindIs(t, err, CodeCommit, failuretype.SQLiteConstraint)
	verifyDeferredCommitCause(t, completion)
	// Malformed structures are a bounded SDK-evidence seam, not actual PB
	// output. They must not manufacture a healthy/double-counted report.
	for _, malformed := range []error{
		nil,
		errors.Join(completion, diagnostic(CodeInput)),
		errors.Join(completion, fmt.Errorf("synthetic: %w", errors.Join())),
		errors.Join(completion, fmt.Errorf("synthetic: %w", errors.Join(diagnostic(CodeInput))), context.Canceled),
		errors.Join(context.Canceled, fmt.Errorf("synthetic: %w", errors.Join(diagnostic(CodeInput)))),
		errors.Join(completion, fmt.Errorf("synthetic: %w", errors.Join(diagnostic(CodeInput), nil)), nil, context.Canceled),
		nonComparableCallbackFailure{1},
	} {
		batch := transactionFailures{}
		batch.add(failuretype.SQLiteConstraint)
		transactionAfterFailures(malformed, completion, nil, &batch)
		_, report := WithFailureReport(background)
		report.publish(batch)
		if kinds, n, valid := report.Take(); valid || n != 0 || kinds != ([3]failuretype.Type{}) {
			t.Fatal("unsupported failed-commit SDK shape supplied numeric evidence")
		}
	}
}

func TestTransactionSupportedDBXRollbackEvidenceSyntheticSeam(t *testing.T) {
	// This is a bounded synthetic SDK-evidence seam, NOT an induced driver
	// rollback failure. Real PB/SQLite work, rollback and post-hook paths are
	// exercised above. dbx v1.12.0 returns exactly this two-element structure.
	s, _, _ := testService(t)
	for _, callback := range []error{nonComparableCallbackFailure{1}, context.Canceled, diagnostic(CodeInput)} {
		for _, rollback := range []error{nonComparableCallbackFailure{2}, context.DeadlineExceeded, actualFatalError(t)} {
			s.ready.Store(true)
			abort := &callbackFailure{cause: callback}
			evidence := dbx.Errors{abort, rollback}
			cause, ok := rollbackCause(evidence, abort)
			if !ok {
				t.Fatal("supported SDK rollback evidence not recognized")
			}
			var batch transactionFailures
			d := s.failedRollback(callback, cause, &batch)
			if d.Code != CodeCommit || d.Outcome != "unknown" {
				t.Fatal("rollback fault invented financial outcome")
			}
			transactionAfterFailures(evidence, evidence, abort, &batch)
			_, report := WithFailureReport(background)
			report.publish(batch)
			takeKinds(t, report, FailureKind(callback, failuretype.TransactionWorkUnknown), FailureKind(rollback, failuretype.TransactionRollbackUnknown))
			if !s.Ready() {
				t.Fatal("secondary rollback classification changed existing readiness policy")
			}
		}
	}
	abort := &callbackFailure{cause: nonComparableCallbackFailure{1}}
	for _, invalid := range []error{dbx.Errors{abort}, dbx.Errors{abort, nil}, dbx.Errors{abort, context.Canceled, context.Canceled}, dbx.Errors{&callbackFailure{}, context.Canceled}} {
		if _, ok := rollbackCause(invalid, abort); ok {
			t.Fatal("unsupported structure guessed rollback cause")
		}
	}
}
