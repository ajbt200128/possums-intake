package website

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

const accountViewKey = "account-snapshot"

// snapshotBalanceView validates the complete snapshot's scope, but copies only
// exact authoritative totals. Hold serialization limits do not hide safe totals.
func snapshotBalanceView(account string, snapshot ledger.BalanceSnapshot) (map[string]string, error) {
	if account == "" || snapshot.AccountBalance.AccountID != account {
		return nil, accountScopeFault()
	}
	for _, row := range snapshot.PendingReservations {
		if row.AccountID != account || row.State != ledger.Active {
			return nil, accountScopeFault()
		}
	}
	balance, ok := moneyUSD(snapshot.AccountBalance.Balance)
	if !ok {
		return nil, accountMoneyFault()
	}
	available, ok := moneyUSD(snapshot.Available)
	if !ok {
		return nil, accountMoneyFault()
	}
	return map[string]string{"balance-usd": balance, "available-usd": available}, nil
}

// snapshotView formats one already-authoritative Service snapshot. It neither
// recomputes available credit nor performs accounting. Only this copied string
// view, not the snapshot/SDK principal/service, may enter a request VM.
func snapshotView(account string, snapshot ledger.BalanceSnapshot) (map[string]string, error) {
	view, err := snapshotBalanceView(account, snapshot)
	if err != nil {
		return nil, err
	}
	// The existing writer bounds growth, including a rejected complete list.
	// fmt writes through Write, never the embedded buffer's unbounded WriteString.
	var out documentBuffer
	_, err = fmt.Fprint(&out, "Pending holds:\n")
	if err != nil {
		return nil, accountViewLimit()
	}
	if len(snapshot.PendingReservations) == 0 {
		if _, err := fmt.Fprint(&out, "(none)\n"); err != nil {
			return nil, accountViewLimit()
		}
	}
	for _, row := range snapshot.PendingReservations {
		maximum, ok := moneyUSD(row.Maximum)
		if !ok {
			return nil, accountMoneyFault()
		}
		charged := "none"
		if row.ChargedAmount != nil {
			amount, ok := moneyUSD(*row.ChargedAmount)
			if !ok {
				return nil, accountMoneyFault()
			}
			charged = amount + " USD"
		}
		_, err := fmt.Fprintf(&out, "- Reserved: %s USD; expires: %s\n  Reservation: %s\n  Request key: %s\n  Timeout: %d seconds; state: %s; initial expiry: %s\n  Charged: %s; completed: %s; retained until: %s\n",
			maximum, time.UnixMilli(int64(row.ExpiresAt)).UTC().Format(time.RFC3339Nano), strconv.QuoteToASCII(row.ReservationID), strconv.QuoteToASCII(row.IdempotencyKey), row.TimeoutSeconds, string(row.State), time.UnixMilli(int64(row.InitialExpiresAt)).UTC().Format(time.RFC3339Nano), charged, nullableMillis(row.TerminalAt), nullableMillis(row.RetainUntil))
		if err != nil {
			return nil, accountViewLimit()
		}
	}
	view[accountViewKey] = out.String()
	total := 0
	for key, value := range view {
		if len(key)+len(value) > maxTextBytes-total {
			return nil, accountViewLimit()
		}
		total += len(key) + len(value)
	}
	return view, nil
}

// moneyUSD formats the authoritative microunit value exactly, without rounding
// or multiplication that could overflow. One USD is 1,000,000 microunits.
func moneyUSD(value ledger.Money) (string, bool) {
	units, err := strconv.ParseUint(string(value), 10, 64)
	if err != nil || strconv.FormatUint(units, 10) != string(value) {
		return "", false
	}
	fraction := strings.TrimRight(fmt.Sprintf("%06d", units%1_000_000), "0")
	if len(fraction) < 2 {
		fraction += strings.Repeat("0", 2-len(fraction))
	}
	return "$" + strconv.FormatUint(units/1_000_000, 10) + "." + fraction, true
}

func accountMoneyFault() *ledger.Diagnostic {
	d := fault("SITE_ACCOUNT_MONEY_UNAVAILABLE", "account_snapshot_validation", "canonical_uint64_microunits", "Snapshot money is invalid; underlying cause unknown. Ask the operator to inspect account data privately. No account data was displayed or operation replayed.", failuretype.NativeReadUnknown)
	d.Outcome = "account_snapshot_received_display_withheld"
	return d
}

func nullableMillis(value *ledger.Millis) string {
	if value == nil {
		return "none"
	}
	return time.UnixMilli(int64(*value)).UTC().Format(time.RFC3339Nano)
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
