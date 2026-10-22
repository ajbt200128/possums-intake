package website

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"possums-intake/internal/ledger"
)

func TestMoneyUSDExactCanonical(t *testing.T) {
	for value, want := range map[ledger.Money]string{
		"0": "$0.00", "1": "$0.000001", "10": "$0.00001", "100": "$0.0001",
		"1000": "$0.001", "10000": "$0.01", "100000": "$0.10", "1000000": "$1.00",
		"5000000": "$5.00", "1234567": "$1.234567", "5010000": "$5.01",
		"18446744073709551615": "$18446744073709.551615",
	} {
		got, ok := moneyUSD(value)
		if !ok || got != want {
			t.Fatal("SITE_TEST_EXACT_USD")
		}
	}
	for _, value := range []ledger.Money{"", "00", "01", "+1", "-1", "1.0", " 1", "1 ", "18446744073709551616", "PRIVATE_INVALID<&>"} {
		if _, ok := moneyUSD(value); ok {
			t.Fatal("SITE_TEST_NONCANONICAL_USD")
		}
		for _, field := range []string{"balance", "available", "maximum", "charged"} {
			snapshot := snapshotFixture("synthetic")
			switch field {
			case "balance":
				snapshot.AccountBalance.Balance = value
			case "available":
				snapshot.Available = value
			case "maximum":
				snapshot.PendingReservations[0].Maximum = value
			case "charged":
				snapshot.PendingReservations[0].ChargedAmount = &value
			}
			view, err := snapshotView("synthetic", snapshot)
			var d *ledger.Diagnostic
			if view != nil || !errors.As(err, &d) || d.Code != "SITE_ACCOUNT_MONEY_UNAVAILABLE" || strings.Contains(d.Error(), "PRIVATE_INVALID") {
				t.Fatal("SITE_TEST_INVALID_SNAPSHOT_MONEY")
			}
		}
	}
}

func TestUSDAndPendingHoldsEscapeTogether(t *testing.T) {
	const account = "<script>synthetic-$-account</script>"
	snapshot := snapshotFixture(account)
	snapshot.AccountBalance.Balance = "5000000"
	snapshot.PendingReservations[0].ReservationID = "<script>synthetic-reservation</script>"
	snapshot.PendingReservations[0].IdempotencyKey = "<script>synthetic-request</script>"
	view, err := snapshotView(account, snapshot)
	if err != nil {
		t.Fatal("SITE_TEST_USD_ESCAPE_SNAPSHOT")
	}
	r := prepared(t)
	page, err := r.PageWithView(context.Background(), "signed-home", view)
	if err != nil {
		t.Fatal("SITE_TEST_USD_ESCAPE_PAGE")
	}
	body, err := r.Render(page, "csrf", "")
	if err != nil {
		t.Fatal("SITE_TEST_USD_HOLDS_RENDER")
	}
	text := string(body)
	for _, want := range []string{"&lt;script&gt;synthetic-reservation&lt;/script&gt;", "&lt;script&gt;synthetic-request&lt;/script&gt;", "Balance: $5.00 USD", "Available: $0.000017 USD", "Reserved: $0.000002 USD"} {
		if !strings.Contains(text, want) {
			t.Fatal("SITE_TEST_USD_HOLDS_ESCAPE")
		}
	}
	for _, forbidden := range []string{account, "synthetic-$-account", "<script>", "Enabled", "microunits", "5000000", "<details"} {
		if strings.Contains(text, forbidden) {
			t.Fatal("SITE_TEST_USD_HOLDS_INTERNAL_FIELD")
		}
	}
	if strings.Count(text, "Balance: ") != 1 || strings.Count(text, "Available: ") != 1 {
		t.Fatal("SITE_TEST_USD_HEADERS_REPEATED")
	}
}

func TestSnapshotViewBudgetIncludesUSD(t *testing.T) {
	snapshot := snapshotFixture("synthetic")
	view, err := snapshotView("synthetic", snapshot)
	if err != nil || len(view) != 3 {
		t.Fatal("SITE_TEST_VIEW_BUDGET_FIXTURE")
	}
	// Each ASCII idempotency byte adds one byte to the complete snapshot.
	total := 0
	for key, value := range view {
		total += len(key) + len(value)
	}
	snapshot.PendingReservations[0].IdempotencyKey += strings.Repeat("x", maxTextBytes-total)
	view, err = snapshotView("synthetic", snapshot)
	if err != nil || len(view) != 3 {
		t.Fatal("SITE_TEST_VIEW_EXACT_BUDGET")
	}
	total = 0
	for key, value := range view {
		total += len(key) + len(value)
	}
	if total != maxTextBytes {
		t.Fatal("SITE_TEST_VIEW_BUDGET_TOTAL")
	}
	if _, err := prepared(t).PageWithView(context.Background(), "signed-home", view); err == nil {
		// Presentation repeats labels and has its own stricter text budget; a
		// complete view need not fit a page. Never truncate it to make it fit.
		t.Fatal("SITE_TEST_PAGE_TEXT_BUDGET")
	}
	snapshot.PendingReservations[0].IdempotencyKey += "x"
	if view, err := snapshotView("synthetic", snapshot); view != nil || err == nil {
		t.Fatal("SITE_TEST_USD_VIEW_BUDGET_NOT_COUNTED")
	}
}

func slotPage(nodes ...Node) Node {
	return Node{Kind: "document", Text: "Synthetic", Children: []Node{{Kind: "main", Children: nodes}}}
}

func TestClosedHostRenderingAndDetails(t *testing.T) {
	r := prepared(t)
	page := slotPage(Node{Kind: "host", Target: "topup-form"}, Node{Kind: "host", Target: "topup-link"}, Node{Kind: "host", Target: "topup-status-link"}, Node{Kind: "host", Target: "account-link"}, Node{Kind: "details", Text: "Account <details>", Children: []Node{{Kind: "text", Text: "$5.00 <script>private</script>"}}})
	for _, bindings := range []hostBindings{{}, {paymentsEnabled: true}} {
		body, err := r.render(page, "synthetic-csrf", "", bindings)
		if err != nil || strings.Contains(string(body), `action="/topup/start"`) || strings.Contains(string(body), `name="attempt"`) {
			t.Fatal("SITE_TEST_FORM_CAPABILITY_SUPPRESSION")
		}
		if strings.Contains(string(body), `href="/topup"`) != bindings.paymentsEnabled || strings.Contains(string(body), `href="/topup/status"`) != bindings.paymentsEnabled {
			t.Fatal("SITE_TEST_LINK_CAPABILITY_SUPPRESSION")
		}
		if !strings.Contains(string(body), `<details><summary>Account &lt;details&gt;</summary>$5.00 &lt;script&gt;private&lt;/script&gt;</details>`) || !strings.Contains(string(body), `href="/home"`) {
			t.Fatal("SITE_TEST_ESCAPED_SEMANTIC_DETAILS")
		}
	}
	body, err := r.Render(page, "synthetic-csrf", "")
	if err != nil || strings.Contains(string(body), `href="/topup"`) || strings.Contains(string(body), `action="/topup/start"`) {
		t.Fatal("SITE_TEST_LEGACY_RENDER_ZERO_BINDINGS")
	}
	valid := hostBindings{paymentsEnabled: true, formEnabled: true, attempt: strings.Repeat("a", 64)}
	body, err = r.render(page, `synthetic<&"csrf`, "", valid)
	if err != nil {
		t.Fatal("SITE_TEST_FORM_BINDING")
	}
	for _, want := range []string{`action="/topup/start"`, `name="revision" value="` + r.Digest + `"`, `name="attempt" value="` + valid.attempt + `"`, `$5.00`, `$10.00`, `$25.00`, `$50.00`, `synthetic&lt;&amp;&#34;csrf`} {
		if !strings.Contains(string(body), want) {
			t.Fatal("SITE_TEST_HOST_FORM_ESCAPED_FIELDS")
		}
	}
	for _, bad := range []hostBindings{
		{formEnabled: true, attempt: valid.attempt},
		{paymentsEnabled: true, formEnabled: true},
		{paymentsEnabled: true, formEnabled: true, attempt: strings.Repeat("A", 64)},
		{paymentsEnabled: true, formEnabled: true, attempt: strings.Repeat("a", 65)},
		{paymentsEnabled: true, formEnabled: true, attempt: `<script>private</script>`},
		{attempt: valid.attempt},
	} {
		if b, err := r.render(page, "csrf", "", bad); err == nil || b != nil {
			t.Fatal("SITE_TEST_HOST_FORM_INPUT_REFUSAL")
		}
	}
	if b, err := r.render(page, "", "", valid); err == nil || b != nil {
		t.Fatal("SITE_TEST_FORM_REQUIRES_CSRF")
	}
	for _, bad := range []Node{
		{Kind: "host", Target: "unknown"}, {Kind: "host", Target: "topup-form", Text: "private"},
		{Kind: "host", Target: "topup-form", Children: []Node{{Kind: "text"}}},
		{Kind: "details"}, {Kind: "details", Children: []Node{{Kind: "main"}}},
		{Kind: "details", Children: []Node{{Kind: "document"}}},
		{Kind: "details", Children: []Node{{Kind: "text"}, {Kind: "text"}}},
	} {
		if b, err := r.render(slotPage(bad), "csrf", "", valid); err == nil || b != nil {
			t.Fatal("SITE_TEST_MALFORMED_HOST_DETAILS")
		}
	}
}

func TestHostSlotsCannotGrantFormsOutsideTopupGET(t *testing.T) {
	f := newAccountFixture(t)
	bundle := Fixture()
	bundle["pages.el"] = strings.ReplaceAll(bundle["pages.el"], `(ui:notice "\n")`, `(ui:host 'topup-form) (ui:notice "\n")`)
	bundle["pages.el"] += `(defun topup-page (v p) (page "Synthetic" (ui:host 'topup-form)))`
	revision, err := Prepare(context.Background(), bundle)
	if err != nil {
		t.Fatal("SITE_TEST_HOST_SLOT_BUNDLE")
	}
	f.host.revision = revision
	p := &topupFixture{result: TopupResult{Status: "funded"}}
	f.host.payments = p
	if accountLogin(t, f.browser, keyHTTPA) != 303 {
		t.Fatal("SITE_TEST_HOST_SLOT_LOGIN")
	}
	for _, path := range []string{"/", "/login", "/home", "/topup/status", "/topup/return"} {
		response, body := f.request(t, "GET", path, nil, "")
		if response.StatusCode != 200 || strings.Contains(body, `action="/topup/start"`) || strings.Contains(body, `name="attempt"`) {
			t.Fatal("SITE_TEST_HOST_SLOT_GET_FORM_ESCALATION")
		}
	}
	form := paymentForm(t, f.browser)
	response, body := f.request(t, "POST", "/topup/start", form, f.server.URL)
	if response.StatusCode != 200 || strings.Contains(body, `action="/topup/start"`) || p.starts != 1 || p.reads != 0 {
		t.Fatal("SITE_TEST_HOST_SLOT_POST_FORM_ESCALATION")
	}
	form.Set("csrf_token", "bad")
	response, body = f.request(t, "POST", "/topup/start", form, f.server.URL)
	if response.StatusCode != 403 || strings.Contains(body, `action="/topup/start"`) || p.starts != 1 {
		t.Fatal("SITE_TEST_HOST_SLOT_ERROR_FORM_ESCALATION")
	}
}

func TestPaymentStatesAreLispPresentationOnly(t *testing.T) {
	r := prepared(t)
	for state, phrase := range map[string]string{
		"form": "Choose one fixed credit amount", "funded": "Credit was added once",
		"awaiting_payment":         "payment and account funding are not confirmed",
		"unknown":                  "Payment creation or funding is unconfirmed",
		"support_required":         "not confirmation of a refund",
		"unavailable_after_unlink": "does not establish whether a charge or credit occurred",
		"no-reference":             "No purchase reference is available",
		"PRIVATE_HOSTILE_STATE":    "Payment creation or funding is unconfirmed",
	} {
		page, err := r.PageWithView(context.Background(), "topup-page", map[string]string{"payment-state": state})
		if err != nil {
			t.Fatal("SITE_TEST_PAYMENT_STATE_PAGE")
		}
		body, err := r.render(page, "csrf", "", hostBindings{paymentsEnabled: true})
		if err != nil || !strings.Contains(string(body), phrase) || strings.Contains(string(body), "PRIVATE_HOSTILE_STATE") || strings.Contains(string(body), `action="/topup/start"`) || !strings.Contains(string(body), r.CSSPath()) || !strings.Contains(string(body), "Sandbox/test only") {
			t.Fatal("SITE_TEST_PAYMENT_CLOSED_STATE_LAYOUT")
		}
	}
	if paymentState("PRIVATE_PROVIDER_STATE") != "unknown" || paymentState("form") != "unknown" || paymentState("funded") != "funded" {
		t.Fatal("SITE_TEST_PAYMENT_STATE_ALLOWLIST")
	}
}

func TestTopupNavigationVisibilityByState(t *testing.T) {
	r := prepared(t)
	for _, state := range []string{"form", "awaiting_payment", "unknown", "funded", "no-reference", "support_required", "unavailable_after_unlink", "PRIVATE_HOSTILE_STATE"} {
		t.Run(state, func(t *testing.T) {
			page, err := r.PageWithView(context.Background(), "topup-page", map[string]string{"payment-state": state})
			if err != nil {
				t.Fatal("SITE_TEST_TOPUP_NAV_PAGE")
			}
			bindings := hostBindings{paymentsEnabled: true}
			if state == "form" {
				bindings.formEnabled = true
				bindings.attempt = strings.Repeat("a", 64)
			}
			body, err := r.render(page, "csrf", "", bindings)
			if err != nil {
				t.Fatal("SITE_TEST_TOPUP_NAV_RENDER")
			}
			text := string(body)
			wantStatus := state == "awaiting_payment" || state == "unknown"
			if strings.Contains(text, `href="/topup/status"`) != wantStatus || strings.Contains(text, "Check payment status") != wantStatus || strings.Contains(text, `href="/topup"`) || strings.Count(text, `<a href="/home">Account</a>`) != 1 {
				t.Fatal("SITE_TEST_TOPUP_NAV_VISIBILITY")
			}
			if strings.Contains(text, `action="/topup/start"`) != (state == "form") {
				t.Fatal("SITE_TEST_TOPUP_FORM_VISIBILITY")
			}
			if state == "form" && strings.Count(text, `name="denomination"`) != 4 {
				t.Fatal("SITE_TEST_TOPUP_FOUR_DENOMINATIONS")
			}
		})
	}
}

func TestAccountEntryUSDAndPlainHoldsHTTP(t *testing.T) {
	f := newAccountFixture(t)
	if accountLogin(t, f.browser, keyHTTPA) != 303 {
		t.Fatal("SITE_TEST_UI_LOGIN")
	}
	_, body := f.request(t, "GET", "/home", nil, "")
	if strings.Contains(body, `href="/topup"`) || !strings.Contains(body, "Balance: $0.00 USD") || !strings.Contains(body, "Available: $0.00 USD") || !strings.Contains(body, "Pending holds:\n(none)\n") || strings.Contains(body, "<details") || strings.Contains(body, "Account details") || strings.Contains(body, "Enabled") || strings.Contains(body, "microunits") || strings.Contains(body, "SYNTHETIC_A") {
		t.Fatal("SITE_TEST_ACCOUNT_USD_DISABLED_ENTRY")
	}
	p := &topupFixture{}
	f.host.payments = p
	_, body = f.request(t, "GET", "/home", nil, "")
	if !strings.Contains(body, `<a href="/topup">Add credit</a>`) || strings.Contains(body, `action="/topup/start"`) || p.starts != 0 || p.reads != 0 {
		t.Fatal("SITE_TEST_ACCOUNT_ENTRY_NO_PAYMENT_ACTION")
	}
	_, body = f.request(t, "GET", "/topup", nil, "")
	if !strings.Contains(body, f.host.revision.CSSPath()) || !strings.Contains(body, `class="skip"`) || strings.Contains(body, "No real payment is enabled") {
		t.Fatal("SITE_TEST_TOPUP_COMMON_DOCUMENT")
	}
}

func TestFundedOutcomeSurvivesDocumentAndSessionFailure(t *testing.T) {
	for _, sessionFailure := range []string{"none", "error", "panic"} {
		t.Run(sessionFailure, func(t *testing.T) {
			f := newAccountFixture(t)
			p := &topupFixture{result: TopupResult{Reference: strings.Repeat("a", 64), Status: "funded"}}
			f.host.payments = p
			if accountLogin(t, f.browser, keyHTTPA) != 303 {
				t.Fatal("SITE_TEST_DOCUMENT_FAILURE_LOGIN")
			}
			form := paymentForm(t, f.browser)
			bundle := Fixture()
			// Copied text fits the tree bound, but HTML escaping exceeds the
			// document cap. Exercise the actual renderer after known funding.
			bundle["pages.el"] += `(defun topup-page (v p) (page "Receipt" (ui:text "` + strings.Repeat("<", 17000) + `")))`
			revision, err := Prepare(context.Background(), bundle)
			if err != nil {
				t.Fatal("SITE_TEST_DOCUMENT_FAILURE_BUNDLE")
			}
			form.Set("revision", revision.Digest)
			f.host.revision = revision
			if sessionFailure != "none" {
				f.host.sessions.Store = faultyStore{Store: f.host.store, operation: "commit", panicValue: sessionFailure == "panic"}
			}
			response, body := f.request(t, "POST", "/topup/start", form, f.server.URL)
			code := "SITE_DOCUMENT_UNAVAILABLE"
			if sessionFailure != "none" {
				code = "SITE_SESSION_UNAVAILABLE"
			}
			assertClosedHTTP(t, response, body, code, "original_funding_confirmed_not_availability")
			if p.starts != 1 || strings.Contains(body, p.result.Reference) || strings.Contains(body, "payment_creation_unconfirmed") {
				t.Fatal("SITE_TEST_DOCUMENT_FAILURE_LOST_FUNDING")
			}
		})
	}
}

func TestTopupOldBundleFailsClosedAndFundingSurvivesRenderFailure(t *testing.T) {
	for _, target := range []string{"form", "funded"} {
		t.Run(target, func(t *testing.T) {
			f := newAccountFixture(t)
			p := &topupFixture{result: TopupResult{Reference: strings.Repeat("a", 64), Status: "funded"}}
			f.host.payments = p
			if accountLogin(t, f.browser, keyHTTPA) != 303 {
				t.Fatal("SITE_TEST_UI_LOGIN")
			}
			var form url.Values
			if target == "funded" {
				form = paymentForm(t, f.browser)
			}
			// A previously published bundle remains valid but lacks the new
			// named function. No unrevisioned built-in page may replace it.
			bundle := Fixture()
			bundle["pages.el"] = strings.Replace(bundle["pages.el"], "(defun topup-page ", "(defun old-topup-page ", 1)
			revision, err := Prepare(context.Background(), bundle)
			if err != nil {
				t.Fatal("SITE_TEST_OLD_BUNDLE_PREPARE")
			}
			// Keep the form revision current to isolate presentation failure.
			if form != nil {
				form.Set("revision", revision.Digest)
			}
			f.host.revision = revision
			method, path := "GET", "/topup"
			if target == "funded" {
				method, path = "POST", "/topup/start"
			}
			response, body := f.request(t, method, path, form, f.server.URL)
			if response.StatusCode != 503 || !strings.Contains(body, "SITE_RENDER_UNAVAILABLE") || strings.Contains(body, `action="/topup/start"`) || strings.Contains(body, p.result.Reference) || strings.Contains(body, "Credit was added once") {
				t.Fatal("SITE_TEST_MISSING_TOPUP_FAIL_CLOSED")
			}
			if target == "funded" && (p.starts != 1 || !strings.Contains(body, "original_funding_confirmed_not_availability")) {
				t.Fatal("SITE_TEST_FUNDED_RENDER_FAILURE_EVIDENCE")
			}
			if target == "form" && p.starts != 0 {
				t.Fatal("SITE_TEST_RENDER_INITIATED_PAYMENT")
			}
		})
	}
}
