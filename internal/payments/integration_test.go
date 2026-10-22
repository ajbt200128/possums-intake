package payments

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"possums-intake/internal/ledger"
	"possums-intake/internal/stripepay"
)

func TestFractionalCreateLocalStatusAndConcurrentWebhooksFundOnce(t *testing.T) {
	f := newFixture(t, true)
	ref := f.start(t)
	p, err := f.core.GetPayment(context.Background(), testAccount, ref)
	if err != nil || int64(p.IssuedAt)%1000 != 789 || int64(p.CheckoutExpiresAt) != (int64(p.IssuedAt)+3600000)/1000*1000 {
		t.Fatal("fractional issuance mapping")
	}
	if f.service.CheckoutOrigin() != f.provider.URL || !f.service.CheckoutURLAllowed(f.provider.URL+"/checkout/synthetic") || f.service.CheckoutURLAllowed("https://checkout.stripe.com/synthetic") {
		t.Fatal("fixed origin forwarding")
	}
	f.paid() // provider completed, but status/return-like reads must not fund
	assertStatus(t, f.service, ref, "awaiting_payment")
	repeated, err := f.service.StartTopup(context.Background(), testAccount, f.form, "usd5")
	if err != nil || repeated.Reference != ref || repeated.URL != "" {
		t.Fatal("duplicate start replayed URL")
	}
	f.assertBalance(t, "0")
	if posts, gets := f.requests(); posts != 1 || gets != 0 {
		t.Fatal("local status initiated provider work")
	}
	const deliveries = 12
	var wg sync.WaitGroup
	results := make(chan string, deliveries)
	for i := 0; i < deliveries; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, reply := f.deliver(t, event(ref))
			if code != 200 {
				t.Error("eligible event failed")
			}
			results <- reply.Status
		}()
	}
	wg.Wait()
	close(results)
	funded := 0
	for status := range results {
		if status == "funded" {
			funded++
		} else if status != "already_funded" {
			t.Fatal("unexpected duplicate status")
		}
	}
	if funded != 1 {
		t.Fatal("not exactly one funding receipt")
	}
	f.assertBalance(t, "5000000")
	if f.count(t, "payment_sales") != 1 || f.count(t, "ledger_credits") != 0 {
		t.Fatal("non-atomic sale or generic credit marker")
	}
	assertStatus(t, f.service, ref, "funded")
	beforePosts, beforeGets := f.requests()
	f.restart(t)
	repeated, err = f.service.StartTopup(context.Background(), testAccount, f.form, "usd5")
	if err != nil || repeated.URL != "" || repeated.Status != "funded" {
		t.Fatal("restart replay")
	}
	if posts, gets := f.requests(); posts != beforePosts || gets != beforeGets {
		t.Fatal("restart local call dispatched")
	}
}

func TestLostCreateResponseRemainsUnknownAndSignedSelectorReconciles(t *testing.T) {
	f := newFixture(t, true)
	f.change(func(f *fixture) { f.createFail = true })
	result, err := f.service.StartTopup(context.Background(), testAccount, f.form, "usd5")
	requireErrorType(t, err)
	var provider *stripepay.Error
	if !errors.As(err, &provider) || provider.Outcome != stripepay.OutcomeCreateUnknown || result.Reference == "" || result.URL != "" {
		t.Fatal("unknown create outcome/reference lost")
	}
	p, err := f.core.GetPayment(context.Background(), testAccount, result.Reference)
	if err != nil || p.CreateState != "unknown" || p.SessionID != nil {
		t.Fatal("unknown claim not durable")
	}
	assertStatus(t, f.service, result.Reference, "unconfirmed")
	f.restart(t)
	repeat, err := f.service.StartTopup(context.Background(), testAccount, f.form, "usd5")
	if err != nil || repeat.Reference != result.Reference || repeat.URL != "" {
		t.Fatal("unknown create replay after restart")
	}
	if posts, gets := f.requests(); posts != 1 || gets != 0 {
		t.Fatal("unknown repeated start dispatched")
	}
	f.paid()
	code, reply := f.deliver(t, event(result.Reference))
	if code != 200 || reply.Status != "funded" {
		t.Fatal("canonical lost-response reconciliation")
	}
	f.assertBalance(t, "5000000")
}

func TestCanonicalMismatchNeverBindsOrFundsFromEvent(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*fixture)
	}{
		{"reference", func(f *fixture) { f.session["client_reference_id"] = strings.Repeat("b", 64) }},
		{"amount", func(f *fixture) { f.session["amount_total"] = 501 }},
		{"currency", func(f *fixture) { f.session["currency"] = "eur" }},
		{"intent", func(f *fixture) { f.charge["payment_intent"] = "pi_other" }},
		{"refund_negative", func(f *fixture) { f.charge["amount_refunded"] = -1 }},
		{"refund_unavailable", func(f *fixture) { f.charge["refunds"] = nil }},
		{"refund_empty_object", func(f *fixture) { f.charge["refunds"] = object{} }},
		{"refund_missing_data", func(f *fixture) { delete(f.charge["refunds"].(object), "data") }},
		{"refund_null_data", func(f *fixture) { f.charge["refunds"].(object)["data"] = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t, true)
			f.change(func(f *fixture) { f.createFail = true })
			result, err := f.service.StartTopup(context.Background(), testAccount, f.form, "usd5")
			requireErrorType(t, err)
			f.paid()
			f.change(test.change)
			code, reply := f.deliver(t, event(result.Reference))
			if code != 503 || reply.Error == nil {
				t.Fatal("canonical mismatch acknowledged/funded")
			}
			f.assertBalance(t, "0")
			p, err := f.core.GetPayment(context.Background(), testAccount, result.Reference)
			if err != nil || p.SessionID != nil || p.FundingState != "unfunded" {
				t.Fatal("invalid evidence bound or blocked purchase")
			}
		})
	}
}

func TestCanonicalRefundDisputeAndListedStatusesAreSticky(t *testing.T) {
	tests := []struct {
		name   string
		change func(*fixture)
	}{
		{"partial", func(f *fixture) { f.charge["amount_refunded"] = 1 }},
		{"full", func(f *fixture) { f.charge["refunded"] = true; f.charge["amount_refunded"] = 500 }},
		{"dispute", func(f *fixture) { f.charge["disputed"] = true }},
	}
	for _, status := range []string{"pending", "requires_action", "succeeded", "failed", "canceled", "future_unknown"} {
		tests = append(tests, struct {
			name   string
			change func(*fixture)
		}{status, func(f *fixture) {
			f.charge["refunds"].(object)["data"] = []any{object{"object": "refund", "id": "re_synthetic", "status": status, "amount": 1, "currency": "usd", "charge": testCharge, "payment_intent": testIntent}}
		}})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t, true)
			ref := f.start(t)
			f.paid()
			f.change(test.change)
			code, reply := f.deliver(t, event(ref))
			if code != 200 || reply.Status != "blocked" {
				t.Fatal("validated canonical block not persisted")
			}
			assertStatus(t, f.service, ref, "support_required")
			f.change(func(f *fixture) {
				f.charge["refunded"] = false
				f.charge["amount_refunded"] = 0
				f.charge["disputed"] = false
				f.charge["refunds"].(object)["data"] = []any{}
			})
			code, reply = f.deliver(t, event(ref))
			if code != 200 || reply.Status != "blocked" {
				t.Fatal("out-of-order success unblocked")
			}
			f.assertBalance(t, "0")
			if f.count(t, "payment_sales") != 0 {
				t.Fatal("blocked sale persisted")
			}
		})
	}
}

func TestRefundAfterFundingDoesNotClawBack(t *testing.T) {
	f := newFixture(t, true)
	ref := f.start(t)
	f.paid()
	code, reply := f.deliver(t, event(ref))
	if code != 200 || reply.Status != "funded" {
		t.Fatal("first funding")
	}
	f.change(func(f *fixture) { f.charge["disputed"] = true })
	code, reply = f.deliver(t, event(ref))
	if code != 200 || reply.Status != "already_funded" {
		t.Fatal("confirmed funding erased")
	}
	f.assertBalance(t, "5000000")
}

func TestUnknownExpiredAndPrunedReferencesInertWithoutProviderRead(t *testing.T) {
	f := newFixture(t, true)
	ref := f.start(t)
	f.paid()
	beforePosts, beforeGets := f.requests()
	code, reply := f.deliver(t, event(strings.Repeat("a", 64)))
	if code != 200 || reply.Status != "ineligible" {
		t.Fatal("unknown reference recreated")
	}
	assertStatus(t, f.service, strings.Repeat("a", 64), "unavailable_after_unlink")
	isolated, err := f.service.TopupStatus(context.Background(), "otheraccount000", ref)
	if err != nil || isolated.Status != "unavailable_after_unlink" {
		t.Fatal("cross-account status")
	}
	p, err := f.core.GetPayment(context.Background(), testAccount, ref)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, "UPDATE ledger_meta SET clock_high_water_ms=?", int64(p.UnlinkAt))
	code, reply = f.deliver(t, event(ref))
	if code != 200 || reply.Status != "ineligible" {
		t.Fatal("expired link admitted")
	}
	assertStatus(t, f.service, ref, "unavailable_after_unlink")
	purge, err := f.core.PurgeSyntheticPayments(context.Background())
	if err != nil || purge.More {
		t.Fatal("purge")
	}
	if f.count(t, "payment_purchases") != 0 {
		t.Fatal("purchase bridge survived prune")
	}
	code, reply = f.deliver(t, event(ref))
	if code != 200 || reply.Status != "ineligible" {
		t.Fatal("pruned link recreated")
	}
	if posts, gets := f.requests(); posts != beforePosts || gets != beforeGets {
		t.Fatal("ineligible mapping initiated provider read")
	}
	f.assertBalance(t, "0")
}

func TestAdmissionDoesNotInitializeAndDrainsOverduePurge(t *testing.T) {
	f := newFixture(t, false)
	service, err := NewOffline(context.Background(), f.core, f.client)
	var d *ledger.Diagnostic
	if service != nil || !errors.As(err, &d) || d.Code != ledger.CodePaymentDisabled {
		t.Fatal("implicit synthetic initialization")
	}
	if f.count(t, "sqlite_schema") == 0 {
		t.Fatal("state missing")
	}
	if err = f.core.InitializeSyntheticPayments(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	p, err := f.core.PreparePayment(context.Background(), ledger.PreparePaymentInput{AccountID: testAccount, Denomination: "usd5", Form: ledger.PaymentForm{Attempt: strings.Repeat("e", 64), IssuedAt: ledger.Millis(now), ExpiresAt: ledger.Millis(now + 600000)}})
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, "UPDATE ledger_meta SET clock_high_water_ms=?", int64(p.UnlinkAt))
	service, err = NewOffline(context.Background(), f.core, f.client)
	if err != nil || service == nil || f.count(t, "payment_purchases") != 0 {
		t.Fatal("admission preceded overdue purge")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if service, err = NewOffline(ctx, f.core, f.client); err == nil || service != nil {
		t.Fatal("canceled admission enabled")
	}
	if posts, gets := f.requests(); posts != 0 || gets != 0 {
		t.Fatal("admission initiated provider work")
	}
}
