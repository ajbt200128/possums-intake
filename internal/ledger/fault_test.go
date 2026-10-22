package ledger

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

func TestWriterWaitFastReleaseAndBoundedCancellation(t *testing.T) {
	s, _, _ := testService(t)
	if _, err := fixtureAccount(s, background, "aaaaaaaaaaaaaaa"); err != nil {
		t.Fatal("fixture creation unavailable")
	}
	s.gate <- struct{}{}
	released := make(chan struct{})
	timer := time.AfterFunc(30*time.Millisecond, func() { <-s.gate; close(released) })
	start := time.Now()
	a, e := s.Balance(background, "aaaaaaaaaaaaaaa")
	must(t, a, e)
	<-released
	timer.Stop()
	if time.Since(start) < 20*time.Millisecond {
		t.Fatal("busy writer was rejected instead of waited for")
	}
	s.gate <- struct{}{}
	ctx, cancel := context.WithTimeout(background, 30*time.Millisecond)
	_, e = s.Balance(ctx, "cancelled000000")
	cancel()
	codeIs(t, e, CodeWriter)
	<-s.gate
	// Full writer wait is independent of the work budget, with no transaction
	// started and no possibility of a detached mutation after rejection.
	s.gate <- struct{}{}
	start = time.Now()
	_, e = s.Balance(background, "timedout0000000")
	codeIs(t, e, CodeWriter)
	elapsed := time.Since(start)
	<-s.gate
	if elapsed < 1900*time.Millisecond || elapsed > 3*time.Second {
		t.Fatal("writer wait outside qualified local bounds")
	}
	for _, id := range []string{"cancelled000000", "timedout0000000"} {
		_, e = s.Balance(background, id)
		codeIs(t, e, CodeAccount)
	}
}
func TestCancelledWorkRollsBackAndQueryBudget(t *testing.T) {
	s, _, _ := testService(t)
	ctx, cancel := context.WithCancel(background)
	e := s.run(ctx, func(tx *transaction) error {
		if e := tx.exec("INSERT INTO accounts(id,balance,enabled) VALUES('cancelled000000','0',1)", nil); e != nil {
			return e
		}
		cancel() // cancellation after a write, before callback/commit completion
		return nil
	}, true)
	codeIs(t, e, CodeWork)
	_, e = s.Balance(background, "cancelled000000")
	codeIs(t, e, CodeAccount)
	start := time.Now()
	e = s.run(background, func(tx *transaction) error {
		var sum int64
		return tx.query("WITH RECURSIVE work(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM work WHERE x<1000000000) SELECT sum(x) FROM work", nil).Row(&sum)
	}, true)
	codeIs(t, e, CodeWork)
	if time.Since(start) > 3*time.Second {
		t.Fatal("actual SQLite work cancellation exceeded qualified local bound")
	}
	// Callback that returns nil after the budget is still rolled back by the
	// mandatory check immediately before PocketBase attempts commit.
	e = s.run(background, func(tx *transaction) error {
		if e := tx.exec("INSERT INTO accounts(id,balance,enabled) VALUES('overbudget00000','0',1)", nil); e != nil {
			return e
		}
		<-tx.ctx.Done()
		return nil
	}, true)
	codeIs(t, e, CodeWork)
	_, e = s.Balance(background, "overbudget00000")
	codeIs(t, e, CodeAccount)
}
func TestExternalSQLiteLockBusyBudgetAndNoLaterEffect(t *testing.T) {
	s, dir, now := testService(t)
	fund(t, s, "aaaaaaaaaaaaaaa", "100", now)
	// A raw synthetic lock fixture, NOT a second running PocketBase authority.
	locker, e := connect(filepath.Join(dir, "data.db"), false)
	if e != nil {
		t.Fatal("lock connection failed")
	}
	defer locker.Close()
	tx, e := locker.Begin()
	if e != nil {
		t.Fatal("lock begin failed")
	}
	if _, e := tx.NewQuery("UPDATE ledger_meta SET clock_high_water_ms=clock_high_water_ms WHERE singleton=1").Execute(); e != nil {
		t.Fatal("lock acquisition failed")
	}
	defer tx.Rollback()
	input := CreditInput{"aaaaaaaaaaaaaaa", "5", key(now, 2)}
	start := time.Now()
	_, e = s.Credit(background, input)
	codeIs(t, e, CodeStorage)
	// A WAL read-to-write upgrade may fail BUSY immediately rather than wait;
	// the configured busy timeout is a maximum, not guaranteed queueing.
	if time.Since(start) > 1500*time.Millisecond {
		t.Fatal("accounting lock rejection exceeded the qualified local bound")
	}
	start = time.Now()
	e = s.run(background, func(tx *transaction) error {
		return tx.exec("UPDATE accounts SET balance='105' WHERE id='aaaaaaaaaaaaaaa'", nil)
	}, false) // first statement writes, exercising the actual busy handler
	codeIs(t, e, CodeStorage)
	elapsed := time.Since(start)
	if elapsed < 150*time.Millisecond || elapsed > 1500*time.Millisecond {
		t.Fatal("direct writer busy wait did not use the qualified 250ms local budget")
	}
	ctx, cancel := context.WithTimeout(background, 20*time.Millisecond)
	e = s.run(ctx, func(tx *transaction) error {
		return tx.exec("UPDATE accounts SET balance='105' WHERE id='aaaaaaaaaaaaaaa'", nil)
	}, false)
	cancel()
	codeIs(t, e, CodeWork)
	if e := tx.Rollback(); e != nil {
		t.Fatal("lock release failed")
	}
	if balance(t, s, "aaaaaaaaaaaaaaa").AccountBalance.Balance != "100" {
		t.Fatal("rejected locked work committed later")
	}
	c, e := s.Credit(background, input)
	if must(t, c, e).BalanceAfter != "105" {
		t.Fatal("locked rejection left a marker")
	}
}

type failedBegin struct {
	core.App
	failure error
}

func (a failedBegin) RunInTransaction(func(core.App) error) error { return a.failure }

func TestBeginFailureAndObservedFatalStorage(t *testing.T) {
	s, _, _ := testService(t)
	original := s.app
	s.app = failedBegin{original, errors.New("SYNTHETIC_HOSTILE_BEGIN")}
	_, e := s.Balance(background, "neverentered000")
	codeIs(t, e, CodeBegin)
	if Failure(e).Outcome != "no_effect" || Failure(e).Stage != "transaction_begin" {
		t.Fatal("begin failure invented rollback")
	}
	s.app = original
	_, e = s.Balance(background, "neverentered000")
	codeIs(t, e, CodeAccount)
	// Obtain an ACTUAL typed SQLite NOTADB error without reflecting its text.
	path := filepath.Join(t.TempDir(), "corrupt.db")
	if e := os.WriteFile(path, []byte("SYNTHETIC_INVALID_SQLITE"), 0600); e != nil {
		t.Fatal("fixture file failed")
	}
	db, e := connect(path, false)
	if e != nil {
		t.Fatal("fixture connector failed")
	}
	defer db.Close()
	_, storageErr := db.NewQuery("SELECT 1").Execute()
	if !fatalStorage(storageErr) {
		t.Fatal("actual fatal driver category not recognized")
	}
	s.app = failedBegin{original, storageErr}
	_, e = s.Balance(background, "fatal0000000000")
	codeIs(t, e, CodeBegin)
	if s.Ready() {
		t.Fatal("observed fatal storage failure left readiness true")
	}
	s.app = original
}

// Test-only wrappers exercise begin/commit/evidence-loss boundaries around the
// REAL supported PocketBase transaction. There are no production fault hooks.
type stalledCompletion struct {
	core.App
	reached chan struct{}
	release chan struct{}
}

func (a stalledCompletion) RunInTransaction(fn func(core.App) error) error {
	return a.App.RunInTransaction(func(tx core.App) error {
		if e := fn(tx); e != nil {
			return e
		}
		close(a.reached)
		<-a.release // after callback work check; modeled commit/fsync stall
		return nil
	})
}
func TestCommitStallRetainsGateUntilActualCompletion(t *testing.T) {
	s, _, now := testService(t)
	fund(t, s, "aaaaaaaaaaaaaaa", "100", now)
	original := s.app
	reached := make(chan struct{})
	release := make(chan struct{})
	s.app = stalledCompletion{original, reached, release}
	ctx, cancel := context.WithCancel(background)
	type response struct {
		row Credit
		err error
	}
	done := make(chan response, 1)
	go func() {
		c, e := s.Credit(ctx, CreditInput{"aaaaaaaaaaaaaaa", "5", key(now, 2)})
		done <- response{c, e}
	}()
	<-reached
	cancel()
	// Cancellation must not release a committing transaction or pretend rollback.
	select {
	case <-done:
		close(release)
		t.Fatal("commit work abandoned before completion")
	case <-time.After(20 * time.Millisecond):
	}
	wait, cancelWait := context.WithTimeout(background, 20*time.Millisecond)
	_, e := s.Balance(wait, "blocked00000000")
	cancelWait()
	codeIs(t, e, CodeWriter)
	close(release)
	result := <-done
	if must(t, result.row, result.err).BalanceAfter != "105" {
		t.Fatal("observed commit lost its authoritative result")
	}
	s.app = original
	if balance(t, s, "aaaaaaaaaaaaaaa").AccountBalance.Balance != "105" {
		t.Fatal("stall effect not committed exactly once")
	}
	_, e = s.Balance(background, "blocked00000000")
	codeIs(t, e, CodeAccount)
}

type stalledBegin struct {
	core.App
	reached chan struct{}
	release chan struct{}
}

func (a stalledBegin) RunInTransaction(fn func(core.App) error) error {
	close(a.reached)
	<-a.release
	return a.App.RunInTransaction(fn)
}
func TestBeginStallCancelledCallbackMakesNoEffect(t *testing.T) {
	s, _, now := testService(t)
	fund(t, s, "aaaaaaaaaaaaaaa", "100", now)
	original := s.app
	reached := make(chan struct{})
	release := make(chan struct{})
	s.app = stalledBegin{original, reached, release}
	ctx, cancel := context.WithCancel(background)
	done := make(chan error, 1)
	go func() { _, e := s.Credit(ctx, CreditInput{"aaaaaaaaaaaaaaa", "5", key(now, 2)}); done <- e }()
	<-reached
	cancel()
	select {
	case <-done:
		close(release)
		t.Fatal("begin work abandoned")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	codeIs(t, <-done, CodeWork)
	s.app = original
	if balance(t, s, "aaaaaaaaaaaaaaa").AccountBalance.Balance != "100" {
		t.Fatal("cancelled begin caused effect")
	}
}

type lostCompletionApp struct{ core.App }
type lostCompletionTx struct {
	core.App
	hidden *core.TxAppInfo
}

func (a lostCompletionTx) TxInfo() *core.TxAppInfo { return a.hidden }
func (a lostCompletionApp) RunInTransaction(fn func(core.App) error) error {
	return a.App.RunInTransaction(func(tx core.App) error { return fn(lostCompletionTx{tx, &core.TxAppInfo{}}) })
}

type afterCommitFailure struct{ core.App }

func (a afterCommitFailure) RunInTransaction(fn func(core.App) error) error {
	if e := a.App.RunInTransaction(fn); e != nil {
		return e
	}
	return errors.New("SYNTHETIC_HOSTILE_AFTER_COMPLETION")
}
func TestCompletionEvidenceLossAndSameIdentityResolution(t *testing.T) {
	s, _, now := testService(t)
	fund(t, s, "aaaaaaaaaaaaaaa", "100", now)
	original := s.app
	s.app = lostCompletionApp{original}
	input := CreditInput{"aaaaaaaaaaaaaaa", "5", key(now, 2)}
	c, e := s.Credit(background, input)
	codeIs(t, e, CodeCommit)
	if c != (Credit{}) || Failure(e).Outcome != "unknown" {
		t.Fatal("missing completion manufactured receipt or rollback")
	}
	s.app = original
	recovered, e := s.Credit(background, input)
	if must(t, recovered, e).BalanceAfter != "105" {
		t.Fatal("same-identity uncertainty resolution failed")
	}
	if balance(t, s, "aaaaaaaaaaaaaaa").AccountBalance.Balance != "105" {
		t.Fatal("uncertain mutation replayed money")
	}
	// An observed successful completion remains successful despite a later local
	// framework error. That is distinct from loss of completion evidence above.
	s.app = afterCommitFailure{original}
	c, e = s.Credit(background, CreditInput{"aaaaaaaaaaaaaaa", "1", key(now, 3)})
	if must(t, c, e).BalanceAfter != "106" {
		t.Fatal("known completion was erased by later framework failure")
	}
	s.app = original
}
func TestRollbackAndPanicNeverExposeHostilePayload(t *testing.T) {
	s, _, _ := testService(t)
	e := s.run(background, func(tx *transaction) error {
		if e := tx.exec("INSERT INTO accounts(id,balance,enabled) VALUES('rollback0000000','0',1)", nil); e != nil {
			return e
		}
		return errors.New("SYNTHETIC_HOSTILE_SQL_OR_SDK_TEXT")
	}, true)
	codeIs(t, e, CodeStorage)
	_, e = s.Balance(background, "rollback0000000")
	codeIs(t, e, CodeAccount)
	e = s.run(background, func(tx *transaction) error {
		if e := tx.exec("INSERT INTO accounts(id,balance,enabled) VALUES('panic0000000000','0',1)", nil); e != nil {
			return e
		}
		panic("SYNTHETIC_HOSTILE_PANIC")
	}, true)
	codeIs(t, e, CodeCommit)
	if s.Ready() {
		t.Fatal("panic left accounting ready")
	}
	if Failure(e).Outcome != "unknown" {
		t.Fatal("panic cleanup asserted unobserved rollback")
	}
}
func TestBoundedCleanupAndReadsIndependentOfBacklog(t *testing.T) {
	s, _, now := testService(t)
	fund(t, s, "aaaaaaaaaaaaaaa", "1000", now)
	var expiry int64
	for i := 0; i < 130; i++ {
		r := reserve(t, s, ReserveInput{"aaaaaaaaaaaaaaa", "1", 1, key(now, i+2)})
		expiry = int64(r.ExpiresAt)
	}
	advance(t, s, expiry)
	out, e := s.Cleanup(background, false)
	if must(t, out, e).Expired != 128 {
		t.Fatal("expiry cleanup exceeded or missed its batch bound")
	}
	// Remaining two holds are due; balance must terminalize them even when a
	// lifecycle tick has not caught up, not merely omit them from availability.
	b := balance(t, s, "aaaaaaaaaaaaaaa")
	if b.Available != "1000" || len(b.PendingReservations) != 0 {
		t.Fatal("account read depended on cleanup keeping up")
	}
	advance(t, s, expiry+KeyWindowMS)
	out, e = s.Cleanup(background, true)
	if must(t, out, e).Pruned != 128 {
		t.Fatal("prune cleanup violated combined batch bound")
	}
	out, e = s.Cleanup(background, true)
	if must(t, out, e).Pruned != 3 {
		t.Fatal("later tick did not finish bounded backlog")
	}
}
