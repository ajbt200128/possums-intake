package website

import (
	"context"
	"strings"
	"testing"

	"possums-intake/internal/ledger"
)

func TestTopupBalanceFailureThenSessionFailureRetainsFunding(t *testing.T) {
	for _, panicValue := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "panic"}[panicValue], func(t *testing.T) {
			f := newAccountFixture(t)
			p := &topupFixture{result: TopupResult{Reference: strings.Repeat("a", 64), Status: "funded"}}
			f.host.payments = p
			if accountLogin(t, f.browser, keyHTTPA) != 303 {
				t.Fatal("SITE_TEST_TOPUP_BALANCE_SESSION_LOGIN")
			}
			form := paymentForm(t, f.browser)
			calls := 0
			f.host.balances = balanceReadFunc(func(context.Context, string) (ledger.BalanceSnapshot, error) {
				calls++
				panic("PRIVATE_BALANCE_PANIC")
			})
			f.host.sessions.Store = faultyStore{Store: f.host.store, operation: "commit", panicValue: panicValue}
			response, body := f.request(t, "POST", "/topup/start", form, f.server.URL)
			assertClosedHTTP(t, response, body, "SITE_SESSION_UNAVAILABLE", "original_funding_confirmed_not_availability")
			if calls != 1 || p.starts != 1 || p.reads != 0 || strings.Contains(body, "payment_creation_unconfirmed") || strings.Contains(body, p.result.Reference) || strings.Contains(body, "PRIVATE_") {
				t.Fatal("SITE_TEST_TOPUP_BALANCE_SESSION_LOST_FUNDING")
			}
		})
	}
}
