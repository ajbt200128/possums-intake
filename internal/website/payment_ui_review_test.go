package website

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"possums-intake/internal/ledger"
)

func TestAccountHostLinkUsesPublishedLoginDestination(t *testing.T) {
	for _, policy := range []string{"session", "public"} {
		t.Run(policy, func(t *testing.T) {
			bundle := Fixture()
			bundle["routes.el"] = strings.Replace(bundle["routes.el"], `"/home" 'session`, `"/welcome" '`+policy, 1)
			r, err := Prepare(context.Background(), bundle)
			if err != nil {
				t.Fatal("SITE_TEST_RENAMED_ACCOUNT_PREPARE")
			}
			page, err := r.PageWithView(context.Background(), "topup-page", map[string]string{"payment-state": "no-reference"})
			if err != nil {
				t.Fatal("SITE_TEST_RENAMED_ACCOUNT_PAGE")
			}
			body, err := r.render(page, "csrf", "", hostBindings{paymentsEnabled: true})
			if policy == "public" {
				if err == nil || body != nil {
					t.Fatal("SITE_TEST_ACCOUNT_LINK_REQUIRES_SESSION_ROUTE")
				}
				return
			}
			if err != nil || !strings.Contains(string(body), `<a href="/welcome">Account</a>`) || strings.Contains(string(body), `href="/home"`) {
				t.Fatal("SITE_TEST_ACCOUNT_LINK_PUBLISHED_DESTINATION")
			}
		})
	}
}

func TestCompleteAccountPageTextLimitHasActionableDiagnostic(t *testing.T) {
	f := newAccountFixture(t)
	if accountLogin(t, f.browser, keyHTTPA) != http.StatusSeeOther {
		t.Fatal("SITE_TEST_ACCOUNT_PAGE_LIMIT_LOGIN")
	}
	snapshot := snapshotFixture(accountHTTPA)
	view, err := snapshotView(accountHTTPA, snapshot)
	if err != nil {
		t.Fatal("SITE_TEST_ACCOUNT_PAGE_LIMIT_FIXTURE")
	}
	total := 0
	for key, value := range view {
		total += len(key) + len(value)
	}
	snapshot.PendingReservations[0].IdempotencyKey += strings.Repeat("x", maxTextBytes-total)
	view, err = snapshotView(accountHTTPA, snapshot)
	if err != nil {
		t.Fatal("SITE_TEST_ACCOUNT_PAGE_LIMIT_COMPLETE_VIEW")
	}
	_, err = f.host.revision.PageWithView(context.Background(), "signed-home", view)
	if err == nil || renderHTTPFault(err).Code != "SITE_PAGE_TEXT_LIMIT" {
		t.Fatal("SITE_TEST_ACCOUNT_PAGE_LIMIT_KNOWN_CAUSE")
	}
	f.host.balances = balanceReadFunc(func(context.Context, string) (ledger.BalanceSnapshot, error) { return snapshot, nil })
	response, body := f.request(t, "GET", "/home", nil, "")
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "SITE_ACCOUNT_VIEW_LIMIT") || !strings.Contains(body, "ask the operator") || strings.Contains(body, "SITE_RENDER_UNAVAILABLE") || strings.Contains(body, snapshot.PendingReservations[0].IdempotencyKey) {
		t.Fatal("SITE_TEST_ACCOUNT_PAGE_LIMIT_ACTIONABLE_PRIVATE")
	}
}

func TestTopupHostFormTimeGuardsBeforeAction(t *testing.T) {
	for _, boundary := range []string{"future-issued", "expired", "expires-now", "expiry-seconds", "missing-expiry"} {
		t.Run(boundary, func(t *testing.T) {
			f := newAccountFixture(t)
			p := &topupFixture{}
			f.host.payments = p
			if accountLogin(t, f.browser, keyHTTPA) != http.StatusSeeOther {
				t.Fatal("SITE_TEST_FORM_TIME_LOGIN")
			}
			form := paymentForm(t, f.browser)
			ctx := sessionContext(t, f.browser)
			now := time.Now()
			issued, expires := now.Add(-time.Minute).UnixMilli(), now.Add(time.Minute).UnixMilli()
			switch boundary {
			case "future-issued":
				issued = now.Add(time.Hour).UnixMilli()
			case "expired":
				expires = now.Add(-time.Millisecond).UnixMilli()
			case "expires-now":
				expires = now.UnixMilli()
			case "expiry-seconds":
				expires = now.Add(time.Minute).Unix()
			case "missing-expiry":
				expires = 0
			}
			f.host.sessions.Put(ctx, "payment_form_issued", issued)
			f.host.sessions.Put(ctx, "payment_form_expires", expires)
			if _, _, err := f.host.sessions.Commit(ctx); err != nil {
				t.Fatal("SITE_TEST_FORM_TIME_COMMIT")
			}
			response, body := f.request(t, "POST", "/topup/start", form, f.server.URL)
			if response.StatusCode != http.StatusConflict || !strings.Contains(body, "SITE_FORM_REFUSED") || p.starts != 0 || p.reads != 0 {
				t.Fatal("SITE_TEST_FORM_TIME_ACTION_REFUSAL")
			}
		})
	}
}
