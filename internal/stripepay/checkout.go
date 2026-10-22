package stripepay

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v87"
)

// CreateCheckout performs one SDK call, never retries or resolves an earlier
// uncertain create. The caller MUST durably claim the original attempt first.
// No local registry is maintained: calling this method again is another attempt
// and must be prevented by the accounting owner, including after restart.
func (c *Client) CreateCheckout(ctx context.Context, in CreateRequest) (out Checkout, err error) {
	defer recoverBoundary(&err, StageCreate, OutcomeCreateUnknown)
	p := in.Purchase
	now := time.Now()
	if !c.configured() || ctx == nil || !validPurchase(p) || !tokenPattern.MatchString(in.IdempotencyKey) || in.IdempotencyKey == p.Reference || now.Before(p.IssuedAt.Add(-time.Minute)) || now.After(p.IssuedAt.Add(5*time.Minute)) {
		return Checkout{}, failure(CodeInput, StageCreate, ConstraintInput, OutcomeNotAttempted, 0)
	}
	ctx, cancel, observed := requestContext(ctx)
	defer cancel()
	params := &stripe.CheckoutSessionCreateParams{
		Mode: stripe.String("payment"), UIMode: stripe.String("hosted_page"),
		CustomerCreation:  stripe.String("if_required"),
		ClientReferenceID: stripe.String(p.Reference),
		SuccessURL:        stripe.String(c.returnURL), CancelURL: stripe.String(c.returnURL),
		ExpiresAt: stripe.Int64(p.ExpiresAt.Unix()),
		// Endive replaces the old static parameter with a strict allowed-method
		// filter. Only card may be offered; provider/dashboard availability is
		// NOT qualified by these offline fixtures.
		AllowedPaymentMethodTypes: stripe.StringSlice([]string{"card"}),
		// Card filtering alone does not disable Link's saved-information UI.
		WalletOptions: &stripe.CheckoutSessionCreateWalletOptionsParams{Link: &stripe.CheckoutSessionCreateWalletOptionsLinkParams{Display: stripe.String("never")}},
		PaymentIntentData:         &stripe.CheckoutSessionCreatePaymentIntentDataParams{CaptureMethod: stripe.String("automatic")},
		AdaptivePricing:           &stripe.CheckoutSessionCreateAdaptivePricingParams{Enabled: stripe.Bool(false)},
		AllowPromotionCodes:       stripe.Bool(false),
		AfterExpiration:           &stripe.CheckoutSessionCreateAfterExpirationParams{Recovery: &stripe.CheckoutSessionCreateAfterExpirationRecoveryParams{Enabled: stripe.Bool(false)}},
		InvoiceCreation:           &stripe.CheckoutSessionCreateInvoiceCreationParams{Enabled: stripe.Bool(false)},
		LineItems: []*stripe.CheckoutSessionCreateLineItemParams{{
			Quantity:           stripe.Int64(1),
			AdjustableQuantity: &stripe.CheckoutSessionCreateLineItemAdjustableQuantityParams{Enabled: stripe.Bool(false)},
			PriceData: &stripe.CheckoutSessionCreateLineItemPriceDataParams{
				Currency: stripe.String("usd"), UnitAmount: stripe.Int64(int64(p.GrossCents)),
				ProductData: &stripe.CheckoutSessionCreateLineItemPriceDataProductDataParams{Name: stripe.String("Synthetic prepaid credit")},
			},
		}},
	}
	params.SetIdempotencyKey(in.IdempotencyKey)
	params.AddExpand("line_items.data.price")
	s, sdkErr := c.sdk.V1CheckoutSessions.Create(ctx, params)
	if err := providerFailure(sdkErr, observed, StageCreate, OutcomeCreateUnknown); err != nil {
		return Checkout{}, err
	}
	if constraint := validateSession(s, p); constraint != "" {
		return Checkout{}, failure(CodeEvidence, StageCreate, constraint, OutcomeCreateUnknown, observed.status)
	}
	if s.Status != stripe.CheckoutSessionStatusOpen || s.PaymentStatus != stripe.CheckoutSessionPaymentStatusUnpaid {
		return Checkout{}, failure(CodeEvidence, StageCreate, ConstraintSession, OutcomeCreateUnknown, observed.status)
	}
	if !c.validHostedURL(s.URL) {
		constraint := ConstraintURL
		if c.sandbox {
			constraint = ConstraintSandboxURL
		}
		return Checkout{}, failure(CodeEvidence, StageHostedURL, constraint, OutcomeCreateUnknown, observed.status)
	}
	return Checkout{Session: projectSession(s), HostedURL: s.URL}, nil
}

// CheckoutURLAllowed exposes only the constructor's fixed browser-origin policy.
// Offline navigation remains fixture-only; sandbox requires a test Checkout path.
// It grants no network action and accepts no browser-selected origin override.
func (c *Client) CheckoutURLAllowed(value string) bool {
	return c.configured() && c.validHostedURL(value)
}

// CheckoutOrigin is the constructor's fixed navigation origin, not its API
// origin or a browser input. The zero client grants no origin.
func (c *Client) CheckoutOrigin() string {
	if !c.configured() {
		return ""
	}
	return c.origin
}

func (c *Client) validHostedURL(value string) bool {
	if len(value) == 0 || len(value) > 4096 {
		return false
	}
	u, err := url.Parse(value)
	if c.sandbox {
		return err == nil && u.Scheme+"://"+u.Host == sandboxCheckoutOrigin && u.User == nil && u.Opaque == "" &&
			u.RawQuery == "" && !u.ForceQuery && u.RawPath == "" && u.String() == value &&
			strings.HasPrefix(u.Path, "/c/pay/") && sessionPattern.MatchString(strings.TrimPrefix(u.Path, "/c/pay/")) &&
			len(u.Fragment) <= 2048 && printableFragment(u.Fragment)
	}
	return err == nil && u.Scheme+"://"+u.Host == c.origin && u.User == nil && u.Opaque == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawFragment == "" && u.RawPath == "" && len(u.Path) > 1 && u.Path[0] == '/'
}

func printableFragment(fragment string) bool {
	for _, b := range []byte(fragment) {
		if b < 0x21 || b > 0x7e {
			return false
		}
	}
	return true
}

func validateSession(s *stripe.CheckoutSession, p Purchase) Constraint {
	if s == nil || s.Livemode {
		return ConstraintContext
	}
	if s.Object != "checkout.session" || !sessionPattern.MatchString(s.ID) || s.Mode != stripe.CheckoutSessionModePayment || s.UIMode != stripe.CheckoutSessionUIModeHostedPage ||
		s.ClientReferenceID != p.Reference || s.Currency != stripe.CurrencyUSD || s.AmountTotal != int64(p.GrossCents) || s.AmountSubtotal != int64(p.GrossCents) ||
		!validSeconds(s.Created) || s.Created < p.IssuedAt.Unix()-60 || s.Created > p.IssuedAt.Unix()+360 || s.Created > time.Now().Unix()+60 || s.ExpiresAt != p.ExpiresAt.Unix() || s.Created >= s.ExpiresAt ||
		s.RecoveredFrom != "" || s.PaymentLink != nil || s.Subscription != nil || s.SetupIntent != nil || s.Customer != nil || s.CustomerAccount != "" || s.CustomerCreation != stripe.CheckoutSessionCustomerCreationIfRequired ||
		len(s.Metadata) != 0 || len(s.Discounts) != 0 || s.AllowPromotionCodes || len(s.PaymentMethodTypes) != 1 || s.PaymentMethodTypes[0] != "card" ||
		len(s.AllowedPaymentMethodTypes) != 1 || s.AllowedPaymentMethodTypes[0] != "card" ||
		(s.AdaptivePricing != nil && s.AdaptivePricing.Enabled) || (s.AfterExpiration != nil && s.AfterExpiration.Recovery != nil && s.AfterExpiration.Recovery.Enabled) {
		return ConstraintSession
	}
	if s.PaymentIntent != nil && !intentPattern.MatchString(s.PaymentIntent.ID) {
		return ConstraintIntent
	}
	if s.LineItems == nil || s.LineItems.HasMore || len(s.LineItems.Data) != 1 {
		return ConstraintLineItem
	}
	item := s.LineItems.Data[0]
	if item == nil || item.Object != "item" || item.Quantity != 1 || item.Currency != stripe.CurrencyUSD || item.AmountTotal != int64(p.GrossCents) || item.AmountSubtotal != int64(p.GrossCents) || item.AmountDiscount != 0 || item.AmountTax != 0 ||
		(item.AdjustableQuantity != nil && item.AdjustableQuantity.Enabled) || len(item.Discounts) != 0 || item.Price == nil {
		return ConstraintLineItem
	}
	price := item.Price
	if price.Object != "price" || price.Livemode || price.Deleted || price.Currency != stripe.CurrencyUSD || price.UnitAmount != int64(p.GrossCents) || price.BillingScheme != stripe.PriceBillingSchemePerUnit || price.Type != stripe.PriceTypeOneTime || price.Recurring != nil || price.CustomUnitAmount != nil || price.TransformQuantity != nil {
		return ConstraintLineItem
	}
	// v87 uses exact decimal.Decimal, not float64. The integer is mandatory;
	// an optional decimal representation may only agree exactly with it.
	if price.UnitAmountDecimal.Exponent() < -12 || price.UnitAmountDecimal.Exponent() > 4 || (!price.UnitAmountDecimal.IsZero() && price.UnitAmountDecimal.String() != strconv.FormatUint(p.GrossCents, 10)) {
		return ConstraintLineItem
	}
	return ""
}

func projectSession(s *stripe.CheckoutSession) Session {
	out := Session{Reference: s.ClientReferenceID, SessionID: s.ID, CreatedSeconds: s.Created, ExpiresSeconds: s.ExpiresAt, Currency: "usd", GrossCents: uint64(s.AmountTotal), Quantity: 1}
	if s.PaymentIntent != nil {
		out.PaymentIntentID = s.PaymentIntent.ID
	}
	return out
}
