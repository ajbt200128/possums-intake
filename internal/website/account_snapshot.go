package website

import (
	"fmt"
	"strconv"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

const accountViewKey = "account-snapshot"

// snapshotView formats one already-authoritative Service snapshot. It neither
// recomputes available credit nor performs accounting. Only this copied string
// view, not the snapshot/SDK principal/service, may enter a request VM.
func snapshotView(account string, snapshot ledger.BalanceSnapshot) (map[string]string, error) {
	if account == "" || snapshot.AccountBalance.AccountID != account {
		return nil, accountScopeFault()
	}
	for _, row := range snapshot.PendingReservations {
		if row.AccountID != account || row.State != ledger.Active {
			return nil, accountScopeFault()
		}
	}
	// The existing writer bounds growth, including a rejected complete list.
	// fmt writes through Write, never the embedded buffer's unbounded WriteString.
	var out documentBuffer
	_, err := fmt.Fprintf(&out, "Account: %s\nBalance (microunits): %s\nAvailable (microunits): %s\nEnabled: %t\nPending holds (amounts in microunits; timestamps in Unix milliseconds):\n",
		strconv.QuoteToASCII(account), snapshot.AccountBalance.Balance, snapshot.Available, snapshot.AccountBalance.Enabled)
	if err != nil {
		return nil, accountViewLimit()
	}
	if len(snapshot.PendingReservations) == 0 {
		if _, err := fmt.Fprint(&out, "(none)\n"); err != nil {
			return nil, accountViewLimit()
		}
	}
	for _, row := range snapshot.PendingReservations {
		_, err := fmt.Fprintf(&out, "- reservation_id=%s idempotency_key=%s account_id=%s maximum=%s timeout_seconds=%d initial_expires_at=%d expires_at=%d state=%s charged_amount=%s terminal_at=%s retain_until=%s\n",
			strconv.QuoteToASCII(row.ReservationID), strconv.QuoteToASCII(row.IdempotencyKey), strconv.QuoteToASCII(row.AccountID), row.Maximum, row.TimeoutSeconds, int64(row.InitialExpiresAt), int64(row.ExpiresAt), strconv.QuoteToASCII(string(row.State)), nullableMoney(row.ChargedAmount), nullableMillis(row.TerminalAt), nullableMillis(row.RetainUntil))
		if err != nil {
			return nil, accountViewLimit()
		}
	}
	if out.Len()+len(accountViewKey) > maxTextBytes {
		return nil, accountViewLimit()
	}
	return map[string]string{accountViewKey: out.String()}, nil
}

func nullableMoney(value *ledger.Money) string {
	if value == nil {
		return "null"
	}
	return string(*value)
}
func nullableMillis(value *ledger.Millis) string {
	if value == nil {
		return "null"
	}
	return strconv.FormatInt(int64(*value), 10)
}

func accountScopeFault() *ledger.Diagnostic {
	d := fault("SITE_ACCOUNT_SCOPE_UNAVAILABLE", "account_snapshot_validation", "matching_account_and_pending_rows", "Snapshot scope is invalid; underlying cause unknown. Ask the operator to inspect the binding privately. No account data was displayed or operation replayed.", failuretype.NativeReadUnknown)
	d.Outcome = "snapshot_withheld"
	return d
}
func accountViewLimit() *ledger.Diagnostic {
	d := fault("SITE_ACCOUNT_VIEW_LIMIT", "account_snapshot_view", "complete_bounded_snapshot", "The complete snapshot exceeds website display limits. No rows were displayed; ask the operator to inspect account data privately. No financial action was replayed.", failuretype.PolicyRefused)
	d.Outcome = "account_snapshot_received_display_withheld"
	return d
}
