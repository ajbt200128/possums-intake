package ledger

import (
	"regexp"
	"time"
)

const (
	paymentLinkMS     int64 = 20 * 24 * 60 * 60 * 1000
	paymentCreateMS   int64 = 5 * 60 * 1000
	paymentCheckoutMS int64 = 60 * 60 * 1000
	paymentFormMS     int64 = 10 * 60 * 1000
	paymentSalesDays        = 90 // Synthetic fixtures only; not a legal/business retention policy.
	paymentMaxTime    int64 = 9999999999999
)

// PaymentDenomination returns a value copy; callers cannot alter the price list.
func PaymentDenomination(choice string) (PaymentTerms, bool) {
	switch choice {
	case "usd5":
		return PaymentTerms{"usd", "500", "5000000"}, true
	case "usd10":
		return PaymentTerms{"usd", "1000", "10000000"}, true
	case "usd25":
		return PaymentTerms{"usd", "2500", "25000000"}, true
	case "usd50":
		return PaymentTerms{"usd", "5000", "50000000"}, true
	default:
		return PaymentTerms{}, false
	}
}

type PaymentTerms struct {
	Currency    string
	GrossMinor  Money
	CreditUnits Money
}

// PaymentForm is a server-issued, session-held form, not browser authority.
// The caller MUST verify the live session's exact attempt and original window
// on EVERY call (including duplicates). Restart/session invalidation destroys
// that authority. Never reconstruct an issued form from persisted purchases.
type PaymentForm struct {
	Attempt   string
	IssuedAt  Millis
	ExpiresAt Millis
}

type PreparePaymentInput struct {
	AccountID    string
	Denomination string
	Form         PaymentForm
}

// PaymentPurchase is PRIVATE adapter data, not a customer response DTO. In
// particular never serialize this struct into diagnostics or website output.
// CheckoutExpiresAt is issuance + one hour rounded DOWN to provider seconds;
// UnlinkAt retains the exact issuance-millisecond + 20-day cutoff.
type PaymentPurchase struct {
	Reference         string  `db:"purchase_ref"`
	FormAttempt       string  `db:"form_attempt"`
	AccountID         string  `db:"account_id"`
	Currency          string  `db:"currency"`
	GrossMinor        Money   `db:"charge_minor"`
	CreditUnits       Money   `db:"credit_units"`
	IssuedAt          Millis  `db:"issued_ms"`
	UnlinkAt          Millis  `db:"unlink_at"`
	CheckoutExpiresAt Millis  `db:"checkout_expires_at"`
	FormIssuedAt      Millis  `db:"form_issued_ms"`
	FormExpiresAt     Millis  `db:"form_expires_at"`
	CreateKey         string  `db:"create_key"`
	CreateState       string  `db:"create_state"`
	SessionID         *string `db:"stripe_session_id"`
	PaymentIntentID   *string `db:"stripe_payment_intent_id"`
	SessionCreatedAt  *Millis `db:"session_created_ms"`
	SaleID            *string `db:"sale_id"`
	FundingState      string  `db:"funding_state"`
}

// StripeSessionEvidence contains only canonical, already validated adapter
// evidence. The adapter verifies signature/context/API version, line items,
// canonical Session->PaymentIntent->Charge relationships and expected provider
// account OUTSIDE the transaction. Event timestamps are deliberately absent.
// This synthetic core always rejects LiveMode; there is no live initializer.
type StripeSessionEvidence struct {
	Reference       string
	SessionID       string
	PaymentIntentID string // May be empty on the original unpaid create response.
	CreatedSeconds  int64
	ExpiresSeconds  int64
	LiveMode        bool
	Mode            string
	Currency        string
	GrossMinor      Money
	Quantity        int
}

type StripeFundingEvidence struct {
	Session              StripeSessionEvidence
	PaymentStatus        string
	IntentStatus         string
	IntentAmount         Money
	AmountReceived       Money
	ChargeStatus         string
	ChargeCreatedSeconds int64
	ChargeCurrency       string
	ChargeAmount         Money
	AmountCaptured       Money
	Paid                 bool
	Captured             bool
	Refunded             bool
	AmountRefunded       Money
	Disputed             bool
	// Any relevant refund evidence, including pending/failed/unknown statuses,
	// is sticky before funding; a later success cannot automatically unblock it.
	RefundObserved bool
}

// PaymentFundingResult confirms original funding, NOT current availability.
// No balance-after or durable provider bridge is added to sales.
type PaymentFundingResult struct {
	Status PaymentFundingStatus
}
type PaymentFundingStatus string

const (
	PaymentFunded    PaymentFundingStatus = "funded"
	PaymentDuplicate PaymentFundingStatus = "duplicate"
	PaymentBlocked   PaymentFundingStatus = "blocked"
)

type PaymentPurgeResult struct {
	Pruned int
	More   bool // Caller must drain bounded batches before opening payment admission.
}

var paymentTokenPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var paymentSessionPattern = regexp.MustCompile(`^cs_[A-Za-z0-9_]{1,200}$`)
var paymentIntentPattern = regexp.MustCompile(`^pi_[A-Za-z0-9_]{1,200}$`)

func paymentTermsValid(currency string, gross, units Money) bool {
	for _, choice := range []string{"usd5", "usd10", "usd25", "usd50"} {
		t, _ := PaymentDenomination(choice)
		if t == (PaymentTerms{currency, gross, units}) {
			return true
		}
	}
	return false
}
func paymentSeconds(seconds int64) (int64, bool) {
	if seconds <= 0 || seconds > paymentMaxTime/1000 {
		return 0, false
	}
	return seconds * 1000, true
}
func paymentSaleDate(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02") }
func paymentSaleCutoff(now int64) string {
	return time.UnixMilli(now).UTC().AddDate(0, 0, -paymentSalesDays).Format("2006-01-02")
}
