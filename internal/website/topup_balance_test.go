package website

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

func assertTopupBalance(t *testing.T, body, balance, available string) {
	t.Helper()
	if !strings.Contains(body, "Current balance: "+balance+" USD") || !strings.Contains(body, "Available: "+available+" USD") || strings.Contains(body, "Current balance is unavailable") {
		t.Fatal("SITE_TEST_TOPUP_CURRENT_TOTALS")
	}
	for _, forbidden := range []string{"SYNTHETIC_", "PRIVATE_", "Pending holds:", "Reservation:", "Request key:", "account-snapshot", "Enabled", "microunits"} {
		if strings.Contains(body, forbidden) {
			t.Fatal("SITE_TEST_TOPUP_SUMMARY_PRIVACY")
		}
	}
}

func TestTopupCurrentBalanceHTTPManualChecks(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	for i, account := range []string{accountHTTPA, accountHTTPB} {
		amount := []ledger.Money{"1000000", "17000000"}[i]
		if _, err := f.service.Credit(ctx, ledger.CreditInput{AccountID: account, Amount: amount, IdempotencyKey: accountingKey(i + 1)}); err != nil {
			t.Fatal("SITE_TEST_TOPUP_INITIAL_CREDIT")
		}
	}
	p := &topupFixture{result: TopupResult{Reference: strings.Repeat("a", 64), URL: "http://127.0.0.1:8099/c/pay/synthetic", Status: "awaiting_payment"}}
	f.host.payments = p
	if accountLogin(t, f.browser, keyHTTPA) != 303 {
		t.Fatal("SITE_TEST_TOPUP_BALANCE_LOGIN")
	}
	before := f.counter.calls.Load()
	for _, method := range []string{"GET", "HEAD"} {
		response, body := f.request(t, method, "/topup", nil, "")
		if response.StatusCode != 200 || strings.Contains(body, "Current balance") || strings.Contains(body, "Available:") {
			t.Fatal("SITE_TEST_TOPUP_FORM_NO_BALANCE_BLOCK")
		}
	}
	form := paymentForm(t, f.browser) // usd10 is not the authoritative balance
	if f.counter.calls.Load() != before {
		t.Fatal("SITE_TEST_TOPUP_FORM_EXTRA_READ")
	}
	response, _ := f.request(t, "POST", "/topup/start", form, f.server.URL)
	if response.StatusCode != 303 || p.starts != 1 || f.counter.calls.Load() != before {
		t.Fatal("SITE_TEST_TOPUP_CREATE_ONCE_NO_BALANCE_READ")
	}
	response, body := f.request(t, "GET", "/topup/return", nil, "")
	if response.StatusCode != 200 || !strings.Contains(body, "payment and account funding are not confirmed") || f.counter.calls.Load() != before+1 || p.reads != 1 {
		t.Fatal("SITE_TEST_TOPUP_AWAITING_SNAPSHOT_ONCE")
	}
	assertTopupBalance(t, body, "$1.00", "$1.00")
	// Independent ledger activity, not a denomination-derived payment receipt.
	if _, err := f.service.Credit(ctx, ledger.CreditInput{AccountID: accountHTTPA, Amount: "4000000", IdempotencyKey: accountingKey(3)}); err != nil {
		t.Fatal("SITE_TEST_TOPUP_INDEPENDENT_CREDIT")
	}
	if _, err := f.service.Reserve(ctx, ledger.ReserveInput{AccountID: accountHTTPA, Maximum: "2000000", TimeoutSeconds: 600, IdempotencyKey: accountingKey(4)}); err != nil {
		t.Fatal("SITE_TEST_TOPUP_INDEPENDENT_HOLD")
	}
	p.result.Status = "funded"
	response, body = f.request(t, "GET", "/topup/status", nil, "")
	if response.StatusCode != 200 || !strings.Contains(body, "Credit was added once") || f.counter.calls.Load() != before+2 || p.starts != 1 || p.reads != 2 || p.account != accountHTTPA || strings.Contains(body, "$10.00") || strings.Contains(body, "$17.00") {
		t.Fatal("SITE_TEST_TOPUP_MANUAL_FRESH_SNAPSHOT")
	}
	assertTopupBalance(t, body, "$5.00", "$3.00")

	other := *f.browser
	client := *f.server.Client()
	other.client = &client
	other.client.Jar, _ = cookiejar.New(nil)
	other.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if accountLogin(t, &other, keyHTTPB) != 303 {
		t.Fatal("SITE_TEST_TOPUP_OTHER_LOGIN")
	}
	before = f.counter.calls.Load()
	response, body = other.request(t, "GET", "/topup/status", nil, "")
	if response.StatusCode != 200 || !strings.Contains(body, "No purchase reference") || f.counter.calls.Load() != before+1 || p.starts != 1 || p.reads != 2 || strings.Contains(body, "$5.00") || strings.Contains(body, "$3.00") {
		t.Fatal("SITE_TEST_TOPUP_OTHER_ACCOUNT_ISOLATION")
	}
	assertTopupBalance(t, body, "$17.00", "$17.00")
}

func TestTopupBalanceFailuresPreserveFundedHTTP(t *testing.T) {
	for _, mode := range []string{"account", "row_account", "row_inactive", "balance", "available", "error", "panic", "reported", "missing"} {
		t.Run(mode, func(t *testing.T) {
			f := newAccountFixture(t)
			p := &topupFixture{result: TopupResult{Reference: strings.Repeat("a", 64), Status: "funded"}}
			f.host.payments = p
			if accountLogin(t, f.browser, keyHTTPA) != 303 {
				t.Fatal("SITE_TEST_TOPUP_FAILURE_LOGIN")
			}
			form := paymentForm(t, f.browser)
			calls := 0
			f.host.balances = balanceReadFunc(func(ctx context.Context, account string) (ledger.BalanceSnapshot, error) {
				calls++
				if account != accountHTTPA {
					t.Error("SITE_TEST_TOPUP_SERVER_BOUND_ACCOUNT")
				}
				snapshot := snapshotFixture(account)
				switch mode {
				case "account":
					snapshot.AccountBalance.AccountID = "PRIVATE_FOREIGN_ACCOUNT"
				case "row_account":
					snapshot.PendingReservations[0].AccountID = "PRIVATE_FOREIGN_ROW"
				case "row_inactive":
					snapshot.PendingReservations[0].State = ledger.State("PRIVATE_STATE")
				case "balance":
					snapshot.AccountBalance.Balance = "01"
				case "available":
					snapshot.Available = "PRIVATE_MONEY"
				case "error":
					return ledger.BalanceSnapshot{}, errors.New("PRIVATE_READ_ERROR")
				case "panic":
					panic("PRIVATE_READ_PANIC")
				case "reported":
					return f.service.Balance(ctx, "missing00000000")
				}
				return snapshot, nil
			})
			if mode == "missing" {
				f.host.balances = nil
			}
			for i, path := range []string{"/topup/start", "/topup/status", "/topup/return"} {
				method := "GET"
				var response *http.Response
				var body string
				before := len(f.observer.counts())
				if i == 0 {
					method = "POST"
					response, body = f.request(t, method, path, form, f.server.URL)
				} else {
					response, body = f.request(t, method, path, nil, "")
				}
				kinds := f.observer.counts()
				want := failuretype.NativeReadUnknown
				if mode == "reported" {
					want = failuretype.AccountMissing
				}
				if response.StatusCode != 200 || !strings.Contains(body, "Credit was added once") || !strings.Contains(body, "Current balance is unavailable. Open your account page or check again; your payment was not retried.") || len(kinds) != before+1 || kinds[before] != want || p.starts != 1 || p.reads != i {
					t.Fatal("SITE_TEST_TOPUP_BALANCE_FAILURE_OWNED_ONCE")
				}
				for _, forbidden := range []string{"PRIVATE_", "SYNTHETIC_", "missing00000000", "Current balance:", "Available:", "$0.00", p.result.Reference, "payment_creation_unconfirmed"} {
					if strings.Contains(body, forbidden) {
						t.Fatal("SITE_TEST_TOPUP_BALANCE_FAILURE_PRIVACY")
					}
				}
			}
			wantCalls := 3
			if mode == "missing" {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatal("SITE_TEST_TOPUP_BALANCE_FAILURE_NO_REPLAY")
			}
		})
	}
}

func TestTopupBalanceMissingPrincipalPreservesOutcome(t *testing.T) {
	revision := prepared(t)
	observer := &numericObserver{}
	calls := 0
	host := &Host{observer: observer, balances: balanceReadFunc(func(context.Context, string) (ledger.BalanceSnapshot, error) {
		calls++
		return snapshotFixture("PRIVATE_ACCOUNT"), nil
	})}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "https://fixture.invalid/topup/status", nil)
	request = request.WithContext(context.WithValue(request.Context(), revisionKey{}, revision))
	state, request := host.ownResponse(recorder, request)
	setOutcome(request, "original_funding_confirmed_not_availability")
	host.topupRender(state, request, false, "funded", "")
	body := recorder.Body.String()
	if recorder.Code != 200 || !strings.Contains(body, "Credit was added once") || !strings.Contains(body, "Current balance is unavailable") || strings.Contains(body, "Current balance:") || calls != 0 || state.outcome != "original_funding_confirmed_not_availability" || len(observer.counts()) != 1 || observer.counts()[0] != failuretype.NativeReadUnknown {
		t.Fatal("SITE_TEST_TOPUP_MISSING_PRINCIPAL_OUTCOME")
	}
}

func TestTopupBalanceSummaryIndependentOfHoldTextBudget(t *testing.T) {
	f := newAccountFixture(t)
	p := &topupFixture{result: TopupResult{Reference: strings.Repeat("a", 64), Status: "funded"}}
	f.host.payments = p
	if accountLogin(t, f.browser, keyHTTPA) != 303 {
		t.Fatal("SITE_TEST_TOPUP_BUDGET_LOGIN")
	}
	form := paymentForm(t, f.browser)
	snapshot := snapshotFixture(accountHTTPA)
	snapshot.AccountBalance.Balance, snapshot.Available = "5000000", "3000000"
	snapshot.PendingReservations[0].ReservationID = "PRIVATE_ROW_ID"
	snapshot.PendingReservations[0].IdempotencyKey = "PRIVATE_ROW_KEY" + strings.Repeat("x", maxTextBytes)
	if view, err := snapshotView(accountHTTPA, snapshot); view != nil || err == nil {
		t.Fatal("SITE_TEST_TOPUP_FULL_HOLDS_EXCEED_BUDGET")
	}
	view, err := snapshotBalanceView(accountHTTPA, snapshot)
	if err != nil || len(view) != 2 || view["balance-usd"] != "$5.00" || view["available-usd"] != "$3.00" {
		t.Fatal("SITE_TEST_TOPUP_BOUNDED_SUMMARY_ONLY")
	}
	calls := 0
	f.host.balances = balanceReadFunc(func(context.Context, string) (ledger.BalanceSnapshot, error) {
		calls++
		return snapshot, nil
	})
	response, body := f.request(t, "POST", "/topup/start", form, f.server.URL)
	if response.StatusCode != 200 || p.starts != 1 || p.reads != 0 || calls != 1 {
		t.Fatal("SITE_TEST_TOPUP_SUMMARY_WITH_LARGE_HOLDS")
	}
	assertTopupBalance(t, body, "$5.00", "$3.00")
	// Scope validation still includes every row even after oversized hold text.
	snapshot.PendingReservations = append(snapshot.PendingReservations, snapshot.PendingReservations[0])
	snapshot.PendingReservations[1].AccountID = "PRIVATE_FOREIGN_ACCOUNT"
	if view, err := snapshotBalanceView(accountHTTPA, snapshot); view != nil || err == nil {
		t.Fatal("SITE_TEST_TOPUP_ALL_ROWS_SCOPED")
	}
	response, body = f.request(t, "GET", "/topup/status", nil, "")
	if response.StatusCode != 200 || !strings.Contains(body, "Credit was added once") || !strings.Contains(body, "Current balance is unavailable") || strings.Contains(body, "Current balance:") || strings.Contains(body, "PRIVATE_") || calls != 2 {
		t.Fatal("SITE_TEST_TOPUP_OVERSIZED_FOREIGN_ROW_WITHHELD")
	}
}
