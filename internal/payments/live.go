package payments

import (
	"context"

	"possums-intake/internal/ledger"
	"possums-intake/internal/stripepay"
)

// IsLive reports admitted configuration only, not payment or funding evidence.
// It performs no database or provider work and exposes no private configuration.
func (s *Service) IsLive() bool {
	return s.configured() && s.identity == ledger.PaymentIdentityLiveStripeTax
}

// NewLive requires explicit live adapter configuration and already initialized
// live Stripe Tax state. It performs no provider work or schema initialization.
// All overdue purchase/sale pairs must drain before admission is returned.
func NewLive(ctx context.Context, core *ledger.Service, client *stripepay.Client) (out *Service, err error) {
	defer recoverError(&err, "payment_admission", "unknown")
	if ctx == nil || core == nil || client == nil || !client.IsLive() || client.IsSandbox() || client.ProductTaxCode() == "" || client.CheckoutOrigin() != "https://checkout.stripe.com" {
		return nil, localFailure("PAYMENT_LIVE_CONFIGURATION_INVALID", "payment_admission", "explicit_initialized_live_ledger_and_tax_adapter_required", "not_attempted")
	}
	return admit(ctx, core, client, ledger.PaymentIdentityLiveStripeTax)
}
