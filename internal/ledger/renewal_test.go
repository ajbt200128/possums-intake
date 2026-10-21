package ledger

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"

	"possums-intake/internal/failuretype"

	"github.com/pocketbase/dbx"
)

func renew(t *testing.T, s *Service, r Reservation) Renewal {
	t.Helper()
	out, err := s.Renew(background, RenewInput{r.ReservationID, r.ExpiresAt})
	return must(t, out, err)
}

func TestRenewalCompareExtendAndRefusals(t *testing.T) {
	s, _, now := testService(t)
	fund(t, s, "a", "100", now)
	in := ReserveInput{"a", "60", 600, key(now, 2)}
	r := reserve(t, s, in)
	if r.InitialExpiresAt != r.ExpiresAt {
		t.Fatal("initial deadline differs from admission")
	}
	out, err := s.Renew(background, RenewInput{r.ReservationID, r.ExpiresAt})
	codeIs(t, err, CodeRenewalNoProgress)
	if !reflect.DeepEqual(out.Reservation, r) || out.ObservedAt != Millis(now) {
		t.Fatal("no-progress refusal lacks authoritative snapshot")
	}
	for _, expected := range []Millis{0, -1, 10000000000000, r.InitialExpiresAt - 1} {
		out, err := s.Renew(background, RenewInput{r.ReservationID, expected})
		codeIs(t, err, CodeInput)
		if out != (Renewal{}) {
			t.Fatal("invalid input returned usable receipt")
		}
	}
	advance(t, s, now+1000)
	first := renew(t, s, r)
	want := r
	want.ExpiresAt += 1000
	if !reflect.DeepEqual(first.Reservation, want) || first.ObservedAt != Millis(now+1000) {
		t.Fatal("renewal altered immutable inputs or used non-authoritative time")
	}
	advance(t, s, now+2000)
	// Older exact and reordered intermediate tokens must not extend again.
	for _, expected := range []Millis{r.ExpiresAt, r.ExpiresAt + 500} {
		out, err := s.Renew(background, RenewInput{r.ReservationID, expected})
		if !reflect.DeepEqual(must(t, out, err).Reservation, want) || out.ObservedAt != Millis(now+2000) {
			t.Fatal("duplicate/reordered token changed deadline")
		}
	}
	ctx, report := WithFailureReport(background)
	out, err = s.Renew(ctx, RenewInput{r.ReservationID, want.ExpiresAt + 1})
	codeIs(t, err, CodeRenewalPrecondition)
	_, count, valid, primary := report.TakeWithPrimary()
	if !valid || primary || count != 0 || !reflect.DeepEqual(out.Reservation, want) {
		t.Fatal("known decision counted in transaction or lacks committed row")
	}
	if retry := reserve(t, s, in); !reflect.DeepEqual(retry, want) {
		t.Fatal("reserve duplicate lost current deadline")
	}
	b := balance(t, s, "a")
	if b.Available != "40" || b.AccountBalance.Balance != "100" {
		t.Fatal("renewal moved funds")
	}
	wire, err := json.Marshal(first)
	if err != nil || !strings.Contains(string(wire), `"initial_expires_at":`) || !strings.Contains(string(wire), `"observed_at":`) {
		t.Fatal("renewal snapshot missing complete timestamp fields")
	}
}

func TestRenewalDisabledIncludingDuplicateCommitsOtherExpiry(t *testing.T) {
	s, _, now := testService(t)
	fund(t, s, "a", "100", now)
	r := reserve(t, s, ReserveInput{"a", "60", 600, key(now, 2)})
	due := reserve(t, s, ReserveInput{"a", "10", 1, key(now, 3)})
	advance(t, s, now+500)
	current := renew(t, s, r).Reservation
	if err := s.run(background, func(tx *transaction) error {
		return tx.exec("UPDATE ledger_accounts SET enabled=0 WHERE account_id='a'", nil)
	}, false); err != nil {
		t.Fatal("disable fixture failed")
	}
	advance(t, s, now+2000)
	for _, expected := range []Millis{r.ExpiresAt, current.ExpiresAt, current.ExpiresAt + 1} {
		out, err := s.Renew(background, RenewInput{r.ReservationID, expected})
		codeIs(t, err, CodeDisabled)
		if !reflect.DeepEqual(out.Reservation, current) || Failure(err).Outcome != "no_effect" {
			t.Fatal("disabled renewal changed hold or lost row")
		}
	}
	var state State
	if err := s.run(background, func(tx *transaction) error {
		return tx.query("SELECT state FROM ledger_reservations WHERE reservation_id={:id}", dbx.Params{"id": due.ReservationID}).Row(&state)
	}, false); err != nil || state != Expired {
		t.Fatal("known refusal rolled back independent due expiry")
	}
	// Terminal takes precedence over disabled and returns actual terminal state.
	terminal, err := s.Refund(background, r.ReservationID)
	must(t, terminal, err)
	out, err := s.Renew(background, RenewInput{r.ReservationID, r.ExpiresAt})
	codeIs(t, err, CodeRenewalClosed)
	if !reflect.DeepEqual(out.Reservation, terminal) || Failure(err).Outcome != "terminal" {
		t.Fatal("closed renewal did not carry actual terminal row")
	}
}

func TestRenewalConcurrentDuplicateAndTerminalRaces(t *testing.T) {
	for _, operation := range []string{"duplicates", "settle", "refund", "expiry"} {
		t.Run(operation, func(t *testing.T) {
			s, _, now := testService(t)
			fund(t, s, "a", "100", now)
			r := reserve(t, s, ReserveInput{"a", "60", 600, key(now, 2)})
			if operation == "expiry" {
				advance(t, s, int64(r.ExpiresAt))
			} else {
				advance(t, s, now+1000)
			}
			var wg sync.WaitGroup
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					out, err := s.Renew(background, RenewInput{r.ReservationID, r.ExpiresAt})
					if err != nil {
						codeIs(t, err, CodeRenewalClosed)
						if out.Reservation.State == Active {
							t.Error("terminal refusal returned active hold")
						}
					} else if out.Reservation.ExpiresAt != r.ExpiresAt+1000 || operation == "expiry" {
						t.Error("duplicate extended more than once or resurrected expiry")
					}
				}()
			}
			if operation == "settle" || operation == "refund" || operation == "expiry" {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if operation == "refund" {
						_, err := s.Refund(background, r.ReservationID)
						if err != nil {
							t.Error("refund failed")
						}
					} else {
						_, err := s.Settle(background, SettleInput{r.ReservationID, "23"})
						if operation == "expiry" {
							codeIs(t, err, CodeExpired)
						} else if err != nil {
							t.Error("settlement failed")
						}
					}
				}()
			}
			wg.Wait()
			row, err := s.Lookup(background, r.ReservationID)
			must(t, row, err)
			wantState, wantBalance, wantAvailable := Active, Money("100"), Money("40")
			switch operation {
			case "settle":
				wantState, wantBalance, wantAvailable = Settled, "77", "77"
			case "refund":
				wantState, wantAvailable = Refunded, "100"
			case "expiry":
				wantState, wantAvailable = Expired, "100"
			}
			b := balance(t, s, "a")
			if row.State != wantState || row.InitialExpiresAt != r.InitialExpiresAt || b.AccountBalance.Balance != wantBalance || b.Available != wantAvailable {
				t.Fatal("race altered immutable state or money")
			}
		})
	}
}

func TestRenewalLostCompletionAndLostResponse(t *testing.T) {
	for _, completionLost := range []bool{true, false} {
		s, _, now := testService(t)
		fund(t, s, "a", "100", now)
		r := reserve(t, s, ReserveInput{"a", "60", 600, key(now, 2)})
		advance(t, s, now+1000)
		base := s.app
		if completionLost {
			s.app = lostCompletionApp{base}
		} else {
			s.app = afterCommitFailure{base}
		}
		ctx, report := WithFailureReport(background)
		out, err := s.Renew(ctx, RenewInput{r.ReservationID, r.ExpiresAt})
		if completionLost {
			codeIs(t, err, CodeCommit)
			if out != (Renewal{}) {
				t.Fatal("unknown completion returned usable renewal")
			}
			takeKinds(t, report, failuretype.TransactionCompletionUnknown)
		} else {
			if must(t, out, err).Reservation.ExpiresAt != r.ExpiresAt+1000 {
				t.Fatal("known commit erased by response fault")
			}
			takeKinds(t, report, failuretype.TransactionAfterCompletionUnknown)
		}
		s.app = base
		advance(t, s, now+2000)
		repeated := renew(t, s, r)
		if repeated.Reservation.ExpiresAt != r.ExpiresAt+1000 || balance(t, s, "a").Available != "40" {
			t.Fatal("lost-response duplicate extended again or changed funds")
		}
	}
}

func TestRenewalSettlementAfterInitialDeadlineAndClassification(t *testing.T) {
	for _, fixture := range []struct {
		code    Code
		kind    failuretype.Type
		outcome string
	}{
		{CodeRenewalClosed, failuretype.PolicyRefused, "terminal"},
		{CodeRenewalPrecondition, failuretype.IdempotencyRefused, "no_effect"},
		{CodeRenewalNoProgress, failuretype.PolicyRefused, "no_effect"},
	} {
		d := CanonicalFailure(diagnostic(fixture.code), failuretype.TransactionWorkUnknown)
		if d.Code != fixture.code || d.Kind() != fixture.kind || d.Outcome != fixture.outcome || d.Stage != "renewal" {
			t.Fatal("renewal classification lost its closed contract")
		}
	}
	s, dir, now := testService(t)
	fund(t, s, "a", "100", now)
	r := reserve(t, s, ReserveInput{"a", "60", 600, key(now, 2)})
	advance(t, s, now+300000)
	current := renew(t, s, r).Reservation
	advance(t, s, int64(r.InitialExpiresAt)+1)
	settled, err := s.Settle(background, SettleInput{r.ReservationID, "23"})
	must(t, settled, err)
	if settled.ExpiresAt != current.ExpiresAt || settled.InitialExpiresAt != r.InitialExpiresAt || *settled.TerminalAt <= r.InitialExpiresAt {
		t.Fatal("settlement used original rather than authoritative deadline")
	}
	if err := s.Close(); err != nil {
		t.Fatal("close failed")
	}
	opened, err := Open(background, dir)
	must(t, opened, err)
	defer opened.Close()
	row, err := opened.Settle(background, SettleInput{r.ReservationID, "60"})
	if !reflect.DeepEqual(must(t, row, err), settled) || balance(t, opened, "a").AccountBalance.Balance != "77" {
		t.Fatal("renewed settlement lost terminal evidence or charged twice")
	}
}

func TestRenewalBeyondKeyWindowRestartExpiryAndPruning(t *testing.T) {
	s, dir, now := testService(t)
	fund(t, s, "a", "100", now)
	in := ReserveInput{"a", "60", 1800, key(now, 2)}
	original := reserve(t, s, in)
	current := original
	for i := int64(1); i <= 100; i++ {
		advance(t, s, now+i*900000)
		current = renew(t, s, current).Reservation
	}
	if current.ExpiresAt <= original.ExpiresAt+Millis(KeyWindowMS) {
		t.Fatal("fixture did not cross original key window")
	}
	out, err := s.Cleanup(background, true)
	if must(t, out, err).Pruned != 1 {
		t.Fatal("active reservation pruned, or eligible credit retained")
	}
	if retry := reserve(t, s, in); !reflect.DeepEqual(retry, current) {
		t.Fatal("active old key no longer deduplicates")
	}
	if err := s.Close(); err != nil {
		t.Fatal("close failed")
	}
	reopened, err := Open(background, dir)
	must(t, reopened, err)
	defer reopened.Close()
	row, err := reopened.Lookup(background, current.ReservationID)
	if !reflect.DeepEqual(must(t, row, err), current) {
		t.Fatal("restart rejected renewed original admission")
	}
	// SQLite wall time is behind the watermark; renewal must not regress time.
	same, err := reopened.Renew(background, RenewInput{current.ReservationID, current.ExpiresAt})
	codeIs(t, err, CodeRenewalNoProgress)
	if same.ObservedAt != Millis(now+100*900000) {
		t.Fatal("clock regressed after restart")
	}
	advance(t, reopened, int64(current.ExpiresAt))
	closed, err := reopened.Renew(background, RenewInput{current.ReservationID, current.ExpiresAt})
	codeIs(t, err, CodeRenewalClosed)
	if closed.Reservation.State != Expired || *closed.Reservation.TerminalAt != current.ExpiresAt || *closed.Reservation.RetainUntil != current.ExpiresAt+Millis(KeyWindowMS) {
		t.Fatal("orphan expiry or retention used original deadline")
	}
	advance(t, reopened, int64(*closed.Reservation.RetainUntil)-1)
	out, err = reopened.Cleanup(background, true)
	if must(t, out, err).Pruned != 0 {
		t.Fatal("terminal renewed hold pruned early")
	}
	advance(t, reopened, int64(*closed.Reservation.RetainUntil))
	out, err = reopened.Cleanup(background, true)
	if must(t, out, err).Pruned != 1 {
		t.Fatal("terminal renewed hold not bounded")
	}
	_, err = reopened.Renew(background, RenewInput{current.ReservationID, current.ExpiresAt})
	codeIs(t, err, CodeEvidence)
	_, err = reopened.Reserve(background, in)
	codeIs(t, err, CodeKeyWindow)
	if balance(t, reopened, "a").Available != "100" {
		t.Fatal("expiry/pruning charged funds")
	}
}
