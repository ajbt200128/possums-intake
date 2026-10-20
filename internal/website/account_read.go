package website

import (
	"context"
	"net/http"

	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

// BalanceReader exposes only the complete authoritative account snapshot. The
// host supplies the identity from SDK validation, never a browser selector.
type BalanceReader interface {
	Balance(context.Context, string) (ledger.BalanceSnapshot, error)
}

type principalKey struct{}

// Own each accounting report once, including independent completion failures.
// Formatting and delivery failures are separate owners and never replay a read.
func (h *Host) accountSnapshot(ctx context.Context, account string) (snapshot ledger.BalanceSnapshot, result *ledger.Diagnostic) {
	ctx, report := ledger.WithFailureReport(ctx)
	var cause error
	panicked := false
	defer func() {
		if recover() != nil {
			panicked = true
			cause = accountReadDiagnostic(nil)
		}
		primary := ledger.ObserveRunFailures(h.observer, report, cause, failuretype.NativeReadUnknown)
		if panicked && primary {
			// A callback already reported a failure before a distinct outer panic.
			failureowner.Observe(h.observer, failuretype.NativeReadUnknown)
		}
		if cause != nil {
			snapshot = ledger.BalanceSnapshot{}
			result = accountReadDiagnostic(cause)
		} else if account == "" || snapshot.AccountBalance.AccountID != account {
			// A successful read must establish this binding before session grant.
			snapshot = ledger.BalanceSnapshot{}
			result = accountScopeFault()
			failureowner.Observe(h.observer, result.Kind())
		}
	}()
	snapshot, cause = h.balances.Balance(ctx, account)
	return
}

func accountReadDiagnostic(cause error) *ledger.Diagnostic {
	d := ledger.CanonicalFailure(cause, failuretype.NativeReadUnknown)
	if d != nil && d.Code != ledger.CodeBoundary {
		return d // retain known accounting category, stage and authoritative outcome
	}
	if d == nil {
		d = fault("SITE_ACCOUNT_READ_UNAVAILABLE", "account_snapshot_read", "service_completion_cause_unknown", "", failuretype.NativeReadUnknown)
	}
	d.Code, d.Stage, d.Constraint = "SITE_ACCOUNT_READ_UNAVAILABLE", "account_snapshot_read", "service_completion_cause_unknown"
	d.Outcome = "unknown"
	d.NextStep = "Underlying cause unknown. The snapshot is unconfirmed; holds may have expired during the read. Do not assume financial rollback or replay an operation. Ask the operator to inspect availability privately."
	return d
}

func (h *Host) accountReadFailure(w http.ResponseWriter, r *http.Request, d *ledger.Diagnostic) {
	setOutcome(r, d.Outcome)
	// accountSnapshot already owns this failure; response delivery is distinct.
	sessionBoundary(r, "commit")
	h.plain(w, http.StatusServiceUnavailable, d)
}
