package payments

import (
	"context"

	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
	"possums-intake/internal/stripepay"
)

// NewLiveObserved has NewLive's admission requirements and performs no provider
// work or initialization. It owns ALL admission failure observations, including
// preflight/decision errors and distinct faults after known transaction completion.
// Callers must not recount its propagated error or wrap this multi-call admission
// in a ledger FailureReport. This is normal admission, not a diagnostic or retry.
func NewLiveObserved(ctx context.Context, core *ledger.Service, client *stripepay.Client, observer failureowner.Observer) (out *Service, err error) {
	defer recoverAdmission(&out, &err, observer)
	if ctx == nil || core == nil || client == nil || !client.IsLive() || client.IsSandbox() || client.ProductTaxCode() == "" || client.CheckoutOrigin() != "https://checkout.stripe.com" {
		err = localFailure("PAYMENT_LIVE_CONFIGURATION_INVALID", "payment_admission", "explicit_initialized_live_ledger_and_tax_adapter_required", "not_attempted")
		failureowner.Observe(observer, ledger.FailureKind(err, failuretype.RequestInvalid))
		return nil, err
	}
	return admitObserved(ctx, core, client, ledger.PaymentIdentityLiveStripeTax, observer)
}

func admitObserved(ctx context.Context, core *ledger.Service, client *stripepay.Client, expected ledger.PaymentIdentity, observer failureowner.Observer) (out *Service, err error) {
	defer recoverAdmission(&out, &err, observer)
	ctx, cancel := context.WithTimeout(ctx, startupBudget)
	defer cancel()
	if cause := ctx.Err(); cause != nil {
		return nil, admissionContextFailure(cause, observer)
	}
	// Each report shadows any caller report and belongs to exactly ONE run.
	call, report := ledger.WithFailureReport(ctx)
	identity, err := core.PaymentIdentity(call)
	ledger.ObserveRunFailures(observer, report, err, failuretype.RuntimeSetupUnknown)
	if err != nil {
		return nil, err
	}
	if identity != expected {
		err = localFailure("PAYMENT_MODE_MISMATCH", "payment_admission", "persisted_payment_identity_must_match_adapter", "not_attempted")
		failureowner.Observe(observer, ledger.FailureKind(err, failuretype.PolicyRefused))
		return nil, err
	}
	for {
		if cause := ctx.Err(); cause != nil {
			return nil, admissionContextFailure(cause, observer)
		}
		call, report := ledger.WithFailureReport(ctx)
		var result ledger.PaymentPurgeResult
		if identity == ledger.PaymentIdentityLiveStripeTax {
			result, err = core.PurgeLiveStripePayments(call)
		} else {
			result, err = core.PurgeSyntheticPayments(call)
		}
		// Consume only after the method returns and releases its writer gate.
		// A nil error can still carry distinct after-completion faults.
		ledger.ObserveRunFailures(observer, report, err, failuretype.RuntimeSetupUnknown)
		if err != nil {
			return nil, err
		}
		if !result.More && ctx.Err() == nil {
			return &Service{ledger: core, stripe: client, identity: identity, sandbox: client.IsSandbox()}, nil
		}
	}
}

func admissionContextFailure(cause error, observer failureowner.Observer) error {
	failureowner.Observe(observer, ledger.FailureKind(cause, failuretype.RuntimeSetupUnknown))
	return localFailure("PAYMENT_PURGE_UNAVAILABLE", "payment_admission", "complete_bounded_startup_purge_required", "unavailable")
}

func recoverAdmission(out **Service, err *error, observer failureowner.Observer) {
	if recover() != nil {
		*out = nil
		*err = localFailure("PAYMENT_COMPONENT_CAUSE_UNKNOWN", "payment_admission", "component_cause_unknown", "unknown")
		failureowner.Observe(observer, ledger.FailureKind(*err, failuretype.RuntimeSetupUnknown))
	}
}
