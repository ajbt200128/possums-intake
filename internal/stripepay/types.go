// Package stripepay provides explicit offline and local sandbox Stripe adapters.
// It has no live-mode constructor, environment credential lookup, persistence,
// accounting dependency, activity hooks, or automatic create replay.
package stripepay

import "time"

const (
	// These public placeholders are the ONLY accepted NewOffline credentials.
	// Never supply real credentials (including test-mode secrets) to NewOffline.
	FixtureAPIKey        = "sk_test_offline_fixture_only"
	FixtureWebhookSecret = "whsec_offline_fixture_only"
	APIVersion           = "2026-09-30.endive"
	ReturnPath           = "/topup/return"
	MaxResponseBytes     = 1 << 20
	MaxWebhookBytes      = 256 << 10
)

// OfflineConfig accepts literal loopback IP origins only, never DNS, proxies,
// real credentials, custom transports, or a production Stripe fallback.
// ReturnOrigin is fixture configuration, not a browser-selected input.
type OfflineConfig struct {
	Endpoint      string
	ReturnOrigin  string
	APIKey        string
	WebhookSecret string
}

// Purchase is the original server-owned immutable purchase, not event/browser
// authority. References and create keys are independent random 32-byte values
// encoded as lowercase hex. The caller generates and retains these; this
// adapter can validate shape/inequality, not prove randomness or uniqueness.
// ExpiresAt is IssuedAt + one hour rounded DOWN to provider seconds. IssuedAt
// retains its original millisecond precision; do not round the local cutoff.
// The caller MUST recheck local link eligibility and binding in its transaction
// after retrieval; a read is neither a funding receipt nor an eligibility lease.
type Purchase struct {
	Reference  string
	GrossCents uint64
	IssuedAt   time.Time
	ExpiresAt  time.Time
}

type CreateRequest struct {
	Purchase       Purchase
	IdempotencyKey string
}

// Session contains only private mapping/validation fields, not customer output.
type Session struct {
	Reference       string
	SessionID       string
	PaymentIntentID string
	CreatedSeconds  int64
	ExpiresSeconds  int64
	Currency        string
	GrossCents      uint64
	Quantity        int
}

// Checkout's URL is transient browser navigation data. Never persist, log,
// serialize into diagnostics, or treat it as payment evidence.
type Checkout struct {
	Session   Session
	HostedURL string
}

// Evidence with nil error is validated current captured, paid, unrefunded and
// undisputed state. ONLY CodeEvidence/StageCharge/ConstraintRefund also returns
// validated canonical Evidence with an error, for a sticky block, NEVER funding.
// Every other error returns zero Evidence. It is not a financial receipt:
// a concurrent refund can race this remote read.
// Charge creation is NOT capture time or event-delivery time. No Charge ID,
// provider object, body, event history, customer or card details are returned.
type Evidence struct {
	Session              Session
	AmountReceived       uint64
	AmountCaptured       uint64
	Paid                 bool
	Captured             bool
	Refunded             bool
	AmountRefunded       uint64
	Disputed             bool
	RefundObserved       bool // Any validated listed refund, regardless of status.
	ChargeCreatedSeconds int64
	ChargeDateUTC        string
}

type EventKind string

const (
	EventIrrelevant             EventKind = "irrelevant"
	EventCheckoutCompleted      EventKind = "checkout_completed"
	EventCheckoutAsyncSucceeded EventKind = "checkout_async_succeeded"
)

// Notification only triggers canonical reads. Reference is a signed LOOKUP
// SELECTOR, not authority: canonical retrieval must match the retained original
// reference/terms before binding or funding. No event payment data is evidence.
// Refund/dispute and unknown event kinds remain inert; current canonical Charge
// refund/dispute state is checked on relevant Checkout notifications.
type Notification struct {
	Kind      EventKind
	SessionID string
	Reference string
}
