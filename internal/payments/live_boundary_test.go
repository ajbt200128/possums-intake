package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v87/webhook"
	"possums-intake/internal/stripepay"
)

func TestLiveSignedWrongModeAndContextRejectedBeforeRetrieval(t *testing.T) {
	f, a := newLiveFixture(t)
	ref := liveStart(t, f)
	for _, test := range []struct {
		name   string
		change func(object)
	}{
		{"test_event", func(e object) { e["livemode"] = false }},
		{"test_session", func(e object) { e["data"].(object)["object"].(object)["livemode"] = false }},
		{"connected_context", func(e object) { e["context"] = privateSentinel }},
		{"api_version", func(e object) { e["api_version"] = "2024-06-20" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := event(ref)
			e["livemode"] = true
			snapshot := e["data"].(object)["object"].(object)
			snapshot["livemode"], snapshot["id"] = true, "cs_live_synthetic"
			test.change(e)
			raw, err := json.Marshal(e)
			if err != nil {
				t.Fatal("synthetic event encoding")
			}
			r := httptest.NewRequest("POST", WebhookPath, bytes.NewReader(raw))
			r.Header.Set("Stripe-Signature", webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: raw, Secret: liveFixtureSecret, Timestamp: time.Now()}).Header)
			w := httptest.NewRecorder()
			f.service.WebhookHandler().ServeHTTP(w, r)
			var reply webhookReply
			if w.Code != 400 || json.Unmarshal(w.Body.Bytes(), &reply) != nil || reply.Error == nil || bytes.Contains(w.Body.Bytes(), []byte(privateSentinel)) {
				t.Fatal("signed mode/context failure not safely rejected")
			}
		})
	}
	if creates, reads := a.calls(); creates != 1 || reads != 0 {
		t.Fatal("rejected signature context dispatched")
	}
	f.assertBalance(t, "0")
}

func TestLiveExpiryRecheckedAfterCanonicalReadWithoutReplay(t *testing.T) {
	f, a := newLiveFixture(t)
	ref := liveStart(t, f)
	p, err := f.core.GetPayment(context.Background(), testAccount, ref)
	if err != nil {
		t.Fatal(err)
	}
	a.onRead = func() {
		// Completing a local accounting operation inside the adapter callback
		// also proves that retrieval is outside the serialized writer gate.
		if _, err := f.core.Balance(context.Background(), testAccount); err != nil {
			t.Error("read held accounting gate")
		}
		f.exec(t, "UPDATE ledger_meta SET clock_high_water_ms=?", int64(p.UnlinkAt))
	}
	if code, r := deliverLive(t, f.service, ref); code != 200 || r.Status != "ineligible" {
		t.Fatal("remote read leased eligibility")
	}
	if f.count(t, "payment_sales") != 0 {
		t.Fatal("late canonical evidence created sale")
	}
	f.assertBalance(t, "0")
	if creates, reads := a.calls(); creates != 1 || reads != 1 {
		t.Fatal("expiry triggered replay")
	}
}

func TestLiveNonCanonicalRefundErrorsNeverBlockOrFund(t *testing.T) {
	f, a := newLiveFixture(t)
	ref := liveStart(t, f)
	for _, err := range []*stripepay.Error{
		{Code: stripepay.CodeProvider, Stage: stripepay.StageCharge, Constraint: stripepay.ConstraintRefund, Outcome: stripepay.OutcomeReadUnavailable},
		{Code: stripepay.CodeEvidence, Stage: stripepay.StageCharge, Constraint: stripepay.ConstraintRefundUnavailable, Outcome: stripepay.OutcomeReadUnavailable},
	} {
		a.change(func() { a.evidence.RefundObserved = true; a.readErr = err })
		if code, r := deliverLive(t, f.service, ref); code != 503 || r.Error == nil {
			t.Fatal("noncanonical error authorized block")
		}
		assertStatus(t, f.service, ref, "awaiting_payment")
	}
	f.assertBalance(t, "0")
	if f.count(t, "payment_sales") != 0 {
		t.Fatal("read error created sale")
	}
}
