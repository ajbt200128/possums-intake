package ledger

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
)

func TestCleanupValidatesEachAccountOncePerTransaction(t *testing.T) {
	for _, tc := range []struct {
		name     string
		accounts []string
	}{
		{"single_account", []string{"a"}},
		{"interleaved_accounts", []string{"a", "b", "c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, now := testService(t)
			for i, id := range tc.accounts {
				a, err := s.Provision(background, id)
				must(t, a, err)
				c, err := s.Credit(background, CreditInput{id, "100", key(now, i+1)})
				must(t, c, err)
			}
			for i := 0; i < 6; i++ {
				reserve(t, s, ReserveInput{tc.accounts[i%len(tc.accounts)], "1", int64(i + 1), key(now, i+10)})
			}
			for i, id := range tc.accounts {
				reserve(t, s, ReserveInput{id, "1", 600, key(now, i+20)})
			}

			db := s.app.NonconcurrentDB().(*dbx.DB)
			original := db.QueryLogFunc
			defer func() { db.QueryLogFunc = original }()
			// Observe only synthetic query counts, never print or retain SQL/values.
			for pass, expectedExpired := range []int{6, len(tc.accounts)} {
				advance(t, s, now+[]int64{6000, 600000}[pass])
				accountReads := make([]int, len(tc.accounts))
				activeReads := make([]int, len(tc.accounts))
				db.QueryLogFunc = func(_ context.Context, _ time.Duration, statement string, _ *sql.Rows, _ error) {
					for i, id := range tc.accounts {
						if statement == "SELECT account_id,balance,enabled FROM ledger_accounts WHERE account_id='"+id+"'" {
							accountReads[i]++
						}
						if strings.HasPrefix(statement, "SELECT ") && strings.HasSuffix(statement, " FROM ledger_reservations WHERE account_id='"+id+"' AND state='active' ORDER BY reservation_id") {
							activeReads[i]++
						}
					}
				}
				out, err := s.Cleanup(background, false)
				db.QueryLogFunc = original
				if must(t, out, err) != (CleanupResult{Expired: expectedExpired}) {
					t.Fatal("cleanup changed the selected expiry population")
				}
				for i, id := range tc.accounts {
					if accountReads[i] != 1 || activeReads[i] != 1 {
						t.Fatalf("expected one account lookup and hold-set query; observed %d and %d", accountReads[i], activeReads[i])
					}
					b := balance(t, s, id)
					pending, available := 1, Money("99")
					if pass == 1 {
						pending, available = 0, "100"
					}
					if b.AccountBalance.Balance != "100" || b.Available != available || len(b.PendingReservations) != pending {
						t.Fatal("cleanup changed balance or expired an undued hold")
					}
				}
			}
		})
	}
}

func TestCleanupCorruptionCannotBeConcealedByExpiry(t *testing.T) {
	for _, corruption := range []string{"undued_active_row", "impossible_held_sum"} {
		for _, corruptAccount := range []string{"a", "b"} {
			t.Run(corruption+"/"+corruptAccount, func(t *testing.T) {
				s, _, now := testService(t)
				for i, id := range []string{"a", "b"} {
					a, err := s.Provision(background, id)
					must(t, a, err)
					c, err := s.Credit(background, CreditInput{id, "10", key(now, i+1)})
					must(t, c, err)
				}
				var holds []Reservation
				for i, id := range []string{"a", "a", "b", "b"} {
					holds = append(holds, reserve(t, s, ReserveInput{id, "2", int64(i + 1), key(now, i+10)}))
				}
				undued := reserve(t, s, ReserveInput{corruptAccount, "2", 600, key(now, 20)})
				holds = append(holds, undued)
				if err := s.run(background, func(tx *transaction) error {
					if corruption == "undued_active_row" {
						return tx.exec("UPDATE ledger_reservations SET idempotency_key='invalid' WHERE reservation_id={:id}", dbx.Params{"id": undued.ReservationID})
					}
					// The full hold sum is six; expiring even one due row would
					// conceal the impossible pre-expiry balance of five.
					return tx.exec("UPDATE ledger_accounts SET balance='5' WHERE account_id={:id}", dbx.Params{"id": corruptAccount})
				}, false); err != nil {
					t.Fatal("synthetic corruption fixture failed")
				}
				advance(t, s, now+4000)

				db := s.app.NonconcurrentDB().(*dbx.DB)
				original := db.ExecLogFunc
				defer func() { db.ExecLogFunc = original }()
				attemptedExpirations := 0
				db.ExecLogFunc = func(_ context.Context, _ time.Duration, statement string, _ sql.Result, err error) {
					if err == nil && strings.HasPrefix(statement, "UPDATE ledger_reservations SET state='expired',") {
						attemptedExpirations++
					}
				}
				out, err := s.Cleanup(background, false)
				db.ExecLogFunc = original
				codeIs(t, err, CodeCorrupt)
				if out != (CleanupResult{}) || s.Ready() {
					t.Fatal("corrupt cleanup returned a receipt or retained readiness")
				}
				expectedAttempts := 0
				if corruptAccount == "b" {
					expectedAttempts = 2
				}
				if attemptedExpirations != expectedAttempts {
					t.Fatalf("expected %d expiry writes before corruption; observed %d", expectedAttempts, attemptedExpirations)
				}
				// Bypass the now-unready Service only to inspect synthetic state;
				// no accounting API may heal rows before rollback is verified.
				for _, hold := range holds {
					var state string
					var charge *string
					var terminal, retain *int64
					err := s.app.DB().NewQuery("SELECT state,charged_amount,terminal_at,retain_until FROM ledger_reservations WHERE reservation_id={:id}").Bind(dbx.Params{"id": hold.ReservationID}).Row(&state, &charge, &terminal, &retain)
					if err != nil || state != "active" || charge != nil || terminal != nil || retain != nil {
						t.Fatal("corruption was concealed or earlier account expirations did not roll back")
					}
				}
				var total Money
				if err := s.app.DB().NewQuery("SELECT balance FROM ledger_accounts WHERE account_id={:id}").Bind(dbx.Params{"id": corruptAccount}).Row(&total); err != nil {
					t.Fatal("synthetic balance inspection failed")
				}
				expectedTotal := Money("10")
				if corruption == "impossible_held_sum" {
					expectedTotal = "5"
				}
				if total != expectedTotal {
					t.Fatal("cleanup repaired or changed the corrupt account balance")
				}
			})
		}
	}
}
