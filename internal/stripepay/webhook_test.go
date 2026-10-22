package stripepay

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v87/webhook"
)

func eventFixture() object {
	return object{"object": "event", "api_version": APIVersion, "livemode": false, "type": "checkout.session.completed", "created": 1, "id": "evt_synthetic", "data": object{"object": object{"object": "checkout.session", "id": fixtureSession, "livemode": false, "client_reference_id": strings.Repeat("a", 64), "payment_status": "unpaid", "customer_email": hostile}}}
}

func signFixture(raw []byte, secret string, at time.Time) string {
	return webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: raw, Secret: secret, Timestamp: at}).Header
}

func TestWebhookSignatureVersionContextAndInertKinds(t *testing.T) {
	f := newFixture(t)
	c := offlineClient(t, f.server.URL)
	for _, tt := range []struct {
		name       string
		change     func(object)
		kind       EventKind
		constraint Constraint
	}{
		{"completed", func(e object) {}, EventCheckoutCompleted, ""},
		{"async_success", func(e object) { e["type"] = "checkout.session.async_payment_succeeded" }, EventCheckoutAsyncSucceeded, ""},
		{"irrelevant", func(e object) { e["type"] = "customer.created" }, EventIrrelevant, ""},
		{"unknown_future", func(e object) { e["type"] = "checkout.session.future_success" }, EventIrrelevant, ""},
		{"intent_success_not_authority", func(e object) { e["type"] = "payment_intent.succeeded" }, EventIrrelevant, ""},
		{"refund_inert", func(e object) { e["type"] = "charge.refunded" }, EventIrrelevant, ""},
		{"dispute_inert", func(e object) { e["type"] = "charge.dispute.created" }, EventIrrelevant, ""},
		{"old_api", func(e object) { e["api_version"] = "2024-06-20" }, "", ConstraintContext},
		{"same_train_different_api", func(e object) { e["api_version"] = "2026-10-31.endive" }, "", ConstraintContext},
		{"live", func(e object) { e["livemode"] = true }, "", ConstraintContext},
		{"connected_account", func(e object) { e["account"] = "acct_synthetic" }, "", ConstraintContext},
		{"context", func(e object) { e["context"] = hostile }, "", ConstraintContext},
		{"live_session", func(e object) { e["data"].(object)["object"].(object)["livemode"] = true }, "", ConstraintEvent},
		{"wrong_session_id", func(e object) { e["data"].(object)["object"].(object)["id"] = "cs_live_synthetic" }, "", ConstraintEvent},
		{"wrong_object", func(e object) { e["data"].(object)["object"].(object)["object"] = "payment_intent" }, "", ConstraintEvent},
		{"missing_data", func(e object) { e["data"] = nil }, "", ConstraintEvent},
		{"missing_reference", func(e object) { delete(e["data"].(object)["object"].(object), "client_reference_id") }, "", ConstraintEvent},
		{"invalid_reference", func(e object) { e["data"].(object)["object"].(object)["client_reference_id"] = hostile }, "", ConstraintEvent},
	} {
		t.Run(tt.name, func(t *testing.T) {
			event := eventFixture()
			tt.change(event)
			raw, _ := json.Marshal(event)
			got, err := c.DecodeWebhook(raw, signFixture(raw, FixtureWebhookSecret, time.Now()))
			if tt.constraint != "" {
				requireFailure(t, err, StageWebhook, tt.constraint, OutcomeWebhookRejected)
				return
			}
			if err != nil || got.Kind != tt.kind {
				t.Fatal("webhook result mismatch")
			}
			if tt.kind == EventIrrelevant && got.SessionID != "" {
				t.Fatal("irrelevant event retained identity")
			}
			if tt.kind != EventIrrelevant && (got.SessionID != fixtureSession || got.Reference != strings.Repeat("a", 64)) {
				t.Fatal("session identity missing")
			}
			b, _ := json.Marshal(got)
			if strings.Contains(string(b), hostile) {
				t.Fatal("raw event retained")
			}
		})
	}
	raw, _ := json.Marshal(eventFixture())
	valid := signFixture(raw, FixtureWebhookSecret, time.Now())
	for _, tt := range []struct {
		name       string
		body       []byte
		signature  string
		constraint Constraint
	}{
		{"invalid_header", raw, hostile, ConstraintSignature},
		{"missing_signature", raw, "", ConstraintSignature},
		{"wrong_secret", raw, signFixture(raw, "whsec_other_synthetic", time.Now()), ConstraintSignature},
		{"stale", raw, signFixture(raw, FixtureWebhookSecret, time.Now().Add(-webhook.DefaultTolerance-time.Minute)), ConstraintSignature},
		{"altered_bytes", append(append([]byte(nil), raw...), byte(' ')), valid, ConstraintSignature},
		{"oversized", []byte(strings.Repeat(hostile, MaxWebhookBytes/len(hostile)+2)), valid, ConstraintWebhookSize},
		{"oversized_header", raw, strings.Repeat(hostile, 4097), ConstraintWebhookSize},
		{"signed_hostile_json", []byte(hostile), signFixture([]byte(hostile), FixtureWebhookSecret, time.Now()), ConstraintEvent},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := c.DecodeWebhook(tt.body, tt.signature)
			requireFailure(t, err, StageWebhook, tt.constraint, OutcomeWebhookRejected)
		})
	}
	if len(f.snapshot()) != 0 {
		t.Fatal("webhook decoding initiated provider work")
	}
}
