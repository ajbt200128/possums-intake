package stripepay

import (
	"math"

	"github.com/stripe/stripe-go/v87"
)

// Open Checkout has no stable tax quote. Only immutable product/principal and
// supported configuration are bound here; final totals/status are checked later.
func validateLiveSession(s *stripe.CheckoutSession, p Purchase) Constraint {
	if s.AutomaticTax == nil || !s.AutomaticTax.Enabled ||
		(s.AutomaticTax.Provider != "" && s.AutomaticTax.Provider != "stripe") ||
		(s.AutomaticTax.Liability != nil && (s.AutomaticTax.Liability.Type != stripe.CheckoutSessionAutomaticTaxLiabilityTypeSelf || s.AutomaticTax.Liability.Account != nil)) {
		return ConstraintTax
	}
	price := s.LineItems.Data[0].Price
	product := price.Product
	if price.TaxBehavior != stripe.PriceTaxBehaviorExclusive || product == nil || product.Object != "product" || product.Deleted || !product.Livemode ||
		product.TaxCode == nil || product.TaxCode.ID != p.TaxCode ||
		(product.TaxDetails != nil && (product.TaxDetails.PerformanceLocation != "" || (product.TaxDetails.TaxCode != "" && product.TaxDetails.TaxCode != p.TaxCode))) {
		return ConstraintTax
	}
	if s.BillingAddressCollection != stripe.CheckoutSessionBillingAddressCollectionAuto || s.Invoice != nil ||
		(s.InvoiceCreation != nil && s.InvoiceCreation.Enabled) || (s.ManagedPayments != nil && s.ManagedPayments.Enabled) ||
		(s.TaxIDCollection != nil && s.TaxIDCollection.Enabled) || s.ShippingAddressCollection != nil || len(s.ShippingOptions) != 0 ||
		(s.ShippingCost != nil && (s.ShippingCost.AmountSubtotal != 0 || s.ShippingCost.AmountTax != 0 || s.ShippingCost.AmountTotal != 0 || s.ShippingCost.ShippingRate != nil || len(s.ShippingCost.Taxes) != 0)) ||
		(s.TotalDetails != nil && (s.TotalDetails.AmountDiscount != 0 || s.TotalDetails.AmountShipping != 0)) ||
		(s.SavedPaymentMethodOptions != nil && s.SavedPaymentMethodOptions.PaymentMethodSave != "" && s.SavedPaymentMethodOptions.PaymentMethodSave != stripe.CheckoutSessionSavedPaymentMethodOptionsPaymentMethodSaveDisabled) ||
		len(price.Metadata) != 0 || len(product.Metadata) != 0 || len(s.LineItems.Data[0].Metadata) != 0 {
		return ConstraintTax
	}
	return ""
}

// validateFinalTax follows SDK integer/nullable semantics, never a local rate or
// an inference from tax=0. This narrow flow requires explicit self liability and
// Stripe provider evidence; null/absent evidence cannot establish either fact.
func validateFinalTax(s *stripe.CheckoutSession) Constraint {
	a := s.AutomaticTax
	if a == nil || !a.Enabled || a.Status != stripe.CheckoutSessionAutomaticTaxStatusComplete || a.Provider != "stripe" ||
		a.Liability == nil || a.Liability.Type != stripe.CheckoutSessionAutomaticTaxLiabilityTypeSelf || a.Liability.Account != nil || s.TotalDetails == nil {
		return ConstraintTaxComplete
	}
	tax := s.TotalDetails.AmountTax
	item := s.LineItems.Data[0]
	if tax < 0 || s.AmountSubtotal < 0 || s.AmountTotal < 0 || tax > math.MaxInt64-s.AmountSubtotal ||
		item.AmountTax != tax || s.AmountSubtotal+tax != item.AmountTotal || item.AmountTotal != s.AmountTotal ||
		s.TotalDetails.AmountDiscount != 0 || s.TotalDetails.AmountShipping != 0 {
		return ConstraintTaxComplete
	}
	return ""
}
