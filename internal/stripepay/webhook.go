package stripepay

import (
	"encoding/json"
	"errors"

	"github.com/stripe/stripe-go/v87"
	"github.com/stripe/stripe-go/v87/webhook"
)

// DecodeWebhook uses supported SDK signature verification with its default
// five-minute tolerance and API compatibility enforcement, plus an exact pinned
// API check. No manual HMAC, tolerance bypass, API bypass, network or persistence.
func (c *Client) DecodeWebhook(raw []byte, signature string) (out Notification, err error) {
	defer recoverBoundary(&err, StageWebhook, OutcomeWebhookRejected)
	if !c.configured() {
		return Notification{}, failure(CodeConfiguration, StageWebhook, ConstraintOffline, OutcomeWebhookRejected, 0)
	}
	if len(raw) > MaxWebhookBytes || len(signature) > 4096 {
		return Notification{}, failure(CodeWebhook, StageWebhook, ConstraintWebhookSize, OutcomeWebhookRejected, 0)
	}
	event, sdkErr := webhook.ConstructEvent(raw, signature, c.webhookSecret)
	if sdkErr != nil {
		if errors.Is(sdkErr, webhook.ErrInvalidHeader) || errors.Is(sdkErr, webhook.ErrNoValidSignature) || errors.Is(sdkErr, webhook.ErrNotSigned) || errors.Is(sdkErr, webhook.ErrTooOld) || errors.Is(sdkErr, webhook.ErrEmptySecret) {
			return Notification{}, failure(CodeWebhook, StageWebhook, ConstraintSignature, OutcomeWebhookRejected, 0)
		}
		if event.APIVersion != "" && event.APIVersion != APIVersion {
			return Notification{}, failure(CodeWebhook, StageWebhook, ConstraintContext, OutcomeWebhookRejected, 0)
		}
		return Notification{}, failure(CodeDecode, StageWebhook, ConstraintEvent, OutcomeWebhookRejected, 0)
	}
	if event.Object != "event" || event.Livemode || event.Account != "" || event.Context != "" || event.APIVersion != APIVersion {
		return Notification{}, failure(CodeWebhook, StageWebhook, ConstraintContext, OutcomeWebhookRejected, 0)
	}
	kind := EventIrrelevant
	switch event.Type {
	case stripe.EventTypeCheckoutSessionCompleted:
		kind = EventCheckoutCompleted
	case stripe.EventTypeCheckoutSessionAsyncPaymentSucceeded:
		kind = EventCheckoutAsyncSucceeded
	default:
		return Notification{Kind: EventIrrelevant}, nil
	}
	if event.Data == nil {
		return Notification{}, failure(CodeWebhook, StageWebhook, ConstraintEvent, OutcomeWebhookRejected, 0)
	}
	var session stripe.CheckoutSession
	if json.Unmarshal(event.Data.Raw, &session) != nil {
		return Notification{}, failure(CodeDecode, StageWebhook, ConstraintEvent, OutcomeWebhookRejected, 0)
	}
	if session.Object != "checkout.session" || session.Livemode || !sessionPattern.MatchString(session.ID) || !tokenPattern.MatchString(session.ClientReferenceID) {
		return Notification{}, failure(CodeWebhook, StageWebhook, ConstraintEvent, OutcomeWebhookRejected, 0)
	}
	return Notification{Kind: kind, SessionID: session.ID, Reference: session.ClientReferenceID}, nil
}
