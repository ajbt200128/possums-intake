package ledger

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"

	"github.com/pocketbase/dbx"
)

func (s *Service) Provision(ctx context.Context, id string) (Account, error) {
	if !validAccount(id) {
		return Account{}, diagnostic(CodeInput)
	}
	var result Account
	err := s.run(ctx, func(tx *transaction) error {
		a, err := tx.account(id)
		if err == nil {
			_, _, err = tx.active(a, true)
			result = a
			return err
		}
		if d, ok := err.(*Diagnostic); !ok || d.Code != CodeAccount {
			return err
		}
		if err := tx.exec("INSERT INTO ledger_accounts(account_id,balance,enabled) VALUES({:id},'0',1)", dbx.Params{"id": id}); err != nil {
			return err
		}
		result = Account{AccountID: id, Balance: "0", Enabled: true}
		return nil
	}, true)
	if err != nil {
		return Account{}, err
	}
	return result, nil
}
func (s *Service) Credit(ctx context.Context, in CreditInput) (Credit, error) {
	issued, err := keyIssued(in.IdempotencyKey)
	if err != nil {
		return Credit{}, err
	}
	amount, err := in.Amount.units()
	if err != nil || amount == 0 || !validAccount(in.AccountID) {
		return Credit{}, diagnostic(CodeInput)
	}
	var result Credit
	err = s.run(ctx, func(tx *transaction) error {
		original, err := tx.credit(in.IdempotencyKey)
		if err == nil {
			if original.AccountID != in.AccountID || original.Amount != in.Amount {
				return diagnostic(CodeConflict)
			}
			result = original
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := validNewKey(issued, tx.now); err != nil {
			return err
		}
		a, err := tx.account(in.AccountID)
		if err != nil {
			return err
		}
		if _, _, err := tx.active(a, true); err != nil {
			return err
		}
		total, _ := a.Balance.units()
		after, err := add(total, amount)
		if err != nil {
			return err
		}
		retain := retention(issued, tx.now)
		if err := tx.exec("UPDATE ledger_accounts SET balance={:after} WHERE account_id={:id}", dbx.Params{"after": string(money(after)), "id": in.AccountID}); err != nil {
			return err
		}
		if err := tx.exec("INSERT INTO ledger_credits(idempotency_key,account_id,amount,balance_after,committed_at,retain_until) VALUES({:key},{:id},{:amount},{:after},{:now},{:retain})", dbx.Params{"key": in.IdempotencyKey, "id": in.AccountID, "amount": string(in.Amount), "after": string(money(after)), "now": tx.now, "retain": int64(retain)}); err != nil {
			return err
		}
		result = Credit{IdempotencyKey: in.IdempotencyKey, AccountID: in.AccountID, Amount: in.Amount, BalanceAfter: money(after), CommittedAt: Millis(tx.now), RetainUntil: retain}
		return nil
	}, true)
	if err != nil {
		return Credit{}, err
	}
	return result, nil
}
func (s *Service) Reserve(ctx context.Context, in ReserveInput) (Reservation, error) {
	issued, err := keyIssued(in.IdempotencyKey)
	if err != nil {
		return Reservation{}, err
	}
	amount, err := in.Maximum.units()
	if err != nil || amount == 0 || !validAccount(in.AccountID) || in.TimeoutSeconds < 1 || in.TimeoutSeconds > 1800 {
		return Reservation{}, diagnostic(CodeInput)
	}
	var result Reservation
	err = s.run(ctx, func(tx *transaction) error {
		original, err := tx.reservation("idempotency_key", in.IdempotencyKey)
		if err == nil {
			if original.AccountID != in.AccountID || original.Maximum != in.Maximum || original.TimeoutSeconds != in.TimeoutSeconds {
				return diagnostic(CodeConflict)
			}
			a, err := tx.account(original.AccountID)
			if err != nil {
				return err
			}
			if _, _, err := tx.active(a, true); err != nil {
				return err
			}
			result, err = tx.reservation("idempotency_key", in.IdempotencyKey)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := validNewKey(issued, tx.now); err != nil {
			return err
		}
		a, err := tx.account(in.AccountID)
		if err != nil {
			return err
		}
		_, available, err := tx.active(a, true)
		if err != nil {
			return err
		}
		if !a.Enabled {
			return diagnostic(CodeDisabled)
		}
		if available < amount {
			return diagnostic(CodeInsufficient)
		}
		// Retry collisions BEFORE insert; never overwrite another reservation. All
		// checks and insertion occur in this serialized transaction.
		var id string
		for attempt := 0; attempt < 3; attempt++ {
			var bytes [16]byte
			if _, err := rand.Read(bytes[:]); err != nil {
				return diagnostic(CodeRandom)
			}
			id = hex.EncodeToString(bytes[:])
			_, err := tx.reservation("reservation_id", id)
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			if err != nil {
				return err
			}
			id = ""
		}
		if id == "" {
			return diagnostic(CodeRandom)
		}
		expires := tx.now + in.TimeoutSeconds*1000
		if err := tx.exec("INSERT INTO ledger_reservations(reservation_id,idempotency_key,account_id,maximum,timeout_seconds,expires_at,state) VALUES({:rid},{:key},{:id},{:max},{:timeout},{:expires},'active')", dbx.Params{"rid": id, "key": in.IdempotencyKey, "id": in.AccountID, "max": string(in.Maximum), "timeout": in.TimeoutSeconds, "expires": expires}); err != nil {
			return err
		}
		result = Reservation{ReservationID: id, IdempotencyKey: in.IdempotencyKey, AccountID: in.AccountID, Maximum: in.Maximum, TimeoutSeconds: in.TimeoutSeconds, ExpiresAt: Millis(expires), State: Active}
		return nil
	}, true)
	if err != nil {
		return Reservation{}, err
	}
	return result, nil
}
func (s *Service) Settle(ctx context.Context, in SettleInput) (Reservation, error) {
	if _, err := in.ChargedAmount.units(); err != nil {
		return Reservation{}, diagnostic(CodeInput)
	}
	return s.finish(ctx, in.ReservationID, Settled, in.ChargedAmount)
}
func (s *Service) Refund(ctx context.Context, id string) (Reservation, error) {
	return s.finish(ctx, id, Refunded, "0")
}
func (s *Service) finish(ctx context.Context, id string, state State, charge Money) (Reservation, error) {
	if !idPattern.MatchString(id) {
		return Reservation{}, diagnostic(CodeInput)
	}
	var result Reservation
	var decision error
	err := s.run(ctx, func(tx *transaction) error {
		r, err := tx.reservation("reservation_id", id)
		if errors.Is(err, sql.ErrNoRows) {
			return diagnostic(CodeEvidence)
		}
		if err != nil {
			return err
		}
		a, err := tx.account(r.AccountID)
		if err != nil {
			return err
		}
		if _, _, err := tx.active(a, true); err != nil {
			return err
		}
		r, err = tx.reservation("reservation_id", id)
		if err != nil {
			return err
		}
		if r.State != Active {
			result = r
			if r.State == Expired && state == Settled {
				decision = diagnostic(CodeExpired)
			}
			return nil
		}
		charged, _ := charge.units()
		maximum, _ := r.Maximum.units()
		if charged > maximum {
			return diagnostic(CodeCharge)
		}
		if state == Settled {
			total, _ := a.Balance.units()
			if total < charged {
				return diagnostic(CodeCorrupt)
			}
			if err := tx.exec("UPDATE ledger_accounts SET balance={:balance} WHERE account_id={:id}", dbx.Params{"balance": string(money(total - charged)), "id": a.AccountID}); err != nil {
				return err
			}
		}
		if err := tx.terminalize(&r, state, charge, tx.now); err != nil {
			return err
		}
		result = r
		return nil
	}, true)
	if err != nil {
		return Reservation{}, err
	}
	return result, decision
}
func (s *Service) Balance(ctx context.Context, id string) (BalanceSnapshot, error) {
	if !validAccount(id) {
		return BalanceSnapshot{}, diagnostic(CodeInput)
	}
	var result BalanceSnapshot
	err := s.run(ctx, func(tx *transaction) error {
		a, err := tx.account(id)
		if err != nil {
			return err
		}
		rows, available, err := tx.active(a, true)
		if err != nil {
			return err
		}
		result = BalanceSnapshot{AccountBalance: a, PendingReservations: rows, Available: money(available)}
		return nil
	}, true)
	if err != nil {
		return BalanceSnapshot{}, err
	}
	return result, nil
}
func (s *Service) Lookup(ctx context.Context, id string) (Reservation, error) {
	if !idPattern.MatchString(id) {
		return Reservation{}, diagnostic(CodeInput)
	}
	var result Reservation
	err := s.run(ctx, func(tx *transaction) error {
		r, err := tx.reservation("reservation_id", id)
		if errors.Is(err, sql.ErrNoRows) {
			return diagnostic(CodeEvidence)
		}
		if err != nil {
			return err
		}
		a, err := tx.account(r.AccountID)
		if err != nil {
			return err
		}
		if _, _, err := tx.active(a, false); err != nil {
			return err
		}
		if err := tx.expire(&r); err != nil {
			return err
		}
		result = r
		return nil
	}, true)
	if err != nil {
		return Reservation{}, err
	}
	return result, nil
}

// Cleanup expires up to 128 due rows; prune=true also prunes at most 128
// eligible terminal/credit rows COMBINED. It never loops through a backlog.
func (s *Service) Cleanup(ctx context.Context, prune bool) (CleanupResult, error) {
	var result CleanupResult
	err := s.run(ctx, func(tx *transaction) error {
		var due []Reservation
		if err := tx.query("SELECT "+reservationColumns+" FROM ledger_reservations WHERE state='active' AND expires_at<={:now} ORDER BY expires_at,reservation_id LIMIT 128", dbx.Params{"now": tx.now}).All(&due); err != nil {
			return err
		}
		for _, r := range due {
			if err := validateReservation(r); err != nil {
				return err
			}
			a, err := tx.account(r.AccountID)
			if err != nil {
				return err
			}
			if _, _, err := tx.active(a, false); err != nil {
				return err
			}
			if err := tx.expire(&r); err != nil {
				return err
			}
			result.Expired++
		}
		if !prune {
			return nil
		}
		var terminal []Reservation
		if err := tx.query("SELECT "+reservationColumns+" FROM ledger_reservations WHERE state!='active' AND retain_until<={:now} ORDER BY retain_until,reservation_id LIMIT 128", dbx.Params{"now": tx.now}).All(&terminal); err != nil {
			return err
		}
		for _, r := range terminal {
			if err := validateReservation(r); err != nil {
				return err
			}
			if err := tx.exec("DELETE FROM ledger_reservations WHERE reservation_id={:id}", dbx.Params{"id": r.ReservationID}); err != nil {
				return err
			}
			result.Pruned++
		}
		var credits []Credit
		if err := tx.query("SELECT "+creditColumns+" FROM ledger_credits WHERE retain_until<={:now} ORDER BY retain_until,idempotency_key LIMIT {:limit}", dbx.Params{"now": tx.now, "limit": CleanupLimit - result.Pruned}).All(&credits); err != nil {
			return err
		}
		for _, c := range credits {
			if err := validateCredit(c); err != nil {
				return err
			}
			if err := tx.exec("DELETE FROM ledger_credits WHERE idempotency_key={:key}", dbx.Params{"key": c.IdempotencyKey}); err != nil {
				return err
			}
			result.Pruned++
		}
		return nil
	}, true)
	if err != nil {
		return CleanupResult{}, err
	}
	return result, nil
}
