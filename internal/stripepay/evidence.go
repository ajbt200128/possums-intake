package stripepay

import (
	"context"
	"time"

	"github.com/stripe/stripe-go/v87"
)

// RetrieveEvidence issues at most three GETs: original Session (+line items),
// that Session's PaymentIntent, then that intent's latest Charge (+refunds).
// Expected purchase terms come from the retained original server purchase,
// never the webhook, URL, or customer. It never searches, creates, confirms,
// captures, retries, or funds. A lookup failure cannot resolve create uncertainty.
func (c *Client) RetrieveEvidence(ctx context.Context, sessionID string, expected Purchase) (out Evidence, err error) {
	defer recoverBoundary(&err, StageSession, OutcomeReadUnavailable)
	if !c.configured() || ctx == nil || !sessionPattern.MatchString(sessionID) || !validPurchase(expected) {
		return Evidence{}, failure(CodeInput, StageSession, ConstraintInput, OutcomeNotAttempted, 0)
	}
	// One budget for the complete read chain, not three independent deadlines.
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	s, err := c.readSession(ctx, sessionID)
	if err != nil {
		return Evidence{}, err
	}
	if constraint := validateSession(s, expected); constraint != "" {
		return Evidence{}, failure(CodeEvidence, StageSession, constraint, OutcomeReadUnavailable, responseStatus(s.LastResponse))
	}
	if s.ID != sessionID {
		return Evidence{}, failure(CodeEvidence, StageSession, ConstraintSession, OutcomeReadUnavailable, responseStatus(s.LastResponse))
	}
	if s.Status != stripe.CheckoutSessionStatusComplete || s.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid || s.PaymentIntent == nil {
		return Evidence{}, failure(CodeEvidence, StageSession, ConstraintPaid, OutcomeReadUnavailable, responseStatus(s.LastResponse))
	}
	pi, err := c.readIntent(ctx, s.PaymentIntent.ID)
	if err != nil {
		return Evidence{}, err
	}
	if pi.Livemode {
		return Evidence{}, failure(CodeEvidence, StageIntent, ConstraintContext, OutcomeReadUnavailable, responseStatus(pi.LastResponse))
	}
	if pi.ID != s.PaymentIntent.ID || pi.Object != "payment_intent" || pi.Status != stripe.PaymentIntentStatusSucceeded || pi.Currency != stripe.CurrencyUSD || pi.Amount != int64(expected.GrossCents) || pi.AmountReceived != int64(expected.GrossCents) || pi.AmountCapturable != 0 || pi.CaptureMethod != stripe.PaymentIntentCaptureMethodAutomatic ||
		pi.LatestCharge == nil || !chargePattern.MatchString(pi.LatestCharge.ID) || len(pi.PaymentMethodTypes) != 1 || pi.PaymentMethodTypes[0] != "card" || pi.OnBehalfOf != nil || pi.TransferData != nil || pi.Customer != nil || pi.CustomerAccount != "" || pi.SetupFutureUsage != "" || len(pi.Metadata) != 0 {
		return Evidence{}, failure(CodeEvidence, StageIntent, ConstraintIntent, OutcomeReadUnavailable, responseStatus(pi.LastResponse))
	}
	charge, err := c.readCharge(ctx, pi.LatestCharge.ID)
	if err != nil {
		return Evidence{}, err
	}
	if charge.Livemode {
		return Evidence{}, failure(CodeEvidence, StageCharge, ConstraintContext, OutcomeReadUnavailable, responseStatus(charge.LastResponse))
	}
	if charge.ID != pi.LatestCharge.ID || charge.Object != "charge" || charge.PaymentIntent == nil || charge.PaymentIntent.ID != pi.ID || charge.Status != stripe.ChargeStatusSucceeded || !charge.Paid || !charge.Captured || charge.Currency != stripe.CurrencyUSD || charge.Amount != int64(expected.GrossCents) || charge.AmountCaptured != pi.AmountReceived || charge.AmountRefunded < 0 || charge.AmountRefunded > charge.Amount ||
		!validSeconds(charge.Created) || charge.Created < s.Created-60 || charge.Created > s.ExpiresAt+60 || charge.Created > time.Now().Unix()+60 ||
		charge.PaymentMethodDetails == nil || charge.PaymentMethodDetails.Type != stripe.ChargePaymentMethodDetailsTypeCard || charge.OnBehalfOf != nil || charge.TransferData != nil || charge.SourceTransfer != nil || charge.Customer != nil {
		return Evidence{}, failure(CodeEvidence, StageCharge, ConstraintCharge, OutcomeReadUnavailable, responseStatus(charge.LastResponse))
	}
	// Missing/incomplete or malformed refund evidence is unavailable, not an
	// observed refund and not permission to persist a block or fund.
	if charge.Refunds == nil || charge.Refunds.Data == nil || charge.Refunds.HasMore {
		return Evidence{}, failure(CodeEvidence, StageCharge, ConstraintRefundUnavailable, OutcomeReadUnavailable, responseStatus(charge.LastResponse))
	}
	for _, refund := range charge.Refunds.Data {
		if refund == nil || refund.Object != "refund" || refund.ID == "" || refund.Amount < 0 || refund.Amount > charge.Amount || refund.Currency != charge.Currency || refund.Charge == nil || refund.Charge.ID != charge.ID || refund.PaymentIntent == nil || refund.PaymentIntent.ID != pi.ID {
			return Evidence{}, failure(CodeEvidence, StageCharge, ConstraintRefundUnavailable, OutcomeReadUnavailable, responseStatus(charge.LastResponse))
		}
	}
	out = Evidence{
		Session: projectSession(s), AmountReceived: uint64(pi.AmountReceived), AmountCaptured: uint64(charge.AmountCaptured),
		Paid: charge.Paid, Captured: charge.Captured, Refunded: charge.Refunded,
		AmountRefunded: uint64(charge.AmountRefunded), Disputed: charge.Disputed,
		RefundObserved: len(charge.Refunds.Data) != 0, ChargeCreatedSeconds: charge.Created,
		ChargeDateUTC: time.Unix(charge.Created, 0).UTC().Format("2006-01-02"),
	}
	// This single error case deliberately returns validated canonical evidence
	// for the core's sticky block. It is NEVER successful funding evidence.
	if out.Refunded || out.AmountRefunded != 0 || out.Disputed || out.RefundObserved {
		return out, failure(CodeEvidence, StageCharge, ConstraintRefund, OutcomeReadUnavailable, responseStatus(charge.LastResponse))
	}
	return out, nil
}

func responseStatus(response *stripe.APIResponse) int {
	if response != nil {
		return response.StatusCode
	}
	return 0
}

func (c *Client) readSession(ctx context.Context, id string) (*stripe.CheckoutSession, error) {
	ctx, cancel, o := requestContext(ctx)
	defer cancel()
	p := &stripe.CheckoutSessionRetrieveParams{}
	p.AddExpand("line_items.data.price")
	s, err := c.sdk.V1CheckoutSessions.Retrieve(ctx, id, p)
	if err := providerFailure(err, o, StageSession, OutcomeReadUnavailable); err != nil {
		return nil, err
	}
	return s, nil
}

func (c *Client) readIntent(ctx context.Context, id string) (pi *stripe.PaymentIntent, err error) {
	defer recoverBoundary(&err, StageIntent, OutcomeReadUnavailable)
	ctx, cancel, o := requestContext(ctx)
	defer cancel()
	pi, err = c.sdk.V1PaymentIntents.Retrieve(ctx, id, nil)
	if err := providerFailure(err, o, StageIntent, OutcomeReadUnavailable); err != nil {
		return nil, err
	}
	return pi, nil
}

func (c *Client) readCharge(ctx context.Context, id string) (charge *stripe.Charge, err error) {
	defer recoverBoundary(&err, StageCharge, OutcomeReadUnavailable)
	ctx, cancel, o := requestContext(ctx)
	defer cancel()
	p := &stripe.ChargeRetrieveParams{}
	p.AddExpand("refunds")
	charge, err = c.sdk.V1Charges.Retrieve(ctx, id, p)
	if err := providerFailure(err, o, StageCharge, OutcomeReadUnavailable); err != nil {
		return nil, err
	}
	return charge, nil
}
