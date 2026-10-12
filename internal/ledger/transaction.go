package ledger

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"

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
	if !s.Ready() {
		return diagnostic(CodeClosed)
	}
	wait, cancelWait := context.WithTimeout(ctx, WriterWait)
	defer cancelWait()
	select {
	case s.gate <- struct{}{}:
	case <-wait.Done():
		return diagnostic(CodeWriter)
	}
	defer func() { <-s.gate }()
	if !s.Ready() {
		return diagnostic(CodeClosed)
	}
	if wait.Err() != nil {
		return diagnostic(CodeWriter)
	}
	work, cancelWork := context.WithTimeout(ctx, WorkBudget)
	defer cancelWork()
	var callbackErr, completionErr error
	var entered, completionObserved, commitAttempted bool
	// dbx rolls back then re-panics. Do not propagate panic payloads; without
	// completion evidence the outcome is conservatively unknown.
	defer func() {
		if recover() != nil {
			if completionObserved && completionErr == nil && commitAttempted {
				result = nil // known commit survives a later hook/presentation panic
			} else {
				s.ready.Store(false)
				result = diagnostic(CodeCommit)
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
		return callbackErr
	})
	if fatalStorage(err) || fatalStorage(completionErr) || fatalStorage(callbackErr) {
		s.ready.Store(false)
	}
	if completionObserved && completionErr == nil && commitAttempted {
		// An after-completion error cannot erase observed successful commit.
		return nil
	}
	if entered && completionObserved && !commitAttempted && completionErr == callbackErr {
		if d, ok := callbackErr.(*Diagnostic); ok {
			if d.Code == CodeCorrupt || d.Code == CodeSchema {
				s.ready.Store(false)
			}
			return d
		}
		if fatalStorage(callbackErr) {
			return diagnostic(CodeStorage)
		}
		if errors.Is(callbackErr, context.Canceled) || errors.Is(callbackErr, context.DeadlineExceeded) || work.Err() != nil {
			return diagnostic(CodeWork)
		}
		// The callback error and successful rollback are observed, not guessed from
		// an SQL error string. Driver details never cross the public boundary.
		return diagnostic(CodeStorage)
	}
	if !entered {
		return diagnostic(CodeBegin)
	}
	return diagnostic(CodeCommit)
}
func fatalStorage(err error) bool {
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
	if version != 1 || high < 0 || high > 9999999999999 {
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

const reservationColumns = "reservation_id,idempotency_key,account_id,maximum,timeout_seconds,expires_at,state,charged_amount,terminal_at,retain_until"
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
	if err != nil || amtErr != nil || maxAmount == 0 || !idPattern.MatchString(r.ReservationID) || !validAccount(r.AccountID) || r.TimeoutSeconds < 1 || r.TimeoutSeconds > 1800 || int64(r.ExpiresAt) <= 0 || int64(r.ExpiresAt) > 9999999999999 {
		return diagnostic(CodeCorrupt)
	}
	// The original admission must have satisfied the immutable new-key window.
	admitted := int64(r.ExpiresAt) - r.TimeoutSeconds*1000
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
	err := tx.query("SELECT account_id,balance,enabled FROM ledger_accounts WHERE account_id={:id}", dbx.Params{"id": id}).One(&a)
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
