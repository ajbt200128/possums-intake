package payments

import (
	"context"

	"possums-intake/internal/ledger"
	"possums-intake/internal/stripepay"
)

// NewSandbox requires a sandbox-mode adapter and already initialized synthetic
// payment schema. Like NewOffline, it admits only after overdue purge completes;
// admission itself makes no Stripe requests and never initializes or repairs.
func NewSandbox(ctx context.Context, core *ledger.Service, client *stripepay.Client) (out *Service, err error) {
	defer recoverError(&err, "payment_admission", "unknown")
	if ctx == nil || core == nil || client == nil || !client.IsSandbox() || client.IsLive() {
		return nil, localFailure("PAYMENT_SANDBOX_CONFIGURATION_INVALID", "payment_admission", "explicit_initialized_ledger_and_sandbox_required", "not_attempted")
	}
	return admit(ctx, core, client, ledger.PaymentIdentitySynthetic)
}
