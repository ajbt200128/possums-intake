// Package payments coordinates explicitly initialized ledger payments with an
// explicitly selected Stripe adapter. Ordinary runtime never constructs it.
package payments

import (
	"context"
	"time"

	"possums-intake/internal/ledger"
	"possums-intake/internal/stripepay"
)

const startupBudget = 10 * time.Second

// stripeAdapter is private so tests can exercise live coordination without
// changing the SDK's fixed origins. Public admission accepts only its owned client.
type stripeAdapter interface {
	IsLive() bool
	IsSandbox() bool
	ProductTaxCode() string
	CheckoutOrigin() string
	CheckoutURLAllowed(string) bool
	CreateCheckout(context.Context, stripepay.CreateRequest) (stripepay.Checkout, error)
	RetrieveEvidence(context.Context, string, stripepay.Purchase) (stripepay.Evidence, error)
	DecodeWebhook([]byte, string) (stripepay.Notification, error)
}

// Service holds no payment cache, provider bridge, queue, worker or retry policy.
// The website owns live session/form validation and memory-only references.
type Service struct {
	ledger   *ledger.Service
	stripe   stripeAdapter
	identity ledger.PaymentIdentity
	sandbox  bool
}

// NewOffline NEVER initializes schema. Admission opens only after bounded purge
// batches drain successfully. A failure returns no partially enabled service.
func NewOffline(ctx context.Context, core *ledger.Service, client *stripepay.Client) (out *Service, err error) {
	defer recoverError(&err, "payment_admission", "unknown")
	if ctx == nil || core == nil || client == nil || client.CheckoutOrigin() == "" || client.IsSandbox() || client.IsLive() {
		return nil, localFailure("PAYMENT_OFFLINE_CONFIGURATION_INVALID", "payment_admission", "explicit_initialized_ledger_and_fixture_required", "not_attempted")
	}
	return admit(ctx, core, client, ledger.PaymentIdentitySynthetic)
}

func admit(ctx context.Context, core *ledger.Service, client *stripepay.Client, expected ledger.PaymentIdentity) (*Service, error) {
	return admitObserved(ctx, core, client, expected, nil)
}

func (s *Service) configured() bool {
	if s == nil || s.ledger == nil || s.stripe == nil || s.stripe.CheckoutOrigin() == "" || s.stripe.IsSandbox() != s.sandbox {
		return false
	}
	switch s.identity {
	case ledger.PaymentIdentityLiveStripeTax:
		return s.stripe.IsLive() && !s.sandbox && s.stripe.ProductTaxCode() != "" && s.stripe.CheckoutOrigin() == "https://checkout.stripe.com"
	case ledger.PaymentIdentitySynthetic:
		return !s.stripe.IsLive() && s.stripe.ProductTaxCode() == "" && (!s.sandbox || s.stripe.CheckoutOrigin() == "https://checkout.stripe.com")
	default:
		return false
	}
}

func (s *Service) CheckoutURLAllowed(value string) bool {
	return s.configured() && s.stripe.CheckoutURLAllowed(value)
}
func (s *Service) CheckoutOrigin() string {
	if !s.configured() {
		return ""
	}
	return s.stripe.CheckoutOrigin()
}

// StartTopup requires the caller's exact live server-issued session form on
// EVERY call. The durable claim grants one provider call outside all ledger
// transactions. False claims never fetch/replay a URL or create, even on restart.
func (s *Service) StartTopup(ctx context.Context, account string, form ledger.PaymentForm, denomination string) (out TopupResult, err error) {
	defer func() {
		if recover() != nil {
			out.URL = ""
			err = localFailure("PAYMENT_COMPONENT_CAUSE_UNKNOWN", "payment_start", "component_cause_unknown", "unknown")
		}
	}()
	if !s.configured() || ctx == nil {
		return out, s.configurationFailure("payment_start")
	}
	input := ledger.PreparePaymentInput{AccountID: account, Denomination: denomination, Form: form}
	if s.identity == ledger.PaymentIdentityLiveStripeTax {
		input.TaxCode = s.stripe.ProductTaxCode()
	}
	p, err := s.ledger.PreparePayment(ctx, input)
	if err != nil {
		return out, err
	}
	out = localResult(p)
	p, claimed, err := s.ledger.ClaimPaymentCreate(ctx, account, p.Reference)
	if err != nil {
		return out, err
	}
	out = localResult(p)
	if !claimed {
		return out, nil
	}
	purchase, err := adapterPurchase(p)
	if err != nil {
		return out, err
	}
	checkout, err := s.stripe.CreateCheckout(ctx, stripepay.CreateRequest{Purchase: purchase, IdempotencyKey: p.CreateKey})
	if err != nil {
		// Best effort on the same bounded request context. Cancellation may prevent
		// this update; the durable attempted claim ALREADY forbids another dispatch.
		// Never detach a new worker or erase the primary creation-unknown failure.
		_ = s.ledger.MarkPaymentCreateUnknown(ctx, account, p.Reference)
		return out, err
	}
	bound, err := s.ledger.BindPaymentSession(ctx, sessionEvidence(checkout.Session))
	if err != nil {
		return out, creationBindingFailure(err)
	}
	out = localResult(bound)
	out.URL = checkout.HostedURL // transient only; never persisted or cached
	return out, nil
}

// TopupStatus only reads local account-owned retained state. It makes no Stripe
// request, creates no purchase, and cannot turn provider completion into credit.
func (s *Service) TopupStatus(ctx context.Context, account, ref string) (out TopupResult, err error) {
	defer recoverError(&err, "payment_status", "unknown")
	if !s.configured() || ctx == nil {
		return out, s.configurationFailure("payment_status")
	}
	p, err := s.ledger.GetPayment(ctx, account, ref)
	if unavailableReference(err) {
		return TopupResult{Status: "unavailable_after_unlink"}, nil
	}
	if err != nil {
		return out, err
	}
	// Do not return even the private reference again from the status surface.
	return TopupResult{Status: localResult(p).Status}, nil
}

func localResult(p ledger.PaymentPurchase) TopupResult {
	status := "unconfirmed"
	switch {
	case p.FundingState == "funded":
		status = "funded"
	case p.FundingState == "blocked":
		status = "support_required"
	case p.CreateState == "known":
		status = "awaiting_payment"
	}
	return TopupResult{Reference: p.Reference, Status: status}
}
