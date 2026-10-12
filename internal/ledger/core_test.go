package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
)

var background = context.Background()

func must[T any](t *testing.T, v T, err error) T {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected closed failure: %s", Failure(err).Code)
	}
	return v
}
func codeIs(t *testing.T, err error, code Code) {
	t.Helper()
	d, ok := err.(*Diagnostic)
	if !ok || d.Code != code {
		t.Fatalf("expected %s; observed %s", code, Failure(err).Code)
	}
}
func testService(t *testing.T) (*Service, string, int64) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	if err := Init(background, dir); err != nil {
		t.Fatalf("init: %s", Failure(err).Code)
	}
	s, err := Open(background, dir)
	if err != nil {
		t.Fatalf("open: %s", Failure(err).Code)
	}
	t.Cleanup(func() { s.Close() })
	// Only tests advance the durable watermark ahead of SQLite wall time. There
	// is no injectable production clock or runtime setting for this value.
	now := time.Now().Add(time.Hour).UnixMilli()
	advance(t, s, now)
	return s, dir, now
}
func advance(t *testing.T, s *Service, now int64) {
	t.Helper()
	if err := s.run(background, func(tx *transaction) error {
		return tx.exec("UPDATE ledger_meta SET clock_high_water_ms={:now} WHERE singleton=1", dbx.Params{"now": now})
	}, false); err != nil {
		t.Fatalf("advance: %s", Failure(err).Code)
	}
}
func key(at int64, nonce int) string { return fmt.Sprintf("v1.%013d.%032x", at, nonce) }
func fund(t *testing.T, s *Service, id string, amount Money, now int64) {
	t.Helper()
	a, e := s.Provision(background, id)
	must(t, a, e)
	c, e := s.Credit(background, CreditInput{id, amount, key(now, 1)})
	must(t, c, e)
}
func reserve(t *testing.T, s *Service, in ReserveInput) Reservation {
	t.Helper()
	r, e := s.Reserve(background, in)
	return must(t, r, e)
}
func balance(t *testing.T, s *Service, id string) BalanceSnapshot {
	t.Helper()
	b, e := s.Balance(background, id)
	return must(t, b, e)
}

func TestMoneyAndKeyBoundaries(t *testing.T) {
	for _, s := range []string{"0", "1", "18446744073709551615"} {
		m, e := ParseMoney(s)
		if e != nil || string(m) != s {
			t.Fatal("canonical amount rejected")
		}
	}
	for _, s := range []string{"", "00", "01", "+1", "-1", "1.0", "1e2", " 1", "1\n", "18446744073709551616", "999999999999999999999", "１２"} {
		_, e := ParseMoney(s)
		codeIs(t, e, CodeInput)
	}
	if _, err := add(math.MaxUint64, 1); err == nil {
		t.Fatal("overflow accepted")
	}
	now := int64(1_800_000_000_000)
	cases := []struct {
		issued int64
		valid  bool
	}{{now, true}, {now + 60000, true}, {now + 60001, false}, {now - KeyWindowMS + 1, true}, {now - KeyWindowMS, false}, {now - KeyWindowMS - 1, false}}
	for _, c := range cases {
		if (validNewKey(c.issued, now) == nil) != c.valid {
			t.Fatal("incorrect key boundary")
		}
	}
	for _, s := range []string{key(now, 2) + "\n", strings.Replace(key(now, 2), "v1.", "v2.", 1), "v1.1800000000000.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "v1.180000000000.00000000000000000000000000000000"} {
		_, e := keyIssued(s)
		codeIs(t, e, CodeKey)
	}
	for _, id := range []string{"", strings.Repeat("a", 129), "a\x00b", string([]byte{0xff})} {
		if validAccount(id) {
			t.Fatal("invalid account accepted")
		}
	}
	if !validAccount("opaque Unicode 🐾") {
		t.Fatal("valid opaque account rejected")
	}
}
func TestFullUint64OverflowAndRollback(t *testing.T) {
	s, _, now := testService(t)
	fund(t, s, "a", "18446744073709551615", now)
	c, e := s.Credit(background, CreditInput{"a", "1", key(now, 2)})
	codeIs(t, e, CodeOverflow)
	if c != (Credit{}) {
		t.Fatal("failure returned a receipt")
	}
	if b := balance(t, s, "a"); b.Available != "18446744073709551615" {
		t.Fatal("overflow changed balance")
	}
	var count int
	if err := s.run(background, func(tx *transaction) error {
		return tx.query("SELECT count(*) FROM ledger_credits WHERE idempotency_key={:key}", dbx.Params{"key": key(now, 2)}).Row(&count)
	}, true); err != nil || count != 0 {
		t.Fatal("overflow persisted marker")
	}
	r := reserve(t, s, ReserveInput{"a", "18446744073709551615", 600, key(now, 3)})
	if b := balance(t, s, "a"); b.Available != "0" || b.AccountBalance.Balance != "18446744073709551615" {
		t.Fatal("hold incorrectly debited total")
	}
	out, e := s.Settle(background, SettleInput{r.ReservationID, "18446744073709551615"})
	must(t, out, e)
	if balance(t, s, "a").AccountBalance.Balance != "0" {
		t.Fatal("full uint64 settlement incorrect")
	}
	// The failed credit identity has no marker and can succeed after capacity frees.
	c, e = s.Credit(background, CreditInput{"a", "1", key(now, 2)})
	if must(t, c, e).BalanceAfter != "1" {
		t.Fatal("rolled-back credit cannot retry")
	}
}
func TestIdempotencyOriginalResultsAndInputConflict(t *testing.T) {
	s, _, now := testService(t)
	fund(t, s, "a", "100", now)
	a, e := s.Provision(background, "b")
	must(t, a, e)
	first, e := s.Credit(background, CreditInput{"a", "10", key(now, 2)})
	must(t, first, e)
	later, e := s.Credit(background, CreditInput{"a", "20", key(now, 3)})
	must(t, later, e)
	repeat, e := s.Credit(background, CreditInput{"a", "10", key(now, 2)})
	must(t, repeat, e)
	if repeat != first || repeat.BalanceAfter != "110" {
		t.Fatal("credit retry returned later balance")
	}
	for _, in := range []CreditInput{{"a", "11", key(now, 2)}, {"b", "10", key(now, 2)}} {
		_, e := s.Credit(background, in)
		codeIs(t, e, CodeConflict)
	}
	in := ReserveInput{"a", "50", 600, key(now, 2)} // same key, distinct operation scope
	r := reserve(t, s, in)
	for _, bad := range []ReserveInput{{"a", "51", 600, in.IdempotencyKey}, {"a", "50", 601, in.IdempotencyKey}, {"b", "50", 600, in.IdempotencyKey}} {
		_, e := s.Reserve(background, bad)
		codeIs(t, e, CodeConflict)
	}
	advance(t, s, now+1000)
	if retry := reserve(t, s, in); !reflect.DeepEqual(retry, r) {
		t.Fatal("reserve retry moved expiry or identity")
	}
	if b := balance(t, s, "a"); b.Available != "80" {
		t.Fatal("duplicate effected money")
	}
	refund, e := s.Refund(background, r.ReservationID)
	must(t, refund, e)
	retry := reserve(t, s, in)
	if !reflect.DeepEqual(refund, retry) || retry.State != Refunded {
		t.Fatal("terminal retry recreated a hold")
	}
	provision, e := s.Provision(background, "a")
	if must(t, provision, e).Balance != "130" {
		t.Fatal("provision reset funded account")
	}
}
func TestLastCreditAndDuplicateRaces(t *testing.T) {
	s, _, now := testService(t)
	fund(t, s, "a", "100", now)
	var wg sync.WaitGroup
	results := make(chan error, 24)
	rows := make(chan Reservation, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, e := s.Reserve(background, ReserveInput{"a", "100", 600, key(now, i+2)})
			rows <- r
			results <- e
		}(i)
	}
	wg.Wait()
	close(results)
	close(rows)
	success := 0
	for e := range results {
		if e == nil {
			success++
		} else {
			codeIs(t, e, CodeInsufficient)
		}
	}
	if success != 1 {
		t.Fatal("last credit admitted more or fewer than one hold")
	}
	var winner Reservation
	for r := range rows {
		if r.State == Active {
			winner = r
		}
	}
	out, e := s.Refund(background, winner.ReservationID)
	must(t, out, e)
	duplicate := ReserveInput{"a", "100", 600, key(now, 100)}
	rows = make(chan Reservation, 24)
	results = make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, e := s.Reserve(background, duplicate); rows <- r; results <- e }()
	}
	wg.Wait()
	close(rows)
	close(results)
	var id string
	for e := range results {
		if e != nil {
			t.Fatalf("duplicate race: %s", Failure(e).Code)
		}
	}
	for r := range rows {
		if id == "" {
			id = r.ReservationID
		}
		if r.ReservationID != id {
			t.Fatal("duplicate created distinct identities")
		}
	}
	if len(balance(t, s, "a").PendingReservations) != 1 {
		t.Fatal("duplicate created multiple holds")
	}
	// Concurrent exact credit retries also add once and return one original result.
	results = make(chan error, 24)
	creditRows := make(chan Credit, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, e := s.Credit(background, CreditInput{"a", "1", key(now, 101)})
			creditRows <- c
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	close(creditRows)
	for e := range results {
		if e != nil {
			t.Fatalf("credit race: %s", Failure(e).Code)
		}
	}
	for c := range creditRows {
		if c.BalanceAfter != "101" {
			t.Fatal("duplicate credit applied again")
		}
	}
	if balance(t, s, "a").AccountBalance.Balance != "101" {
		t.Fatal("credit race total incorrect")
	}
}
func TestTerminalRacesDisabledAndExactExpiry(t *testing.T) {
	s, _, now := testService(t)
	fund(t, s, "a", "100", now)
	r := reserve(t, s, ReserveInput{"a", "50", 600, key(now, 2)})
	if err := s.run(background, func(tx *transaction) error {
		return tx.exec("UPDATE ledger_accounts SET enabled=0 WHERE account_id='a'", nil)
	}, true); err != nil {
		t.Fatal("disable failed")
	}
	_, e := s.Reserve(background, ReserveInput{"a", "1", 600, key(now, 3)})
	codeIs(t, e, CodeDisabled)
	a, e := s.Provision(background, "a")
	if must(t, a, e).Enabled {
		t.Fatal("provision reenabled account")
	}
	var wg sync.WaitGroup
	rows := make(chan Reservation, 20)
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var row Reservation
			var e error
			if i%2 == 0 {
				row, e = s.Settle(background, SettleInput{r.ReservationID, money(uint64(i))})
			} else {
				row, e = s.Refund(background, r.ReservationID)
			}
			rows <- row
			errs <- e
		}(i)
	}
	wg.Wait()
	close(rows)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatalf("terminal race: %s", Failure(e).Code)
		}
	}
	var terminal Reservation
	for row := range rows {
		if terminal.State == "" {
			terminal = row
		}
		if !reflect.DeepEqual(row, terminal) {
			t.Fatal("terminal attempts returned different outcomes")
		}
	}
	charge, _ := terminal.ChargedAmount.units()
	if balance(t, s, "a").AccountBalance.Balance != money(100-charge) {
		t.Fatal("more than one terminal debit")
	}
	// Re-enable only in synthetic fixture, then test expiry precedence.
	if e := s.run(background, func(tx *transaction) error {
		return tx.exec("UPDATE ledger_accounts SET enabled=1 WHERE account_id='a'", nil)
	}, true); e != nil {
		t.Fatal("fixture enable failed")
	}
	due := reserve(t, s, ReserveInput{"a", "20", 1, key(now, 4)})
	advance(t, s, int64(due.ExpiresAt)-1)
	before, e := s.Lookup(background, due.ReservationID)
	if must(t, before, e).State != Active {
		t.Fatal("expired a millisecond early")
	}
	advance(t, s, int64(due.ExpiresAt))
	expired, e := s.Settle(background, SettleInput{due.ReservationID, "999"})
	codeIs(t, e, CodeExpired)
	if expired.State != Expired || *expired.TerminalAt != due.ExpiresAt || *expired.ChargedAmount != "0" {
		t.Fatal("late settlement has no authoritative expiry")
	}
	refund, e := s.Refund(background, due.ReservationID)
	if !reflect.DeepEqual(must(t, refund, e), expired) {
		t.Fatal("refund changed expired outcome")
	}
	b := balance(t, s, "a")
	if len(b.PendingReservations) != 0 || b.Available != b.AccountBalance.Balance {
		t.Fatal("expiry retained capacity")
	}
}
func TestSnapshotCompleteSortedScopedAndExpiry(t *testing.T) {
	s, _, now := testService(t)
	fund(t, s, "a", "100", now)
	a, e := s.Provision(background, "b")
	must(t, a, e)
	c, e := s.Credit(background, CreditInput{"b", "5", key(now, 999)})
	must(t, c, e)
	for i := 0; i < 40; i++ {
		reserve(t, s, ReserveInput{"a", "1", 600, key(now, i+2)})
	}
	other := reserve(t, s, ReserveInput{"b", "5", 600, key(now, 1000)})
	due := reserve(t, s, ReserveInput{"a", "10", 1, key(now, 1001)})
	b := balance(t, s, "a")
	if len(b.PendingReservations) != 41 || b.Available != "50" || b.AccountBalance.Balance != "100" {
		t.Fatal("incomplete snapshot")
	}
	ids := make([]string, 0)
	for _, row := range b.PendingReservations {
		if row.AccountID != "a" || row.ReservationID == other.ReservationID {
			t.Fatal("cross-account snapshot")
		}
		ids = append(ids, row.ReservationID)
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatal("unsorted snapshot")
	}
	advance(t, s, int64(due.ExpiresAt))
	b = balance(t, s, "a")
	if len(b.PendingReservations) != 40 || b.Available != "60" {
		t.Fatal("snapshot did not atomically expire hold")
	}
	r, e := s.Lookup(background, due.ReservationID)
	if must(t, r, e).State != Expired {
		t.Fatal("snapshot ignored hold without terminalizing")
	}
	encoded, e := json.Marshal(b)
	if e != nil || !strings.Contains(string(encoded), `"available":"60"`) {
		t.Fatal("money wire encoding")
	}
	stamp, e := json.Marshal(Millis(1_800_000_000_123))
	if e != nil || string(stamp) != `"2027-01-15T08:00:00.123Z"` {
		t.Fatal("timestamp wire encoding")
	}
}
func TestKeyPruningRetentionRollbackClockAndRestart(t *testing.T) {
	s, dir, now := testService(t)
	fund(t, s, "a", "100", now)
	future := now + FutureSkewMS
	in := ReserveInput{"a", "10", 1, key(future, 2)}
	r := reserve(t, s, in)
	c, e := s.Credit(background, CreditInput{"a", "5", key(future, 3)})
	must(t, c, e)
	advance(t, s, int64(r.ExpiresAt)+10000) // cleanup delayed; terminal time remains deadline
	out, e := s.Cleanup(background, false)
	if must(t, out, e).Expired != 1 {
		t.Fatal("cleanup failed expiry")
	}
	r, e = s.Lookup(background, r.ReservationID)
	must(t, r, e)
	retain := future + KeyWindowMS
	if int64(*r.RetainUntil) != retain || int64(c.RetainUntil) != retain || *r.TerminalAt != r.ExpiresAt {
		t.Fatal("future-skew or delayed-expiry retention incorrect")
	}
	advance(t, s, retain-1)
	out, e = s.Cleanup(background, true)
	if must(t, out, e).Pruned != 1 {
		t.Fatal("only the earlier funding marker should be pruned")
	}
	retry := reserve(t, s, in)
	if retry.State != Expired {
		t.Fatal("expired-key retained retry denied or recreated")
	}
	again, e := s.Credit(background, CreditInput{"a", "5", key(future, 3)})
	if must(t, again, e) != c {
		t.Fatal("expired key did not recover original credit")
	}
	advance(t, s, retain)
	out, e = s.Cleanup(background, true)
	if must(t, out, e).Pruned != 2 {
		t.Fatal("exact boundary prune count incorrect")
	}
	_, e = s.Reserve(background, in)
	codeIs(t, e, CodeKeyWindow)
	_, e = s.Credit(background, CreditInput{"a", "5", key(future, 3)})
	codeIs(t, e, CodeKeyWindow)
	_, e = s.Lookup(background, r.ReservationID)
	codeIs(t, e, CodeEvidence)
	before := balance(t, s, "a").AccountBalance.Balance
	if e := s.Close(); e != nil {
		t.Fatal("close failed")
	}
	reopened, e := Open(background, dir)
	must(t, reopened, e)
	defer reopened.Close()
	// Actual SQLite wall clock is now behind the committed/pruned watermark.
	// Reopening uses max(wall, durable watermark), never resetting it.
	_, e = reopened.Reserve(background, in)
	codeIs(t, e, CodeKeyWindow)
	_, e = reopened.Credit(background, CreditInput{"a", "5", key(future, 3)})
	codeIs(t, e, CodeKeyWindow)
	if balance(t, reopened, "a").AccountBalance.Balance != before {
		t.Fatal("restart or clock rollback replayed money")
	}
	var high int64
	if e := reopened.run(background, func(tx *transaction) error {
		return tx.query("SELECT clock_high_water_ms FROM ledger_meta WHERE singleton=1", nil).Row(&high)
	}, true); e != nil || high != retain {
		t.Fatal("watermark lost across restart")
	}
}
func TestNewKeyAdmissionBoundariesAndFailedReserveRetry(t *testing.T) {
	s, _, now := testService(t)
	fund(t, s, "a", "100", now)
	for i, c := range []struct {
		at int64
		ok bool
	}{{now - KeyWindowMS, false}, {now - KeyWindowMS + 1, true}, {now + 60000, true}, {now + 60001, false}} {
		r, e := s.Reserve(background, ReserveInput{"a", "1", 1, key(c.at, i+2)})
		if c.ok {
			must(t, r, e)
		} else {
			codeIs(t, e, CodeKeyWindow)
		}
	}
	attempt := ReserveInput{"a", "101", 600, key(now, 20)}
	_, e := s.Reserve(background, attempt)
	codeIs(t, e, CodeInsufficient)
	c, e := s.Credit(background, CreditInput{"a", "10", key(now, 21)})
	must(t, c, e)
	r := reserve(t, s, attempt)
	if r.Maximum != "101" {
		t.Fatal("failed identity could not retry")
	}
	// Altering timestamp produces a distinct key, not a replay receipt.
	changed := attempt
	changed.IdempotencyKey = key(now+1, 20)
	_, e = s.Reserve(background, changed)
	codeIs(t, e, CodeInsufficient)
}
func TestBackdatedActiveRetryAfterKeyValidityEnds(t *testing.T) {
	s, _, now := testService(t)
	fund(t, s, "a", "100", now)
	in := ReserveInput{"a", "10", 600, key(now-KeyWindowMS+1, 2)}
	original := reserve(t, s, in)
	advance(t, s, now+2)
	if retry := reserve(t, s, in); !reflect.DeepEqual(original, retry) {
		t.Fatal("retained active hold was rejected after key validity ended")
	}
	bad := in
	bad.TimeoutSeconds = 601
	_, e := s.Reserve(background, bad)
	codeIs(t, e, CodeConflict)
	advance(t, s, int64(original.ExpiresAt))
	expired := reserve(t, s, in)
	if expired.State != Expired || expired.ExpiresAt != original.ExpiresAt || *expired.RetainUntil != Millis(int64(original.ExpiresAt)+KeyWindowMS) {
		t.Fatal("backdated key expiry/retention reset original hold")
	}
}

func TestSnapshotRacingSettlementNeverReturnsMixedState(t *testing.T) {
	s, _, now := testService(t)
	fund(t, s, "a", "100", now)
	r := reserve(t, s, ReserveInput{"a", "50", 600, key(now, 2)})
	var wg sync.WaitGroup
	snapshots := make(chan BalanceSnapshot, 20)
	errors := make(chan error, 21)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); b, e := s.Balance(background, "a"); snapshots <- b; errors <- e }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); _, e := s.Settle(background, SettleInput{r.ReservationID, "17"}); errors <- e }()
	wg.Wait()
	close(snapshots)
	close(errors)
	for e := range errors {
		if e != nil {
			t.Fatalf("snapshot race: %s", Failure(e).Code)
		}
	}
	for b := range snapshots {
		switch len(b.PendingReservations) {
		case 1:
			if b.AccountBalance.Balance != "100" || b.Available != "50" || b.PendingReservations[0].State != Active {
				t.Fatal("mixed pre-settlement snapshot")
			}
		case 0:
			if b.AccountBalance.Balance != "83" || b.Available != "83" {
				t.Fatal("mixed post-settlement snapshot")
			}
		default:
			t.Fatal("unexpected snapshot population")
		}
	}
}

func TestUnknownAndInvalidInputs(t *testing.T) {
	s, _, now := testService(t)
	_, e := s.Balance(background, "absent")
	codeIs(t, e, CodeAccount)
	_, e = s.Lookup(background, strings.Repeat("a", 32))
	codeIs(t, e, CodeEvidence)
	_, e = s.Settle(background, SettleInput{strings.Repeat("a", 32), "0"})
	codeIs(t, e, CodeEvidence)
	_, e = s.Credit(background, CreditInput{"absent", "1", key(now, 2)})
	codeIs(t, e, CodeAccount)
	for _, timeout := range []int64{0, 1801, -1} {
		_, e = s.Reserve(background, ReserveInput{"a", "1", timeout, key(now, 3)})
		codeIs(t, e, CodeInput)
	}
	for _, amount := range []Money{"0", "01", "18446744073709551616"} {
		_, e = s.Credit(background, CreditInput{"a", amount, key(now, 4)})
		codeIs(t, e, CodeInput)
	}
	if Failure(errors.New("SYNTHETIC_SECRET_PAYLOAD")).Code != CodeCommit {
		t.Fatal("unexpected error fallback")
	}
	payload, _ := json.Marshal(Failure(errors.New("SYNTHETIC_SECRET_PAYLOAD")))
	if strings.Contains(string(payload), "SYNTHETIC_SECRET_PAYLOAD") {
		t.Fatal("raw error escaped")
	}
}
