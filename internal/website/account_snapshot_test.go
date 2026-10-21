package website

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"possums-intake/internal/ledger"
)

func snapshotFixture(account string) ledger.BalanceSnapshot {
	return ledger.BalanceSnapshot{
		AccountBalance:      ledger.Account{AccountID: account, Balance: "18446744073709551615", Enabled: false},
		Available:           "17",
		PendingReservations: []ledger.Reservation{{ReservationID: strings.Repeat("a", 32), IdempotencyKey: "synthetic-key", AccountID: account, Maximum: "2", TimeoutSeconds: 60, InitialExpiresAt: 1999999940000, ExpiresAt: 2000000000000, State: ledger.Active}},
	}
}

func TestAccountSnapshotCompleteExactAndEscaped(t *testing.T) {
	account := "row\n\t\x00雪\u202e"
	snapshot := snapshotFixture(account)
	view, err := snapshotView(account, snapshot)
	if err != nil || len(view) != 1 {
		t.Fatal("complete snapshot view unavailable")
	}
	text := view[accountViewKey]
	for _, expected := range []string{`Account: "row\n\t\x00\u96ea\u202e"`, "Balance (microunits): 18446744073709551615", "Available (microunits): 17", "Enabled: false", `reservation_id="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`, `idempotency_key="synthetic-key"`, `account_id="row\n\t\x00\u96ea\u202e"`, "maximum=2", "timeout_seconds=60", "initial_expires_at=1999999940000", "expires_at=2000000000000", `state="active"`, "charged_amount=null", "terminal_at=null", "retain_until=null"} {
		if !strings.Contains(text, expected) {
			t.Fatal("snapshot omitted or changed a defined field")
		}
	}
	if strings.Contains(text, account) || strings.Contains(text, "雪") || strings.Contains(text, "\u202e") || strings.Contains(text, "\x00") {
		t.Fatal("identifier controls or Unicode not visibly escaped")
	}
	// Do not recompute financial arithmetic in the website: available is copied.
	if strings.Count(text, "Available (microunits): 17") != 1 {
		t.Fatal("snapshot available balance recomputed")
	}
	snapshot.PendingReservations = nil
	view, err = snapshotView(account, snapshot)
	if err != nil || !strings.Contains(view[accountViewKey], "(none)\n") {
		t.Fatal("empty authoritative holds not shown explicitly")
	}
}

func TestAccountSnapshotScopeAndWholeListBounds(t *testing.T) {
	const account = "SYNTHETIC_ACCOUNT_SENTINEL"
	for _, mutation := range []string{"account", "row", "terminal"} {
		snapshot := snapshotFixture(account)
		switch mutation {
		case "account":
			snapshot.AccountBalance.AccountID = "foreign-account"
		case "row":
			snapshot.PendingReservations[0].AccountID = "foreign-account"
		case "terminal":
			snapshot.PendingReservations[0].State = ledger.Settled
		}
		view, err := snapshotView(account, snapshot)
		var d *ledger.Diagnostic
		if view != nil || !errors.As(err, &d) || d.Code != "SITE_ACCOUNT_SCOPE_UNAVAILABLE" {
			t.Fatal("non-scoped/current snapshot admitted")
		}
		encoded, _ := json.Marshal(d)
		if strings.Contains(string(encoded), account) || strings.Contains(string(encoded), "foreign-account") {
			t.Fatal("scope error exposed identifiers")
		}
	}
	snapshot := snapshotFixture(account)
	row := snapshot.PendingReservations[0]
	for i := 1; i < 400; i++ {
		row.ReservationID = fmt.Sprintf("%032x", i)
		snapshot.PendingReservations = append(snapshot.PendingReservations, row)
	}
	view, err := snapshotView(account, snapshot)
	var d *ledger.Diagnostic
	if view != nil || !errors.As(err, &d) || d.Code != "SITE_ACCOUNT_VIEW_LIMIT" || d.Outcome != "account_snapshot_received_display_withheld" {
		t.Fatal("oversized complete snapshot silently truncated or wrong outcome")
	}
	encoded, _ := json.Marshal(d)
	if strings.Contains(string(encoded), account) {
		t.Fatal("display-limit error exposed account")
	}
}
