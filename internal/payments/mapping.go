package payments

import (
	"strconv"
	"time"

	"possums-intake/internal/ledger"
	"possums-intake/internal/stripepay"
)

func adapterPurchase(p ledger.PaymentPurchase) (stripepay.Purchase, error) {
	amount, err := strconv.ParseUint(string(p.GrossMinor), 10, 64)
	if err != nil {
		return stripepay.Purchase{}, localFailure("PAYMENT_TERMS_INVALID", "payment_mapping", "canonical_original_integer_required", "not_attempted")
	}
	return stripepay.Purchase{Reference: p.Reference, GrossCents: amount, IssuedAt: time.UnixMilli(int64(p.IssuedAt)), ExpiresAt: time.UnixMilli(int64(p.CheckoutExpiresAt)), TaxCode: p.TaxCode}, nil
}

func decimal(amount uint64) ledger.Money { return ledger.Money(strconv.FormatUint(amount, 10)) }

func sessionEvidence(s stripepay.Session) ledger.StripeSessionEvidence {
	return ledger.StripeSessionEvidence{Reference: s.Reference, SessionID: s.SessionID, PaymentIntentID: s.PaymentIntentID, CreatedSeconds: s.CreatedSeconds, ExpiresSeconds: s.ExpiresSeconds, LiveMode: s.LiveMode, Mode: "payment", Currency: s.Currency, GrossMinor: decimal(s.GrossCents), Quantity: s.Quantity, TaxCode: s.TaxCode}
}

// Fixed success fields are authorized ONLY after the adapter validates all
// canonical objects. A block rejection also passes here, only to persist its
// explicit refund/dispute flags; no event fields enter either projection.
func fundingEvidence(e stripepay.Evidence) ledger.StripeFundingEvidence {
	amount := decimal(e.Session.GrossCents)
	var tax *ledger.StripeTaxEvidence
	if e.Tax != nil {
		tax = &ledger.StripeTaxEvidence{Amount: decimal(e.Tax.AmountCents), Total: decimal(e.Tax.TotalCents), Complete: e.Tax.Complete, Exclusive: e.Tax.Exclusive, LiabilitySelf: e.Tax.LiabilitySelf}
		if e.Session.LiveMode {
			amount = tax.Total
		}
	}
	return ledger.StripeFundingEvidence{
		Session: sessionEvidence(e.Session), PaymentStatus: "paid", IntentStatus: "succeeded", IntentAmount: amount, AmountReceived: decimal(e.AmountReceived),
		ChargeStatus: "succeeded", ChargeCreatedSeconds: e.ChargeCreatedSeconds, ChargeCurrency: e.Session.Currency, ChargeAmount: amount, AmountCaptured: decimal(e.AmountCaptured),
		Paid: e.Paid, Captured: e.Captured, Refunded: e.Refunded, AmountRefunded: decimal(e.AmountRefunded), Disputed: e.Disputed, RefundObserved: e.RefundObserved, Tax: tax,
	}
}
