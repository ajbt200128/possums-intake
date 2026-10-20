package website

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

func TestAccountSnapshotSettlementAndExpiryRaces(t *testing.T) {
	for _, mode := range []string{"settlement", "expiry"} {
		t.Run(mode, func(t *testing.T) {
			f := newAccountFixture(t)
			ctx := context.Background()
			if _, err := f.service.Credit(ctx, ledger.CreditInput{AccountID: accountHTTPA, Amount: "100", IdempotencyKey: accountingKey(10)}); err != nil {
				t.Fatal("SITE_TEST_RACE_CREDIT")
			}
			timeout := int64(60)
			if mode == "expiry" {
				timeout = 1
			}
			row, err := f.service.Reserve(ctx, ledger.ReserveInput{AccountID: accountHTTPA, Maximum: "9", TimeoutSeconds: timeout, IdempotencyKey: accountingKey(11)})
			if err != nil || accountLogin(t, f.browser, keyHTTPA) != 303 {
				t.Fatal("SITE_TEST_RACE_RESERVE_LOGIN")
			}
			if mode == "expiry" {
				timer := time.NewTimer(time.Until(time.UnixMilli(int64(row.ExpiresAt))) + 2*time.Millisecond)
				defer timer.Stop()
				<-timer.C
			}
			settled := make(chan error, 1)
			go func() {
				_, err := f.service.Settle(ctx, ledger.SettleInput{ReservationID: row.ReservationID, ChargedAmount: "3"})
				settled <- err
			}()
			before := f.counter.calls.Load()
			response, body := f.request(t, "GET", "/home", nil, "")
			settlementErr := <-settled
			if response.StatusCode != 200 || f.counter.calls.Load() != before+1 {
				t.Fatal("SITE_TEST_RACE_SINGLE_SNAPSHOT")
			}
			text := html.UnescapeString(body)
			if mode == "expiry" {
				var d *ledger.Diagnostic
				if !errors.As(settlementErr, &d) || d.Code != ledger.CodeExpired || !strings.Contains(text, "Balance (microunits): 100") || !strings.Contains(text, "Available (microunits): 100") || strings.Contains(text, "reservation_id=") {
					t.Fatal("SITE_TEST_EXPIRY_SNAPSHOT_OR_LATE_SETTLEMENT")
				}
			} else {
				prior := strings.Contains(text, "Balance (microunits): 100") && strings.Contains(text, "Available (microunits): 91") && strings.Count(text, "reservation_id=") == 1
				after := strings.Contains(text, "Balance (microunits): 97") && strings.Contains(text, "Available (microunits): 97") && !strings.Contains(text, "reservation_id=")
				if settlementErr != nil || (!prior && !after) {
					t.Fatal("SITE_TEST_SETTLEMENT_MIXED_SNAPSHOT")
				}
				_, body = f.request(t, "GET", "/home", nil, "")
				if !strings.Contains(body, "Available (microunits): 97") || strings.Contains(body, "reservation_id=") {
					t.Fatal("SITE_TEST_SETTLED_CURRENT_SNAPSHOT")
				}
			}
		})
	}
}

func TestAccountDisabledSpendingStillHasCompleteRead(t *testing.T) {
	f := newAccountFixture(t)
	record, err := f.sdk.FindFirstRecordByData("ledger_accounts", "account_id", accountHTTPA)
	if err != nil {
		t.Fatal("SITE_TEST_DISABLED_ACCOUNT_LOOKUP")
	}
	record.Set("enabled", false)
	if f.sdk.Save(record) != nil || accountLogin(t, f.browser, keyHTTPA) != 303 {
		t.Fatal("SITE_TEST_DISABLED_ACCOUNT_LOGIN")
	}
	response, body := f.request(t, "GET", "/home", nil, "")
	if response.StatusCode != 200 || !strings.Contains(body, "Enabled: false") || !strings.Contains(body, "Available (microunits): 0") || strings.Contains(body, "reservation_id=") {
		t.Fatal("SITE_TEST_DISABLED_ACCOUNT_READ")
	}
}

func TestAccountWholeListLimitHTTPDoesNotExposePartialData(t *testing.T) {
	f := newAccountFixture(t)
	// Inject a complete synthetic oversized response at the presentation boundary;
	// this is not a funding/financial-capacity fixture or a runtime fallback.
	snapshot := ledger.BalanceSnapshot{AccountBalance: ledger.Account{AccountID: accountHTTPA, Balance: "400", Enabled: true}, Available: "0"}
	for i := range 400 {
		snapshot.PendingReservations = append(snapshot.PendingReservations, ledger.Reservation{ReservationID: fmt.Sprintf("%032x", i), IdempotencyKey: accountingKey(i + 20), AccountID: accountHTTPA, Maximum: "1", TimeoutSeconds: 60, ExpiresAt: ledger.Millis(time.Now().Add(time.Minute).UnixMilli()), State: ledger.Active})
	}
	var calls atomic.Int64
	f.host.balances = balanceReadFunc(func(context.Context, string) (ledger.BalanceSnapshot, error) { calls.Add(1); return snapshot, nil })
	if accountLogin(t, f.browser, keyHTTPA) != 303 {
		t.Fatal("SITE_TEST_LARGE_ACCOUNT_LOGIN")
	}
	failures := len(f.observer.counts())
	before := calls.Load()
	response, body := f.request(t, "GET", "/home", nil, "")
	kinds := f.observer.counts()
	if response.StatusCode != 503 || calls.Load() != before+1 || !strings.Contains(body, "SITE_ACCOUNT_VIEW_LIMIT") || strings.Contains(body, "SYNTHETIC_A") || strings.Contains(body, "Balance (microunits)") || strings.Contains(body, "reservation_id=") || len(kinds) != failures+1 || kinds[len(kinds)-1] != failuretype.PolicyRefused {
		t.Fatal("SITE_TEST_WHOLE_LIST_LIMIT_HTTP")
	}
}
