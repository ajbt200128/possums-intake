package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"possums-intake/internal/ledger"
	"possums-intake/internal/stripepay"
)

func TestConcurrentStartHasOneDurableDispatch(t *testing.T) {
	f := newFixture(t, true)
	const attempts = 12
	var wg sync.WaitGroup
	results := make(chan TopupResult, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := f.service.StartTopup(context.Background(), testAccount, f.form, "usd5")
			if err != nil {
				t.Error("concurrent start failed")
			}
			results <- result
		}()
	}
	wg.Wait()
	close(results)
	urls := 0
	ref := ""
	for result := range results {
		if result.Reference == "" {
			t.Fatal("original reference missing")
		}
		if ref == "" {
			ref = result.Reference
		} else if ref != result.Reference {
			t.Fatal("duplicate original purchase")
		}
		if result.URL != "" {
			urls++
		}
	}
	if posts, gets := f.requests(); posts != 1 || gets != 0 || urls != 1 {
		t.Fatal("concurrent start replayed provider or URL")
	}
	f.assertBalance(t, "0")
}

func TestAttemptClaimBeforeCrashNeverDispatchesAfterRestart(t *testing.T) {
	f := newFixture(t, true)
	p, err := f.core.PreparePayment(context.Background(), ledger.PreparePaymentInput{AccountID: testAccount, Denomination: "usd5", Form: f.form})
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := f.core.ClaimPaymentCreate(context.Background(), testAccount, p.Reference); err != nil || !claimed {
		t.Fatal("durable claim")
	}
	f.restart(t)
	result, err := f.service.StartTopup(context.Background(), testAccount, f.form, "usd5")
	if err != nil || result.Reference != p.Reference || result.URL != "" || result.Status != "unconfirmed" {
		t.Fatal("unsent claim recreated after crash")
	}
	if posts, gets := f.requests(); posts != 0 || gets != 0 {
		t.Fatal("claimed crash attempt replayed")
	}
}

func TestCanceledCreatePreservesPrimaryUncertaintyAndClaim(t *testing.T) {
	f := newFixture(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Withhold the receipt so this tests cancellation, not a receipt/cancel race.
	releaseCreate := make(chan struct{})
	f.change(func(f *fixture) {
		f.onCreate = func() {
			cancel()
			<-releaseCreate
		}
	})
	result, err := func() (TopupResult, error) {
		defer close(releaseCreate)
		return f.service.StartTopup(ctx, testAccount, f.form, "usd5")
	}()
	var diagnostic *stripepay.Error
	if !errors.As(err, &diagnostic) || diagnostic.Outcome != stripepay.OutcomeCreateUnknown || result.Reference == "" || result.URL != "" {
		t.Fatal("canceled create invented rollback or lost primary")
	}
	p, err := f.core.GetPayment(context.Background(), testAccount, result.Reference)
	if err != nil || p.CreateState != "attempted" {
		t.Fatal("canceled mark invented rollback or erased claim")
	}
	repeat, err := f.service.StartTopup(context.Background(), testAccount, f.form, "usd5")
	if err != nil || repeat.URL != "" {
		t.Fatal("canceled create replay")
	}
	if posts, gets := f.requests(); posts != 1 || gets != 0 {
		t.Fatal("canceled create dispatched again")
	}
}

func TestSuccessfulCreateThenBindFailurePreservesProviderOutcome(t *testing.T) {
	f := newFixture(t, true)
	closure := make(chan error, 1)
	f.change(func(f *fixture) { f.onCreate = func() { closure <- f.core.Close() } })
	result, err := f.service.StartTopup(context.Background(), testAccount, f.form, "usd5")
	if closeErr := <-closure; closeErr != nil {
		t.Fatal(closeErr)
	}
	var boundary *Error
	var core *ledger.Diagnostic
	if !errors.As(err, &boundary) || !errors.As(err, &core) || core.Code != ledger.CodeClosed || boundary.Outcome != "checkout_created_local_binding_unconfirmed" || result.Reference == "" || result.URL != "" {
		t.Fatal("bind failure lost primary or inferred no provider effect")
	}
	f.change(func(f *fixture) { f.onCreate = nil })
	f.restart(t)
	f.paid()
	code, reply := f.deliver(t, event(result.Reference))
	if code != 200 || reply.Status != "funded" {
		t.Fatal("known create could not bind via canonical event")
	}
}

func TestRemoteCallsDoNotHoldAccountingWriterGate(t *testing.T) {
	f := newFixture(t, true)
	localWrite := make(chan error, 2)
	callback := func() { _, err := f.core.Balance(context.Background(), testAccount); localWrite <- err }
	f.change(func(f *fixture) { f.onCreate = callback; f.onCharge = callback })
	ref := f.start(t)
	if err := <-localWrite; err != nil {
		t.Fatal("provider create held writer gate")
	}
	f.paid()
	code, reply := f.deliver(t, event(ref))
	if err := <-localWrite; err != nil {
		t.Fatal("provider read held writer gate")
	}
	if code != 200 || reply.Status != "funded" {
		t.Fatal("canonical funding")
	}
}

func TestEligibilityRecheckedAfterProviderRead(t *testing.T) {
	f := newFixture(t, true)
	ref := f.start(t)
	f.paid()
	p, err := f.core.GetPayment(context.Background(), testAccount, ref)
	if err != nil {
		t.Fatal(err)
	}
	f.change(func(f *fixture) {
		f.onCharge = func() { f.exec(t, "UPDATE ledger_meta SET clock_high_water_ms=?", int64(p.UnlinkAt)) }
	})
	code, reply := f.deliver(t, event(ref))
	if code != 200 || reply.Status != "ineligible" {
		t.Fatal("provider read leased expired eligibility")
	}
	f.assertBalance(t, "0")
	if f.count(t, "payment_sales") != 0 {
		t.Fatal("late evidence funded")
	}
}

func TestAdmissionDrainsMultipleBoundedBatches(t *testing.T) {
	f := newFixture(t, true)
	var cutoff int64
	for i := 0; i <= ledger.CleanupLimit; i++ {
		now := time.Now().UnixMilli()
		p, err := f.core.PreparePayment(context.Background(), ledger.PreparePaymentInput{AccountID: testAccount, Denomination: "usd5", Form: ledger.PaymentForm{Attempt: fmt.Sprintf("%064x", i+1), IssuedAt: ledger.Millis(now), ExpiresAt: ledger.Millis(now + 600000)}})
		if err != nil {
			t.Fatal(err)
		}
		cutoff = max(cutoff, int64(p.UnlinkAt))
	}
	if f.count(t, "payment_purchases") != ledger.CleanupLimit+1 {
		t.Fatal("backlog fixture")
	}
	f.exec(t, "UPDATE ledger_meta SET clock_high_water_ms=?", cutoff)
	service, err := NewOffline(context.Background(), f.core, f.client)
	if err != nil || service == nil || f.count(t, "payment_purchases") != 0 {
		t.Fatal("admission left overdue second batch")
	}
}

func TestStaleConflictingAndInvalidFormsCannotDispatch(t *testing.T) {
	f := newFixture(t, true)
	expired := f.form
	expired.IssuedAt -= ledger.Millis(20 * time.Minute / time.Millisecond)
	expired.ExpiresAt -= ledger.Millis(20 * time.Minute / time.Millisecond)
	for _, input := range []struct {
		form         ledger.PaymentForm
		denomination string
	}{
		{expired, "usd5"}, {ledger.PaymentForm{Attempt: privateSentinel, IssuedAt: f.form.IssuedAt, ExpiresAt: f.form.ExpiresAt}, "usd5"}, {f.form, "usd0"},
	} {
		_, err := f.service.StartTopup(context.Background(), testAccount, input.form, input.denomination)
		requireErrorType(t, err)
	}
	if posts, gets := f.requests(); posts != 0 || gets != 0 {
		t.Fatal("invalid form dispatched")
	}
	ref := f.start(t)
	if _, err := f.service.StartTopup(context.Background(), testAccount, f.form, "usd10"); err == nil {
		t.Fatal("same attempt changed terms")
	}
	if _, err := f.service.TopupStatus(context.Background(), testAccount, strings.Repeat("!", 64)); err == nil {
		t.Fatal("known invalid-input error suppressed")
	}
	assertStatus(t, f.service, ref, "awaiting_payment")
}
