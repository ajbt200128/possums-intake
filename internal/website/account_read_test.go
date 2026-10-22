package website

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
	"possums-intake/internal/siteauth"
	"possums-intake/pkg/lispsite"
)

type balanceReadFunc func(context.Context, string) (ledger.BalanceSnapshot, error)

func (f balanceReadFunc) Balance(ctx context.Context, id string) (ledger.BalanceSnapshot, error) {
	return f(ctx, id)
}

func TestAccountReadUnknownAndScopeOwnOnceWithoutReplay(t *testing.T) {
	for _, mode := range []string{"error", "panic", "cancelled", "scope"} {
		observer := &numericObserver{}
		calls := 0
		host := &Host{observer: observer, balances: balanceReadFunc(func(context.Context, string) (ledger.BalanceSnapshot, error) {
			calls++
			switch mode {
			case "panic":
				panic("SYNTHETIC_READ_SECRET")
			case "cancelled":
				return ledger.BalanceSnapshot{}, context.Canceled
			case "scope":
				return snapshotFixture("SYNTHETIC_FOREIGN_SECRET"), nil
			}
			return ledger.BalanceSnapshot{}, errors.New("SYNTHETIC_READ_SECRET")
		})}
		snapshot, d := host.accountSnapshot(context.Background(), "synthetic-own")
		expected := failuretype.NativeReadUnknown
		if mode == "cancelled" {
			expected = failuretype.Cancelled
		}
		if calls != 1 || d == nil || d.Kind() != expected || len(observer.counts()) != 1 || observer.counts()[0] != expected || snapshot.AccountBalance.AccountID != "" {
			t.Fatal("SITE_TEST_ACCOUNT_READ_OWNERSHIP")
		}
		if strings.Contains(diagnosticText(d), "SECRET") || d.Outcome == "no_effect" {
			t.Fatal("SITE_TEST_ACCOUNT_READ_PRIVACY_OUTCOME")
		}
		// Disabling observation does not disable classification or initiate work.
		host.observer = nil
		_, d = host.accountSnapshot(context.Background(), "synthetic-own")
		if d == nil || d.Kind() != expected || calls != 2 {
			t.Fatal("SITE_TEST_ACCOUNT_READ_DISABLED_CLASSIFICATION")
		}
	}
}

func TestAccountReadPreservesReportedPrimaryBeforeOuterPanic(t *testing.T) {
	f := newAccountFixture(t)
	observer := &numericObserver{}
	host := &Host{observer: observer, balances: balanceReadFunc(func(ctx context.Context, _ string) (ledger.BalanceSnapshot, error) {
		f.service.Balance(ctx, "missing00000000")
		panic("SYNTHETIC_OUTER_SECRET")
	})}
	_, d := host.accountSnapshot(context.Background(), accountHTTPA)
	kinds := observer.counts()
	if d == nil || len(kinds) != 2 || kinds[0] != failuretype.AccountMissing || kinds[1] != failuretype.NativeReadUnknown || strings.Contains(diagnosticText(d), "SECRET") {
		t.Fatal("SITE_TEST_ACCOUNT_READ_SECONDARY_PANIC")
	}
}

func accountPageRequest(host *Host, revision *Revision, writer http.ResponseWriter) (*responseState, *http.Request) {
	request := httptest.NewRequest("GET", "https://fixture.invalid/home", nil)
	request = request.WithContext(context.WithValue(request.Context(), revisionKey{}, revision))
	request = request.WithContext(context.WithValue(request.Context(), principalKey{}, siteauth.Principal{AccountID: "synthetic-own"}))
	return host.ownResponse(writer, request)
}

func TestAccountSnapshotLateRenderAndDeliveryDoNotReadAgain(t *testing.T) {
	for _, mode := range []string{"render", "delivery"} {
		revision := prepared(t)
		if mode == "render" {
			bundle := Fixture()
			start := strings.Index(bundle["pages.el"], "(defun signed-home ")
			if start < 0 {
				t.Fatal("SITE_TEST_ACCOUNT_PAGE_DEFINITION")
			}
			bundle["pages.el"] = bundle["pages.el"][:start] + `(defun signed-home (view preferences) (if (get view "account-snapshot") (page "Home" (ui:p 7)) (page "Home" (ui:p "Ready"))))`
			var err error
			revision, err = Prepare(context.Background(), bundle)
			if err != nil {
				t.Fatal("SITE_TEST_ACCOUNT_LATE_RENDER_PREPARE")
			}
		}
		calls := 0
		observer := &numericObserver{}
		host := &Host{revision: revision, observer: observer, balances: balanceReadFunc(func(context.Context, string) (ledger.BalanceSnapshot, error) {
			calls++
			return snapshotFixture("synthetic-own"), nil
		})}
		recorder := httptest.NewRecorder()
		var writer http.ResponseWriter = recorder
		if mode == "delivery" {
			writer = &failingResponse{header: make(http.Header)}
		}
		state, request := accountPageRequest(host, revision, writer)
		route, _ := revision.Named("signed-home")
		host.page(state, request, route, 200, "")
		kinds := observer.counts()
		if calls != 1 || len(kinds) != 1 || state.outcome != "account_snapshot_confirmed_display_unconfirmed" {
			t.Fatal("SITE_TEST_ACCOUNT_LATE_FAILURE_REPLAY_OR_OUTCOME")
		}
		if mode == "render" && (recorder.Code != 503 || strings.Contains(recorder.Body.String(), "synthetic-own") || !strings.Contains(recorder.Body.String(), "SITE_RENDER_UNAVAILABLE")) {
			t.Fatal("SITE_TEST_ACCOUNT_LATE_RENDER_PRIVACY")
		}
		if mode == "delivery" && kinds[0] != failuretype.ResponseDeliveryUnknown {
			t.Fatal("SITE_TEST_ACCOUNT_DELIVERY_CATEGORY")
		}
	}
}

func TestAccountViewClosedProjection(t *testing.T) {
	good := &lispsite.Diagnostic{Code: "PAGE_VIEW_REFUSED", Stage: "page_evaluation", Constraint: "bounded_string_view", Category: lispsite.Refused}
	d, ok := coreDiagnostic(good)
	if !ok || d.Code != "SITE_PAGE_VIEW_REFUSED" || renderHTTPFault(d).Constraint != "bounded_string_view" {
		t.Fatal("SITE_TEST_ACCOUNT_VIEW_PROJECTION")
	}
	good.Stage = "SYNTHETIC_PRIVATE_STAGE"
	if _, ok := coreDiagnostic(good); ok {
		t.Fatal("SITE_TEST_ACCOUNT_VIEW_HOSTILE_TUPLE")
	}
	_, err := prepared(t).PageWithView(context.Background(), "signed-home", map[string]string{"Bad": "SYNTHETIC_PRIVATE_VIEW"})
	d, ok = err.(*ledger.Diagnostic)
	if !ok || d.Code != "SITE_PAGE_VIEW_REFUSED" || strings.Contains(diagnosticText(d), "PRIVATE") {
		t.Fatal("SITE_TEST_ACCOUNT_VIEW_WRAPPER")
	}
}
