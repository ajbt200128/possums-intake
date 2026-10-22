package ledger

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"

	"possums-intake/internal/failuretype"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type Service struct {
	app   core.App
	gate  chan struct{}
	ready atomic.Bool
}

func newService(app core.App) *Service {
	s := &Service{app: app, gate: make(chan struct{}, 1)}
	s.ready.Store(true)
	return s
}

// A comparable callback token identifies observed rollback without comparing
// arbitrary SDK errors, whose underlying values may be slices or maps.
type callbackFailure struct {
	cause error
}

func (*callbackFailure) Error() string   { return "LEDGER_TRANSACTION_CALLBACK_ABORTED" }
func (e *callbackFailure) Unwrap() error { return e.cause }

type transaction struct {
	app core.App
	ctx context.Context
	now int64
}

func (tx *transaction) query(sql string, params dbx.Params) *dbx.Query {
	return tx.app.DB().NewQuery(sql).Bind(params).WithContext(tx.ctx)
}
func (tx *transaction) exec(sql string, params dbx.Params) error {
	_, err := tx.query(sql, params).Execute()
	return err
}

// run does not mutate DB context or launch asynchronous accounting work. The
// query context covers the whole callback, not separate fresh per-query budgets.
// Supported RunInTransaction lacks context for begin/commit; these may outlast
// the caller. Gate ownership is retained until authoritative completion returns.
func (s *Service) run(ctx context.Context, fn func(*transaction) error, withClock bool) (result error) {
	var failures transactionFailures
	report := reportFromContext(ctx)
	if report != nil {
		report.used.Store(true)
	}
	// Registered first: panic classification, work cancellation and gate release
	// all finish before numeric evidence is made available to the outer owner.
	defer func() {
		failures.primary = result != nil // after completion/recovery, never inferred from count
		report.publish(failures)
	}()
	fail := func(code Code, kind failuretype.Type) *Diagnostic {
		d := diagnostic(code)
		d.kind = kind
		failures.add(kind)
		return d
	}
	writerFailure := func() *Diagnostic {
		kind := failuretype.WriterWaitExhausted
		if cause := ctx.Err(); cause != nil {
			kind = FailureKind(cause, kind)
		}
		return fail(CodeWriter, kind)
	}
	if !s.Ready() {
		return fail(CodeClosed, failuretype.NotReady)
	}
	wait, cancelWait := context.WithTimeout(ctx, WriterWait)
	defer cancelWait()
	select {
	case s.gate <- struct{}{}:
	case <-wait.Done():
		return writerFailure()
	}
	defer func() { <-s.gate }()
	if !s.Ready() {
		return fail(CodeClosed, failuretype.NotReady)
	}
	if wait.Err() != nil {
		return writerFailure()
	}
	work, cancelWork := context.WithTimeout(ctx, WorkBudget)
	defer cancelWork()
	var callbackErr, completionErr error
	var callbackAbort *callbackFailure
	var entered, completionObserved, commitAttempted bool
	// dbx rolls back then re-panics. Do not propagate panic payloads; without
	// completion evidence the outcome is conservatively unknown.
	defer func() {
		if recover() != nil {
			if completionObserved && completionErr == nil && commitAttempted {
				failures.add(failuretype.TransactionAfterCompletionUnknown)
				result = nil // known commit survives a later hook/presentation panic
			} else {
				s.ready.Store(false)
				if completionObserved && callbackRollback(completionErr, callbackAbort) {
					primary := s.rollbackFailure(callbackErr, work, &failures)
					failures.add(failuretype.TransactionAfterCompletionUnknown)
					// Retain the existing conservative public panic result while
					// reporting the known primary and distinct later fault once.
					d := diagnostic(CodeCommit)
					d.kind = primary.Kind()
					result = d
				} else if cause, ok := rollbackCause(completionErr, callbackAbort); completionObserved && ok {
					result = s.failedRollback(callbackErr, cause, &failures)
					failures.add(failuretype.TransactionAfterCompletionUnknown)
				} else if completionObserved && commitAttempted && completionErr != nil {
					result = fail(CodeCommit, transactionFailureKind(completionErr, failuretype.TransactionCompletionUnknown))
					failures.add(failuretype.TransactionAfterCompletionUnknown)
				} else if !entered {
					result = fail(CodeCommit, failuretype.TransactionBeginUnknown)
				} else {
					// dbx discards rollback errors when re-panicking. No cause or
					// rollback result may be inferred from its panic payload.
					result = fail(CodeCommit, failuretype.TransactionCompletionUnknown)
				}
			}
		}
	}()
	err := s.app.RunInTransaction(func(app core.App) error {
		entered = true
		app.TxInfo().OnComplete(func(err error) error { completionObserved = true; completionErr = err; return nil })
		tx := &transaction{app: app, ctx: work}
		callbackErr = work.Err()
		if callbackErr == nil && withClock {
			callbackErr = tx.clock()
		}
		if callbackErr == nil {
			callbackErr = fn(tx)
		}
		if callbackErr == nil {
			callbackErr = work.Err()
		}
		commitAttempted = callbackErr == nil
		if commitAttempted {
			return nil
		}
		callbackAbort = &callbackFailure{cause: callbackErr}
		return callbackAbort
	})
	if fatalStorage(err) || fatalStorage(completionErr) || fatalStorage(callbackErr) {
		s.ready.Store(false)
	}
	if completionObserved && completionErr == nil && commitAttempted {
		// An after-completion error cannot erase observed successful commit.
		transactionAfterFailures(err, nil, callbackAbort, &failures)
		return nil
	}
	if entered && completionObserved && !commitAttempted {
		if callbackRollback(completionErr, callbackAbort) {
			d := s.rollbackFailure(callbackErr, work, &failures)
			transactionAfterFailures(err, completionErr, callbackAbort, &failures)
			return d
		}
		if cause, ok := rollbackCause(completionErr, callbackAbort); ok {
			// dbx supplies both actual failures. Unknown effect outcome is not a
			// third occurrence, and propagating this SDK result is not a new fault.
			d := s.failedRollback(callbackErr, cause, &failures)
			transactionAfterFailures(err, completionErr, callbackAbort, &failures)
			return d
		}
	}
	if !entered {
		return fail(CodeBegin, transactionFailureKind(err, failuretype.TransactionBeginUnknown))
	}
	d := fail(CodeCommit, transactionFailureKind(completionErr, failuretype.TransactionCompletionUnknown))
	if completionObserved && commitAttempted && completionErr != nil {
		transactionAfterFailures(err, completionErr, callbackAbort, &failures)
	}
	return d
}

func transactionFailureKind(err error, fallback failuretype.Type) failuretype.Type {
	if err == nil {
		return fallback
	}
	return FailureKind(err, fallback)
}

func (s *Service) rollbackFailure(cause error, work context.Context, failures *transactionFailures) *Diagnostic {
	d, kind := classifiedFailure(cause, failuretype.TransactionWorkUnknown)
	code := CodeStorage
	if d != nil && defaultFailureKind(d.Code).Valid() {
		code = d.Code
	} else if !fatalStorage(cause) && (kind == failuretype.Cancelled || kind == failuretype.DeadlineExceeded || work.Err() != nil) {
		code = CodeWork
		if kind == failuretype.TransactionWorkUnknown {
			kind = FailureKind(work.Err(), kind)
		}
	}
	// Classification can look through wrappers; readiness retains its existing
	// direct core-diagnostic policy rather than expanding it in this packet.
	if observed, ok := cause.(*Diagnostic); ok && observed != nil && (observed.Code == CodeCorrupt || observed.Code == CodeSchema) {
		s.ready.Store(false)
	}
	clean := diagnostic(code)
	clean.kind = kind
	failures.add(kind)
	return clean
}

func (s *Service) failedRollback(callbackErr, cause error, failures *transactionFailures) *Diagnostic {
	kind := transactionFailureKind(callbackErr, failuretype.TransactionWorkUnknown)
	failures.add(kind)
	failures.add(FailureKind(cause, failuretype.TransactionRollbackUnknown))
	// The SDK's non-unwrapping rollback wrapper did not change readiness before
	// this packet. Report its cause without introducing a new recovery policy.
	d := diagnostic(CodeCommit)
	d.kind = kind
	return d
}

func callbackRollback(err error, abort *callbackFailure) bool {
	token, ok := err.(*callbackFailure)
	return ok && abort != nil && token == abort
}

// Pinned dbx v1.12.0 has no Unwrap on Errors. Only its exact two-element
// normal rollback structure establishes an independently reported cause.
func rollbackCause(err error, abort *callbackFailure) (error, bool) {
	causes, ok := err.(dbx.Errors)
	if !ok || len(causes) != 2 || !callbackRollback(causes[0], abort) || causes[1] == nil {
		return nil, false
	}
	return causes[1], true
}

func sameCompletionFailure(err, completion error, abort *callbackFailure) bool {
	if abort != nil {
		if callbackRollback(err, abort) {
			return true
		}
		_, ok := rollbackCause(err, abort)
		return ok
	}
	// The pinned SQLite driver returns a concrete pointer on failed commit.
	// Compare only that typed identity, never arbitrary error interface values.
	observed, ok := completion.(*sqlite.Error)
	returned, matches := err.(*sqlite.Error)
	return ok && matches && observed != nil && returned == observed
}

func transactionAfterFailures(err, completion error, abort *callbackFailure, failures *transactionFailures) {
	if err == nil {
		if completion != nil {
			failures.invalid = true
		}
		return
	}
	if completion != nil && sameCompletionFailure(err, completion, abort) {
		return
	}
	defer func() {
		if recover() != nil {
			failures.invalid = true
		}
	}()
	// A single propagation wrapper is not an independently observed fault.
	if completion != nil {
		if wrapper, ok := err.(interface{ Unwrap() error }); ok && sameCompletionFailure(wrapper.Unwrap(), completion, abort) {
			return
		}
	}
	// Inspect only the pinned PocketBase completion wrapper, not an arbitrary
	// recursive error graph. Its outer Join holds txErr (if any) and one fmt
	// wrapper around Join(afterFunc errors). An uncertain shape cannot supply
	// complete numeric evidence; it does not change the accounting outcome.
	fault := err
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		parts := joined.Unwrap()
		index := 0
		if completion != nil {
			if len(parts) == 1 && sameCompletionFailure(parts[0], completion, abort) {
				return
			}
			index = 1
		}
		if len(parts) == index+1 {
			if completion != nil && !sameCompletionFailure(parts[0], completion, abort) {
				failures.invalid = true
				return
			}
			fault = parts[index]
			if wrapper, ok := fault.(interface{ Unwrap() error }); ok {
				if after, ok := wrapper.Unwrap().(interface{ Unwrap() []error }); ok {
					causes := after.Unwrap()
					if len(causes) > 0 {
						// Inspect at most one overflow slot; never silently truncate
						// a larger SDK list into a complete report.
						for _, cause := range causes[:min(len(causes), len(failures.kinds)+1)] {
							failures.add(FailureKind(cause, failuretype.TransactionAfterCompletionUnknown))
						}
						return
					}
				}
			}
		}
	}
	if completion != nil {
		failures.invalid = true
		return
	}
	failures.add(FailureKind(fault, failuretype.TransactionAfterCompletionUnknown))
}

func fatalStorage(err error) (fatal bool) {
	defer func() {
		if recover() != nil {
			fatal = false
		}
	}()
	var failure *sqlite.Error
	if !errors.As(err, &failure) {
		return false
	}
	switch failure.Code() & 0xff {
	case sqlite3.SQLITE_CORRUPT, sqlite3.SQLITE_NOTADB, sqlite3.SQLITE_IOERR, sqlite3.SQLITE_FULL, sqlite3.SQLITE_CANTOPEN:
		return true
	}
	return false
}

func (tx *transaction) clock() error {
	var version, count int
	var high, wall int64
	if err := tx.query("SELECT count(*) FROM ledger_meta", nil).Row(&count); err != nil {
		return err
	}
	if count != 1 {
		return diagnostic(CodeSchema)
	}
	if err := tx.query("SELECT schema_version,clock_high_water_ms FROM ledger_meta WHERE singleton=1", nil).Row(&version, &high); err != nil {
		return err
	}
	if version != 3 || high < 0 || high > 9999999999999 {
		return diagnostic(CodeSchema)
	}
	if err := tx.query("SELECT CAST(strftime('%s','now') AS INTEGER)*1000+CAST(substr(strftime('%f','now'),4,3) AS INTEGER)", nil).Row(&wall); err != nil {
		return err
	}
	tx.now = max(wall, high)
	// Bound all timestamp arithmetic and fixed four-digit-year serialization.
	if tx.now <= 0 || tx.now > 9999999999999-KeyWindowMS {
		return diagnostic(CodeCorrupt)
	}
	return tx.exec("UPDATE ledger_meta SET clock_high_water_ms={:now} WHERE singleton=1", dbx.Params{"now": tx.now})
}

const reservationColumns = "reservation_id,idempotency_key,account_id,maximum,timeout_seconds,initial_expires_at,expires_at,state,charged_amount,terminal_at,retain_until"
const creditColumns = "idempotency_key,account_id,amount,balance_after,committed_at,retain_until"

func validateAccount(a Account) error {
	if !validAccount(a.AccountID) {
		return diagnostic(CodeCorrupt)
	}
	if _, err := a.Balance.units(); err != nil {
		return diagnostic(CodeCorrupt)
	}
	return nil
}
func validateReservation(r Reservation) error {
	issued, err := keyIssued(r.IdempotencyKey)
	maxAmount, amtErr := r.Maximum.units()
	if err != nil || amtErr != nil || maxAmount == 0 || !idPattern.MatchString(r.ReservationID) || !validAccount(r.AccountID) || r.TimeoutSeconds < 1 || r.TimeoutSeconds > 1800 || r.InitialExpiresAt <= 0 || r.InitialExpiresAt > r.ExpiresAt || int64(r.ExpiresAt) <= 0 || int64(r.ExpiresAt) > 9999999999999 {
		return diagnostic(CodeCorrupt)
	}
	// The original admission must have satisfied the immutable new-key window.
	admitted := int64(r.InitialExpiresAt) - r.TimeoutSeconds*1000
	if admitted <= 0 || issued > admitted+FutureSkewMS || admitted >= issued+KeyWindowMS {
		return diagnostic(CodeCorrupt)
	}
	if r.State == Active {
		if r.ChargedAmount != nil || r.TerminalAt != nil || r.RetainUntil != nil {
			return diagnostic(CodeCorrupt)
		}
		return nil
	}
	if r.ChargedAmount == nil || r.TerminalAt == nil || r.RetainUntil == nil {
		return diagnostic(CodeCorrupt)
	}
	charge, err := r.ChargedAmount.units()
	if err != nil || charge > maxAmount || int64(*r.TerminalAt) < admitted || *r.RetainUntil != retention(issued, int64(*r.TerminalAt)) {
		return diagnostic(CodeCorrupt)
	}
	switch r.State {
	case Expired:
		if *r.TerminalAt != r.ExpiresAt || charge != 0 {
			return diagnostic(CodeCorrupt)
		}
	case Refunded:
		if charge != 0 || *r.TerminalAt >= r.ExpiresAt {
			return diagnostic(CodeCorrupt)
		}
	case Settled:
		if *r.TerminalAt >= r.ExpiresAt {
			return diagnostic(CodeCorrupt)
		}
	default:
		return diagnostic(CodeCorrupt)
	}
	return nil
}
func validateCredit(c Credit) error {
	issued, err := keyIssued(c.IdempotencyKey)
	amount, e1 := c.Amount.units()
	balance, e2 := c.BalanceAfter.units()
	if err != nil || e1 != nil || e2 != nil || amount == 0 || balance < amount || !validAccount(c.AccountID) || c.CommittedAt <= 0 || c.RetainUntil != retention(issued, int64(c.CommittedAt)) || validNewKey(issued, int64(c.CommittedAt)) != nil {
		return diagnostic(CodeCorrupt)
	}
	return nil
}
func (tx *transaction) account(id string) (Account, error) {
	var a Account
	err := tx.query("SELECT id AS account_id,balance,enabled FROM accounts WHERE id={:id}", dbx.Params{"id": id}).One(&a)
	if errors.Is(err, sql.ErrNoRows) {
		return a, diagnostic(CodeAccount)
	}
	if err != nil {
		return a, err
	}
	return a, validateAccount(a)
}
func (tx *transaction) reservation(field, id string) (Reservation, error) {
	var r Reservation
	// field is a locally authored literal at every call site, never caller input.
	err := tx.query("SELECT "+reservationColumns+" FROM ledger_reservations WHERE "+field+"={:id}", dbx.Params{"id": id}).One(&r)
	if err != nil {
		return r, err
	}
	return r, validateReservation(r)
}
func (tx *transaction) credit(key string) (Credit, error) {
	var c Credit
	err := tx.query("SELECT "+creditColumns+" FROM ledger_credits WHERE idempotency_key={:key}", dbx.Params{"key": key}).One(&c)
	if err != nil {
		return c, err
	}
	return c, validateCredit(c)
}
func (tx *transaction) active(a Account, expire bool) ([]Reservation, uint64, error) {
	rows := make([]Reservation, 0)
	if err := tx.query("SELECT "+reservationColumns+" FROM ledger_reservations WHERE account_id={:id} AND state='active' ORDER BY reservation_id", dbx.Params{"id": a.AccountID}).All(&rows); err != nil {
		return nil, 0, err
	}
	var held uint64
	pending := make([]Reservation, 0, len(rows))
	for _, r := range rows {
		if err := validateReservation(r); err != nil {
			return nil, 0, err
		}
		amount, _ := r.Maximum.units()
		// Validate the entire pre-expiry hold sum; do not conceal corruption by
		// deleting or expiring impossible holds.
		var err error
		held, err = add(held, amount)
		if err != nil {
			return nil, 0, diagnostic(CodeCorrupt)
		}
	}
	total, _ := a.Balance.units()
	if held > total {
		return nil, 0, diagnostic(CodeCorrupt)
	}
	for _, r := range rows {
		if expire && int64(r.ExpiresAt) <= tx.now {
			if err := tx.expire(&r); err != nil {
				return nil, 0, err
			}
			amount, _ := r.Maximum.units()
			held -= amount
		} else {
			pending = append(pending, r)
		}
	}
	return pending, total - held, nil
}
func (tx *transaction) terminalize(r *Reservation, state State, charge Money, at int64) error {
	issued, _ := keyIssued(r.IdempotencyKey)
	terminal := Millis(at)
	retain := retention(issued, at)
	if err := tx.exec("UPDATE ledger_reservations SET state={:state},charged_amount={:charge},terminal_at={:at},retain_until={:retain} WHERE reservation_id={:id} AND state='active'", dbx.Params{"state": string(state), "charge": string(charge), "at": at, "retain": int64(retain), "id": r.ReservationID}); err != nil {
		return err
	}
	r.State = state
	r.ChargedAmount = &charge
	r.TerminalAt = &terminal
	r.RetainUntil = &retain
	return nil
}
func (tx *transaction) expire(r *Reservation) error {
	if r.State != Active || int64(r.ExpiresAt) > tx.now {
		return nil
	}
	return tx.terminalize(r, Expired, "0", int64(r.ExpiresAt))
}
