package stripepay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func lineItem(f *fixture) object { return f.session["line_items"].(object)["data"].([]any)[0].(object) }
func price(f *fixture) object    { return lineItem(f)["price"].(object) }

func TestCanonicalEvidenceRejectsUnpaidWrongTermsAndBindings(t *testing.T) {
	tests := []struct {
		name       string
		stage      Stage
		constraint Constraint
		change     func(*fixture)
	}{
		{"live_session", StageSession, ConstraintContext, func(f *fixture) { f.session["livemode"] = true }},
		{"mode", StageSession, ConstraintSession, func(f *fixture) { f.session["mode"] = "subscription" }},
		{"embedded", StageSession, ConstraintSession, func(f *fixture) { f.session["ui_mode"] = "embedded_page" }},
		{"wrong_session", StageSession, ConstraintSession, func(f *fixture) { f.session["id"] = "cs_test_other" }},
		{"wrong_reference", StageSession, ConstraintSession, func(f *fixture) { f.session["client_reference_id"] = strings.Repeat("c", 64) }},
		{"currency", StageSession, ConstraintSession, func(f *fixture) { f.session["currency"] = "eur" }},
		{"amount", StageSession, ConstraintSession, func(f *fixture) { f.session["amount_total"] = 501 }},
		{"negative_amount", StageSession, ConstraintSession, func(f *fixture) { f.session["amount_total"] = -500 }},
		{"subtotal", StageSession, ConstraintSession, func(f *fixture) { f.session["amount_subtotal"] = 501 }},
		{"expiry", StageSession, ConstraintSession, func(f *fixture) { f.session["expires_at"] = f.purchase.ExpiresAt.Unix() + 1 }},
		{"old_session", StageSession, ConstraintSession, func(f *fixture) { f.session["created"] = f.purchase.IssuedAt.Unix() - 61 }},
		{"late_session", StageSession, ConstraintSession, func(f *fixture) { f.session["created"] = f.purchase.IssuedAt.Unix() + 361 }},
		{"recovered", StageSession, ConstraintSession, func(f *fixture) { f.session["recovered_from"] = "cs_test_old" }},
		{"account", StageSession, ConstraintSession, func(f *fixture) { f.session["customer_account"] = hostile }},
		{"customer", StageSession, ConstraintSession, func(f *fixture) { f.session["customer"] = "cus_synthetic" }},
		{"metadata", StageSession, ConstraintSession, func(f *fixture) { f.session["metadata"] = object{"private": hostile} }},
		{"non_card", StageSession, ConstraintSession, func(f *fixture) { f.session["payment_method_types"] = []string{"card", "link"} }},
		{"adaptive_pricing", StageSession, ConstraintSession, func(f *fixture) { f.session["adaptive_pricing"] = object{"enabled": true} }},
		{"unpaid", StageSession, ConstraintPaid, func(f *fixture) { f.session["payment_status"] = "unpaid" }},
		{"no_payment_required", StageSession, ConstraintPaid, func(f *fixture) { f.session["payment_status"] = "no_payment_required" }},
		{"open", StageSession, ConstraintPaid, func(f *fixture) { f.session["status"] = "open" }},
		{"missing_intent", StageSession, ConstraintPaid, func(f *fixture) { f.session["payment_intent"] = nil }},
		{"bad_intent_id", StageSession, ConstraintIntent, func(f *fixture) { f.session["payment_intent"] = hostile }},
		{"quantity", StageSession, ConstraintLineItem, func(f *fixture) { lineItem(f)["quantity"] = 2 }},
		{"line_currency", StageSession, ConstraintLineItem, func(f *fixture) { lineItem(f)["currency"] = "eur" }},
		{"line_amount", StageSession, ConstraintLineItem, func(f *fixture) { lineItem(f)["amount_total"] = 1000 }},
		{"line_discount", StageSession, ConstraintLineItem, func(f *fixture) { lineItem(f)["amount_discount"] = 1 }},
		{"line_tax", StageSession, ConstraintLineItem, func(f *fixture) { lineItem(f)["amount_tax"] = 1 }},
		{"missing_lines", StageSession, ConstraintLineItem, func(f *fixture) { f.session["line_items"] = nil }},
		{"more_lines", StageSession, ConstraintLineItem, func(f *fixture) { f.session["line_items"].(object)["has_more"] = true }},
		{"two_lines", StageSession, ConstraintLineItem, func(f *fixture) { f.session["line_items"].(object)["data"] = []any{lineItem(f), lineItem(f)} }},
		{"adjustable", StageSession, ConstraintLineItem, func(f *fixture) { lineItem(f)["adjustable_quantity"] = object{"enabled": true} }},
		{"price", StageSession, ConstraintLineItem, func(f *fixture) { price(f)["unit_amount"] = 501 }},
		{"decimal_mismatch", StageSession, ConstraintLineItem, func(f *fixture) { price(f)["unit_amount_decimal"] = "500.000000000001" }},
		{"hostile_decimal_exponent", StageSession, ConstraintLineItem, func(f *fixture) { price(f)["unit_amount_decimal"] = "5e99999999" }},
		{"price_currency", StageSession, ConstraintLineItem, func(f *fixture) { price(f)["currency"] = "eur" }},
		{"recurring_price", StageSession, ConstraintLineItem, func(f *fixture) { price(f)["type"] = "recurring" }},
		{"live_price", StageSession, ConstraintLineItem, func(f *fixture) { price(f)["livemode"] = true }},
		{"pi_live", StageIntent, ConstraintContext, func(f *fixture) { f.intent["livemode"] = true }},
		{"pi_binding", StageIntent, ConstraintIntent, func(f *fixture) { f.intent["id"] = "pi_other" }},
		{"pi_processing", StageIntent, ConstraintIntent, func(f *fixture) { f.intent["status"] = "processing" }},
		{"pi_3ds", StageIntent, ConstraintIntent, func(f *fixture) { f.intent["status"] = "requires_action" }},
		{"pi_currency", StageIntent, ConstraintIntent, func(f *fixture) { f.intent["currency"] = "eur" }},
		{"pi_amount", StageIntent, ConstraintIntent, func(f *fixture) { f.intent["amount"] = 501 }},
		{"pi_received", StageIntent, ConstraintIntent, func(f *fixture) { f.intent["amount_received"] = 499 }},
		{"pi_manual", StageIntent, ConstraintIntent, func(f *fixture) { f.intent["capture_method"] = "manual" }},
		{"pi_capturable", StageIntent, ConstraintIntent, func(f *fixture) { f.intent["amount_capturable"] = 1 }},
		{"pi_missing_charge", StageIntent, ConstraintIntent, func(f *fixture) { f.intent["latest_charge"] = nil }},
		{"pi_future_usage", StageIntent, ConstraintIntent, func(f *fixture) { f.intent["setup_future_usage"] = "off_session" }},
		{"pi_context", StageIntent, ConstraintIntent, func(f *fixture) { f.intent["on_behalf_of"] = "acct_synthetic" }},
		{"pi_metadata", StageIntent, ConstraintIntent, func(f *fixture) { f.intent["metadata"] = object{"reference": hostile} }},
		{"charge_live", StageCharge, ConstraintContext, func(f *fixture) { f.charge["livemode"] = true }},
		{"charge_binding", StageCharge, ConstraintCharge, func(f *fixture) { f.charge["id"] = "ch_other" }},
		{"charge_pi_binding", StageCharge, ConstraintCharge, func(f *fixture) { f.charge["payment_intent"] = "pi_other" }},
		{"charge_failed", StageCharge, ConstraintCharge, func(f *fixture) { f.charge["status"] = "failed" }},
		{"charge_unpaid", StageCharge, ConstraintCharge, func(f *fixture) { f.charge["paid"] = false }},
		{"charge_uncaptured", StageCharge, ConstraintCharge, func(f *fixture) { f.charge["captured"] = false }},
		{"charge_partial_capture", StageCharge, ConstraintCharge, func(f *fixture) { f.charge["amount_captured"] = 499 }},
		{"charge_amount", StageCharge, ConstraintCharge, func(f *fixture) { f.charge["amount"] = 501 }},
		{"charge_currency", StageCharge, ConstraintCharge, func(f *fixture) { f.charge["currency"] = "eur" }},
		{"charge_created", StageCharge, ConstraintCharge, func(f *fixture) { f.charge["created"] = 0 }},
		{"charge_noncard", StageCharge, ConstraintCharge, func(f *fixture) { f.charge["payment_method_details"] = object{"type": "link"} }},
		{"partial_refund_false_flag", StageCharge, ConstraintRefund, func(f *fixture) { f.charge["amount_refunded"] = 1 }},
		{"full_refund", StageCharge, ConstraintRefund, func(f *fixture) { f.charge["refunded"] = true }},
		{"negative_refund", StageCharge, ConstraintCharge, func(f *fixture) { f.charge["amount_refunded"] = -1 }},
		{"disputed", StageCharge, ConstraintRefund, func(f *fixture) { f.charge["disputed"] = true }},
		{"refunds_unavailable", StageCharge, ConstraintRefundUnavailable, func(f *fixture) { delete(f.charge, "refunds") }},
		{"refunds_empty_object", StageCharge, ConstraintRefundUnavailable, func(f *fixture) { f.charge["refunds"] = object{} }},
		{"refunds_missing_data", StageCharge, ConstraintRefundUnavailable, func(f *fixture) { delete(f.charge["refunds"].(object), "data") }},
		{"refunds_null_data", StageCharge, ConstraintRefundUnavailable, func(f *fixture) { f.charge["refunds"].(object)["data"] = nil }},
		{"refunds_more", StageCharge, ConstraintRefundUnavailable, func(f *fixture) { f.charge["refunds"].(object)["has_more"] = true }},
	}
	for _, status := range []string{"pending", "requires_action", "succeeded", "failed", "canceled", "future_unknown"} {
		tests = append(tests, struct {
			name       string
			stage      Stage
			constraint Constraint
			change     func(*fixture)
		}{"refund_" + status, StageCharge, ConstraintRefund, func(f *fixture) {
			f.charge["refunds"].(object)["data"] = []any{object{"id": "re_synthetic", "object": "refund", "status": status, "amount": 1, "currency": "usd", "charge": fixtureCharge, "payment_intent": fixtureIntent}}
		}})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			tt.change(f)
			c := offlineClient(t, f.server.URL)
			got, err := c.RetrieveEvidence(context.Background(), fixtureSession, f.purchase)
			requireFailure(t, err, tt.stage, tt.constraint, OutcomeReadUnavailable)
			if tt.constraint == ConstraintRefund {
				if got.Session.SessionID != fixtureSession || !(got.Refunded || got.Disputed || got.AmountRefunded > 0 || got.RefundObserved) {
					t.Fatal("canonical block evidence missing")
				}
			} else if got != (Evidence{}) {
				t.Fatal("unvalidated evidence escaped on ordinary error")
			}
			for _, r := range f.snapshot() {
				if r.method != http.MethodGet {
					t.Fatal("negative evidence mutated provider")
				}
			}
		})
	}
}

func TestReadErrorsAreClosedAndNeverPost(t *testing.T) {
	for _, stage := range []Stage{StageSession, StageIntent, StageCharge} {
		t.Run(string(stage), func(t *testing.T) {
			f := newFixture(t)
			var reads, posts atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts.Add(1)
				}
				reads.Add(1)
				result := f.session
				current := StageSession
				if strings.HasPrefix(r.URL.Path, "/v1/payment_intents/") {
					result = f.intent
					current = StageIntent
				}
				if strings.HasPrefix(r.URL.Path, "/v1/charges/") {
					result = f.charge
					current = StageCharge
				}
				if current == stage {
					_, _ = w.Write([]byte(hostile))
					return
				}
				_ = json.NewEncoder(w).Encode(result)
			}))
			defer s.Close()
			c := offlineClient(t, s.URL)
			_, err := c.RetrieveEvidence(context.Background(), fixtureSession, f.purchase)
			requireFailure(t, err, stage, ConstraintDecode, OutcomeReadUnavailable)
			if posts.Load() != 0 || reads.Load() > 3 {
				t.Fatal("read failure triggered mutation or extra requests")
			}
		})
	}
}

func TestDateComesFromChargeNotIntentOrDelivery(t *testing.T) {
	f := newFixture(t)
	issued := time.Date(2025, 1, 1, 23, 45, 0, 0, time.UTC)
	f.purchase.IssuedAt = issued
	f.purchase.ExpiresAt = issued.Add(time.Hour)
	f.session["created"] = issued.Unix()
	f.session["expires_at"] = f.purchase.ExpiresAt.Unix()
	f.intent["created"] = issued.Unix()
	f.charge["created"] = issued.Add(30 * time.Minute).Unix()
	c := offlineClient(t, f.server.URL)
	got, err := c.RetrieveEvidence(context.Background(), fixtureSession, f.purchase)
	if err != nil {
		t.Fatal(err)
	}
	if got.ChargeDateUTC != "2025-01-02" {
		t.Fatal("sale date substituted non-charge time")
	}
}
