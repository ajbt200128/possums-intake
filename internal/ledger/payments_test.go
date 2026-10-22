package ledger

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"possums-intake/internal/failuretype"

	"github.com/pocketbase/dbx"
)

func paymentService(t *testing.T) (*Service, string, int64) {
	t.Helper()
	s, dir, now := testService(t)
	if err := s.InitializeSyntheticPayments(background); err != nil {
		t.Fatalf("payment init: %s", Failure(err).Code)
	}
	a, err := fixtureAccount(s, background, "paymentaccount0")
	must(t, a, err)
	return s, dir, now
}
func paymentForm(now int64, n int) PaymentForm {
	return PaymentForm{fmt.Sprintf("%064x", n), Millis(now), Millis(now + paymentFormMS)}
}
func preparePurchase(t *testing.T, s *Service, now int64, n int) PaymentPurchase {
	t.Helper()
	p, err := s.PreparePayment(background, PreparePaymentInput{"paymentaccount0", "usd5", paymentForm(now, n)})
	return must(t, p, err)
}
func claimPurchase(t *testing.T, s *Service, p PaymentPurchase) PaymentPurchase {
	t.Helper()
	p, claimed, err := s.ClaimPaymentCreate(background, p.AccountID, p.Reference)
	if err != nil || !claimed {
		t.Fatalf("claim: %s", Failure(err).Code)
	}
	return p
}
func fundingEvidence(p PaymentPurchase, n int) StripeFundingEvidence {
	return StripeFundingEvidence{
		Session:       StripeSessionEvidence{p.Reference, fmt.Sprintf("cs_test_%d", n), fmt.Sprintf("pi_test_%d", n), int64(p.IssuedAt) / 1000, int64(p.CheckoutExpiresAt) / 1000, false, "payment", "usd", p.GrossMinor, 1},
		PaymentStatus: "paid", IntentStatus: "succeeded", IntentAmount: p.GrossMinor, AmountReceived: p.GrossMinor,
		ChargeStatus: "succeeded", ChargeCreatedSeconds: int64(p.IssuedAt) / 1000, ChargeCurrency: "usd", ChargeAmount: p.GrossMinor, AmountCaptured: p.GrossMinor, Paid: true, Captured: true, AmountRefunded: "0",
	}
}
func paymentCounts(t *testing.T, s *Service) (purchases, sales, credits int) {
	t.Helper()
	err := s.run(background, func(tx *transaction) error {
		if err := tx.query("SELECT count(*) FROM payment_purchases", nil).Row(&purchases); err != nil {
			return err
		}
		if err := tx.query("SELECT count(*) FROM payment_sales", nil).Row(&sales); err != nil {
			return err
		}
		return tx.query("SELECT count(*) FROM ledger_credits", nil).Row(&credits)
	}, false)
	if err != nil {
		t.Fatalf("counts: %s", Failure(err).Code)
	}
	return
}

func TestSyntheticPaymentFixedDenominations(t *testing.T) {
	for choice, want := range map[string]PaymentTerms{
		"usd5": {"usd", "500", "5000000"}, "usd10": {"usd", "1000", "10000000"},
		"usd25": {"usd", "2500", "25000000"}, "usd50": {"usd", "5000", "50000000"},
	} {
		got, ok := PaymentDenomination(choice)
		if !ok || got != want {
			t.Fatal("fixed denomination mismatch")
		}
		got.CreditUnits = "1"
		again, _ := PaymentDenomination(choice)
		if again != want {
			t.Fatal("caller mutated denomination table")
		}
	}
	for _, choice := range []string{"", "usd1", "usd500", "USD5", "500"} {
		if _, ok := PaymentDenomination(choice); ok {
			t.Fatal("arbitrary denomination admitted")
		}
	}
}

func TestSyntheticPaymentsExplicitAdditiveInitialization(t *testing.T) {
	s, dir, now := testService(t)
	fund(t, s, "paymentaccount0", "100", now)
	hold := reserve(t, s, ReserveInput{"paymentaccount0", "40", 600, key(now, 2)})
	before := balance(t, s, "paymentaccount0")
	var highBefore int64
	if err := s.app.DB().NewQuery("SELECT clock_high_water_ms FROM ledger_meta").Row(&highBefore); err != nil {
		t.Fatal("watermark fixture")
	}
	// Init/Open never implicitly create optional payment state.
	var count int
	if err := s.app.DB().NewQuery("SELECT count(*) FROM sqlite_schema WHERE name GLOB 'payment_*'").Row(&count); err != nil || count != 0 {
		t.Fatal("implicit schema")
	}
	_, err := s.PreparePayment(background, PreparePaymentInput{"paymentaccount0", "usd5", paymentForm(now, 1)})
	codeIs(t, err, CodePaymentDisabled)
	if err := s.InitializeSyntheticPayments(background); err != nil {
		t.Fatal("explicit init")
	}
	if err := s.InitializeSyntheticPayments(background); err != nil {
		t.Fatal("validated repeat")
	}
	var highAfter int64
	if err := s.app.DB().NewQuery("SELECT clock_high_water_ms FROM ledger_meta").Row(&highAfter); err != nil || highAfter != highBefore {
		t.Fatal("init changed watermark")
	}
	if err := validateDDL(func(q string, p dbx.Params) *dbx.Query { return s.app.DB().NewQuery(q).Bind(p) }); err != nil {
		t.Fatal("legacy exact-nine DDL changed")
	}
	if after := balance(t, s, "paymentaccount0"); !reflect.DeepEqual(after, before) {
		t.Fatal("init changed balance/hold")
	}
	credit, err := s.Credit(background, CreditInput{"paymentaccount0", "100", key(now, 1)})
	if must(t, credit, err).BalanceAfter != "100" {
		t.Fatal("init changed keys")
	}
	if err := s.Close(); err != nil {
		t.Fatal("close")
	}
	reopened, err := Open(background, dir)
	must(t, reopened, err)
	defer reopened.Close()
	if got := balance(t, reopened, "paymentaccount0"); len(got.PendingReservations) != 1 || got.PendingReservations[0].ReservationID != hold.ReservationID {
		t.Fatal("restart changed holds")
	}
	// Separate never-enabled legacy state remains absent even after reopen/cleanup.
	legacy, legacyDir, _ := testService(t)
	legacy.Close()
	old, err := Open(background, legacyDir)
	must(t, old, err)
	defer old.Close()
	_, err = old.Cleanup(background, true)
	if err != nil {
		t.Fatal("legacy maintenance")
	}
	if err := old.app.DB().NewQuery("SELECT count(*) FROM sqlite_schema WHERE name GLOB 'payment_*'").Row(&count); err != nil || count != 0 {
		t.Fatal("open/cleanup initialized payments")
	}
}

func TestSyntheticPaymentFormsClaimsAndAccountIsolation(t *testing.T) {
	s, _, now := paymentService(t)
	p := preparePurchase(t, s, now, 1)
	if len(p.Reference) != 64 || len(p.CreateKey) != 64 || p.Reference == p.CreateKey || p.Reference == p.FormAttempt {
		t.Fatal("independent random identities")
	}
	repeat := preparePurchase(t, s, now, 1)
	if !reflect.DeepEqual(p, repeat) {
		t.Fatal("duplicate form allocated another reference")
	}
	for _, input := range []PreparePaymentInput{
		{"paymentaccount0", "usd10", paymentForm(now, 1)},
		{"otheraccount000", "usd5", paymentForm(now, 1)},
		{"paymentaccount0", "usd5", PaymentForm{p.FormAttempt, Millis(now), Millis(now + paymentFormMS - 1)}},
	} {
		_, err := s.PreparePayment(background, input)
		codeIs(t, err, CodePaymentConflict)
	}
	_, err := s.GetPayment(background, "otheraccount000", p.Reference)
	codeIs(t, err, CodePaymentMissing)
	_, _, err = s.ClaimPaymentCreate(background, "otheraccount000", p.Reference)
	codeIs(t, err, CodePaymentMissing)
	var wg sync.WaitGroup
	outcomes := make(chan bool, 16)
	failures := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, claimed, err := s.ClaimPaymentCreate(background, p.AccountID, p.Reference)
			outcomes <- claimed
			failures <- err
		}()
	}
	wg.Wait()
	close(outcomes)
	close(failures)
	claims := 0
	for got := range outcomes {
		if got {
			claims++
		}
	}
	for err := range failures {
		if err != nil {
			t.Fatal("claim concurrency")
		}
	}
	if claims != 1 {
		t.Fatal("multiple dispatch grants")
	}
	if err := s.MarkPaymentCreateUnknown(background, p.AccountID, p.Reference); err != nil {
		t.Fatal("unknown create")
	}
	_, claimed, err := s.ClaimPaymentCreate(background, p.AccountID, p.Reference)
	if err != nil || claimed {
		t.Fatal("unknown create replayed")
	}
	e := fundingEvidence(p, 1)
	bound, err := s.BindPaymentSession(background, e.Session)
	must(t, bound, err)
	if bound.CreateState != "known" || bound.SessionCreatedAt == nil {
		t.Fatal("canonical unknown binding")
	}
	if err := s.MarkPaymentCreateUnknown(background, p.AccountID, p.Reference); err != nil {
		t.Fatal("known create overwritten")
	}
	bound, err = s.GetPayment(background, p.AccountID, p.Reference)
	if must(t, bound, err).CreateState != "known" {
		t.Fatal("lost known binding")
	}
	advance(t, s, now+paymentFormMS)
	_, err = s.PreparePayment(background, PreparePaymentInput{p.AccountID, "usd5", paymentForm(now, 1)})
	codeIs(t, err, CodePaymentExpired)
	// A missing ancient form cannot reallocate either.
	_, err = s.PreparePayment(background, PreparePaymentInput{p.AccountID, "usd5", paymentForm(now, 2)})
	codeIs(t, err, CodePaymentExpired)
	if purchases, _, _ := paymentCounts(t, s); purchases != 1 {
		t.Fatal("stale form allocated")
	}
}

func TestSyntheticPaymentUnknownCreateSurvivesRestartWithoutDispatch(t *testing.T) {
	s, dir, now := paymentService(t)
	p := claimPurchase(t, s, preparePurchase(t, s, now, 1))
	if err := s.MarkPaymentCreateUnknown(background, p.AccountID, p.Reference); err != nil {
		t.Fatal("unknown create")
	}
	s.Close()
	reopened, err := Open(background, dir)
	must(t, reopened, err)
	defer reopened.Close()
	got, claimed, err := reopened.ClaimPaymentCreate(background, p.AccountID, p.Reference)
	if err != nil || claimed || got.CreateState != "unknown" || got.CreateKey != p.CreateKey {
		t.Fatal("restart recreated uncertain dispatch")
	}
	result, err := reopened.FundStripePurchase(background, fundingEvidence(p, 1))
	if must(t, result, err).Status != PaymentFunded {
		t.Fatal("matching canonical evidence did not resolve original attempt")
	}
}

func TestSyntheticPaymentFundingConcurrentOnceAndNoGenericMarker(t *testing.T) {
	s, dir, now := paymentService(t)
	p := claimPurchase(t, s, preparePurchase(t, s, now, 1))
	e := fundingEvidence(p, 1)
	// Disabled admission is not grounds to discard valid purchased credit.
	if err := s.run(background, func(tx *transaction) error {
		return tx.exec("UPDATE accounts SET enabled=0 WHERE id={:id}", dbx.Params{"id": p.AccountID})
	}, false); err != nil {
		t.Fatal("disable fixture")
	}
	var wg sync.WaitGroup
	results := make(chan PaymentFundingStatus, 24)
	failures := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.FundStripePurchase(background, e)
			results <- r.Status
			failures <- err
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	funded, duplicates := 0, 0
	for status := range results {
		switch status {
		case PaymentFunded:
			funded++
		case PaymentDuplicate:
			duplicates++
		default:
			t.Fatal("unexpected status")
		}
	}
	for err := range failures {
		if err != nil {
			t.Fatalf("funding: %s", Failure(err).Code)
		}
	}
	if funded != 1 || duplicates != 23 || balance(t, s, p.AccountID).AccountBalance.Balance != "5000000" {
		t.Fatal("funding not once-only")
	}
	if purchases, sales, credits := paymentCounts(t, s); purchases != 1 || sales != 1 || credits != 0 {
		t.Fatal("unexpected payment records")
	}
	// A refund after known funding is an original receipt, not clawback.
	e.RefundObserved = true
	e.Disputed = true
	e.AmountRefunded = p.GrossMinor
	r, err := s.FundStripePurchase(background, e)
	if must(t, r, err).Status != PaymentDuplicate || balance(t, s, p.AccountID).AccountBalance.Balance != "5000000" {
		t.Fatal("post-funding adjustment")
	}
	// The duplicate receipt must not be confused with the current balance.
	if err := s.run(background, func(tx *transaction) error {
		return tx.exec("UPDATE accounts SET balance='1' WHERE id={:id}", dbx.Params{"id": p.AccountID})
	}, false); err != nil {
		t.Fatal("spend fixture")
	}
	s.Close()
	reopened, err := Open(background, dir)
	must(t, reopened, err)
	defer reopened.Close()
	r, err = reopened.FundStripePurchase(background, e)
	if must(t, r, err).Status != PaymentDuplicate || balance(t, reopened, p.AccountID).Available != "1" {
		t.Fatal("restart duplicate receipt fabricated availability")
	}
}

func TestSyntheticPaymentCanonicalEvidenceAndStickyBlock(t *testing.T) {
	mutations := map[string]func(*StripeFundingEvidence){
		"live":                func(e *StripeFundingEvidence) { e.Session.LiveMode = true },
		"mode":                func(e *StripeFundingEvidence) { e.Session.Mode = "subscription" },
		"currency":            func(e *StripeFundingEvidence) { e.Session.Currency = "eur" },
		"amount":              func(e *StripeFundingEvidence) { e.Session.GrossMinor = "501" },
		"quantity":            func(e *StripeFundingEvidence) { e.Session.Quantity = 2 },
		"unpaid":              func(e *StripeFundingEvidence) { e.PaymentStatus = "unpaid" },
		"no_payment_required": func(e *StripeFundingEvidence) { e.PaymentStatus = "no_payment_required" },
		"intent":              func(e *StripeFundingEvidence) { e.IntentStatus = "processing" },
		"intent_amount":       func(e *StripeFundingEvidence) { e.IntentAmount = "499" },
		"received":            func(e *StripeFundingEvidence) { e.AmountReceived = "499" },
		"charge":              func(e *StripeFundingEvidence) { e.ChargeStatus = "pending" },
		"charge_currency":     func(e *StripeFundingEvidence) { e.ChargeCurrency = "eur" },
		"charge_amount":       func(e *StripeFundingEvidence) { e.ChargeAmount = "499" },
		"paid":                func(e *StripeFundingEvidence) { e.Paid = false },
		"capture":             func(e *StripeFundingEvidence) { e.Captured = false },
		"capture_amount":      func(e *StripeFundingEvidence) { e.AmountCaptured = "499" },
		"missing_intent":      func(e *StripeFundingEvidence) { e.Session.PaymentIntentID = "" },
		"old_session":         func(e *StripeFundingEvidence) { e.Session.CreatedSeconds -= 61 },
		"late_session":        func(e *StripeFundingEvidence) { e.Session.CreatedSeconds += 361 },
		"expiry":              func(e *StripeFundingEvidence) { e.Session.ExpiresSeconds++ },
		"old_charge":          func(e *StripeFundingEvidence) { e.ChargeCreatedSeconds-- },
		"future_charge":       func(e *StripeFundingEvidence) { e.ChargeCreatedSeconds += 61 },
		"overflow_time":       func(e *StripeFundingEvidence) { e.Session.CreatedSeconds = 1 << 62 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			s, _, now := paymentService(t)
			p := claimPurchase(t, s, preparePurchase(t, s, now, 1))
			e := fundingEvidence(p, 1)
			mutate(&e)
			result, err := s.FundStripePurchase(background, e)
			codeIs(t, err, CodePaymentConflict)
			if result != (PaymentFundingResult{}) || balance(t, s, p.AccountID).Available != "0" {
				t.Fatal("invalid evidence funded")
			}
			stored, err := s.GetPayment(background, p.AccountID, p.Reference)
			must(t, stored, err)
			if stored.SessionID != nil || stored.FundingState != "unfunded" {
				t.Fatal("invalid evidence bound/marked")
			}
			if _, sales, credits := paymentCounts(t, s); sales != 0 || credits != 0 {
				t.Fatal("invalid evidence persisted receipt")
			}
		})
	}
	for _, reason := range []string{"refund_seen", "refunded", "partial", "dispute"} {
		t.Run(reason, func(t *testing.T) {
			s, _, now := paymentService(t)
			p := claimPurchase(t, s, preparePurchase(t, s, now, 1))
			e := fundingEvidence(p, 1)
			switch reason {
			case "refund_seen":
				e.RefundObserved = true
			case "refunded":
				e.Refunded = true
			case "partial":
				e.AmountRefunded = "1"
			case "dispute":
				e.Disputed = true
			}
			result, err := s.FundStripePurchase(background, e)
			if must(t, result, err).Status != PaymentBlocked {
				t.Fatal("not blocked")
			}
			clean := fundingEvidence(p, 1)
			result, err = s.FundStripePurchase(background, clean)
			if must(t, result, err).Status != PaymentBlocked || balance(t, s, p.AccountID).Available != "0" {
				t.Fatal("success unblocked prior evidence")
			}
		})
	}
}

func TestSyntheticPaymentNoRebindingOrMissingProvision(t *testing.T) {
	s, _, now := paymentService(t)
	p := claimPurchase(t, s, preparePurchase(t, s, now, 1))
	q := claimPurchase(t, s, preparePurchase(t, s, now, 2))
	e := fundingEvidence(p, 1)
	r, err := s.FundStripePurchase(background, e)
	must(t, r, err)
	for _, same := range []string{"session", "intent"} {
		other := fundingEvidence(q, 2)
		if same == "session" {
			other.Session.SessionID = e.Session.SessionID
		} else {
			other.Session.PaymentIntentID = e.Session.PaymentIntentID
		}
		_, err := s.FundStripePurchase(background, other)
		codeIs(t, err, CodePaymentConflict)
	}
	changed := e
	changed.Session.SessionID = "cs_different"
	_, err = s.FundStripePurchase(background, changed)
	codeIs(t, err, CodePaymentConflict)
	changed = e
	changed.Session.CreatedSeconds--
	_, err = s.FundStripePurchase(background, changed)
	codeIs(t, err, CodePaymentConflict)
	missing := e
	missing.Session.Reference = strings.Repeat("f", 64)
	_, err = s.FundStripePurchase(background, missing)
	codeIs(t, err, CodePaymentMissing)
	_, err = s.PreparePayment(background, PreparePaymentInput{"nevercreated000", "usd5", paymentForm(now, 3)})
	codeIs(t, err, CodeAccount)
	if purchases, sales, _ := paymentCounts(t, s); purchases != 2 || sales != 1 {
		t.Fatal("identity conflict changed receipts")
	}
}

func TestSyntheticPaymentExpiryPruneReplayAndSalesCutoff(t *testing.T) {
	s, dir, now := paymentService(t)
	p := claimPurchase(t, s, preparePurchase(t, s, now, 1))
	e := fundingEvidence(p, 1)
	r, err := s.FundStripePurchase(background, e)
	must(t, r, err)
	advance(t, s, int64(p.UnlinkAt)-1)
	if _, err := s.GetPayment(background, p.AccountID, p.Reference); err != nil {
		t.Fatal("early unlink")
	}
	advance(t, s, int64(p.UnlinkAt))
	_, err = s.GetPayment(background, p.AccountID, p.Reference)
	codeIs(t, err, CodePaymentExpired)
	_, err = s.FundStripePurchase(background, e)
	codeIs(t, err, CodePaymentExpired)
	clean, err := s.Cleanup(background, true)
	if must(t, clean, err).Pruned != 1 {
		t.Fatal("exact-cutoff purchase prune")
	}
	if purchases, sales, credits := paymentCounts(t, s); purchases != 0 || sales != 1 || credits != 0 {
		t.Fatal("mapping survived or sales pruned early")
	}
	_, err = s.FundStripePurchase(background, e)
	codeIs(t, err, CodePaymentMissing)
	// Replaying an old form cannot recreate its now-missing reference.
	_, err = s.PreparePayment(background, PreparePaymentInput{p.AccountID, "usd5", paymentForm(now, 1)})
	codeIs(t, err, CodePaymentExpired)
	freshNow := int64(p.UnlinkAt)
	fresh := claimPurchase(t, s, preparePurchase(t, s, freshNow, 2))
	ancient := e
	ancient.Session.Reference = fresh.Reference
	_, err = s.FundStripePurchase(background, ancient)
	codeIs(t, err, CodePaymentConflict)
	// Sales use UTC date cutoff, not payment receipt time or issuance time.
	chargeMS := e.ChargeCreatedSeconds * 1000
	saleDay := time.Date(time.UnixMilli(chargeMS).UTC().Year(), time.UnixMilli(chargeMS).UTC().Month(), time.UnixMilli(chargeMS).UTC().Day(), 0, 0, 0, 0, time.UTC)
	cutoff := saleDay.AddDate(0, 0, paymentSalesDays).UnixMilli()
	advance(t, s, cutoff-1)
	purged, err := s.PurgeSyntheticPayments(background)
	must(t, purged, err)
	if _, sales, _ := paymentCounts(t, s); sales != 1 {
		t.Fatal("early sales prune")
	}
	advance(t, s, cutoff)
	purged, err = s.PurgeSyntheticPayments(background)
	must(t, purged, err)
	if purchases, sales, _ := paymentCounts(t, s); purchases != 0 || sales != 0 {
		t.Fatal("date cutoff not pruned")
	}
	// Wall time remains far behind committed high water after a restart.
	s.Close()
	reopened, err := Open(background, dir)
	must(t, reopened, err)
	defer reopened.Close()
	_, err = reopened.FundStripePurchase(background, e)
	codeIs(t, err, CodePaymentMissing)
	_, err = reopened.PreparePayment(background, PreparePaymentInput{p.AccountID, "usd5", paymentForm(now, 1)})
	codeIs(t, err, CodePaymentExpired)
	if balance(t, reopened, p.AccountID).Available != "5000000" {
		t.Fatal("pruning changed balance")
	}
	var high int64
	if err := reopened.app.DB().NewQuery("SELECT clock_high_water_ms FROM ledger_meta").Row(&high); err != nil || high < cutoff {
		t.Fatal("backwards wall clock reopened time")
	}
}

func TestSyntheticPaymentWindowsAndProviderAge(t *testing.T) {
	s, _, now := paymentService(t)
	p := preparePurchase(t, s, now, 1)
	advance(t, s, int64(p.IssuedAt)+paymentCreateMS)
	_, _, err := s.ClaimPaymentCreate(background, p.AccountID, p.Reference)
	codeIs(t, err, CodePaymentExpired)
	// A canonical Session just before local issuance can reach its own 20-day
	// horizon earlier than unlink_at; a fresh notification cannot extend it.
	p = claimPurchase(t, s, preparePurchase(t, s, now+paymentCreateMS, 2))
	e := fundingEvidence(p, 2)
	e.Session.CreatedSeconds -= 59
	e.ChargeCreatedSeconds = e.Session.CreatedSeconds
	advance(t, s, e.Session.CreatedSeconds*1000+paymentLinkMS)
	_, err = s.FundStripePurchase(background, e)
	codeIs(t, err, CodePaymentExpired)
	if _, sales, _ := paymentCounts(t, s); sales != 0 {
		t.Fatal("old canonical object funded")
	}
}

func TestSyntheticPaymentRollbackOverflowHoldAndMissingAccount(t *testing.T) {
	for _, fault := range []string{"overflow", "holds", "missing"} {
		t.Run(fault, func(t *testing.T) {
			s, dir, now := paymentService(t)
			if fault == "overflow" {
				fund(t, s, "paymentaccount0", "18446744073709551615", now)
			}
			if fault == "holds" {
				fund(t, s, "paymentaccount0", "10", now)
				reserve(t, s, ReserveInput{"paymentaccount0", "10", 1, key(now, 2)})
			}
			p := claimPurchase(t, s, preparePurchase(t, s, now, 1))
			e := fundingEvidence(p, 1)
			if fault == "holds" {
				if err := s.run(background, func(tx *transaction) error {
					return tx.exec("UPDATE accounts SET balance='9' WHERE id={:id}", dbx.Params{"id": p.AccountID})
				}, false); err != nil {
					t.Fatal("corrupt hold fixture")
				}
				advance(t, s, now+1000)
			}
			if fault == "missing" {
				// A separate raw fixture connection models trusted corruption with FK off,
				// not a production capability or a second ledger authority.
				db, err := connect(filepath.Join(dir, "data.db"), false)
				if err != nil {
					t.Fatal("fixture connect")
				}
				_, err = db.NewQuery("PRAGMA foreign_keys=OFF; DELETE FROM accounts WHERE id='paymentaccount0'").Execute()
				db.Close()
				if err != nil {
					t.Fatal("missing fixture")
				}
			}
			result, err := s.FundStripePurchase(background, e)
			want := CodeOverflow
			if fault == "holds" {
				want = CodeCorrupt
			}
			if fault == "missing" {
				want = CodeAccount
			}
			codeIs(t, err, want)
			if result != (PaymentFundingResult{}) {
				t.Fatal("fault returned receipt")
			}
			var state, create string
			var sales int
			if err := s.app.DB().NewQuery("SELECT funding_state,create_state FROM payment_purchases").Row(&state, &create); err != nil || state != "unfunded" || create != "attempted" {
				t.Fatal("partial fault binding")
			}
			if err := s.app.DB().NewQuery("SELECT count(*) FROM payment_sales").Row(&sales); err != nil || sales != 0 {
				t.Fatal("fault sale survived")
			}
			if fault == "holds" {
				var state string
				if err := s.app.DB().NewQuery("SELECT state FROM ledger_reservations").Row(&state); err != nil || state != "active" {
					t.Fatal("hold expiry not rolled back")
				}
			}
		})
	}
}

func TestSyntheticPaymentSchemaRowsAndHiddenKeys(t *testing.T) {
	s, _, now := paymentService(t)
	p := claimPurchase(t, s, preparePurchase(t, s, now, 1))
	r, err := s.FundStripePurchase(background, fundingEvidence(p, 1))
	must(t, r, err)
	for _, table := range []string{"payment_meta", "payment_purchases", "payment_sales"} {
		for _, key := range []string{"rowid", "_rowid_", "oid"} {
			var hidden int
			if err := s.app.DB().NewQuery("SELECT " + key + " FROM " + table).Row(&hidden); err == nil {
				t.Fatal("hidden sequential key available")
			}
		}
	}
	var cols []struct {
		Name string `db:"name"`
	}
	if err := s.app.DB().NewQuery("SELECT name FROM pragma_table_info('payment_sales') ORDER BY cid").All(&cols); err != nil {
		t.Fatal("column inventory")
	}
	names := []string{}
	for _, col := range cols {
		names = append(names, col.Name)
	}
	if !reflect.DeepEqual(names, []string{"sale_id", "account_id", "sale_date", "currency", "gross_minor", "credit_units"}) {
		t.Fatal("sales retained unapproved field")
	}
	// Ordinary TEXT primary keys retain a paired-insert rowid bridge. Keep the
	// temporary fixture on the SAME transaction connection, not a pooled read.
	var joins int
	if err := s.run(background, func(tx *transaction) error {
		if err := tx.exec("CREATE TEMP TABLE synthetic_sale (id TEXT PRIMARY KEY); CREATE TEMP TABLE synthetic_provider (id TEXT PRIMARY KEY); INSERT INTO synthetic_sale VALUES('sale'); INSERT INTO synthetic_provider VALUES('provider')", nil); err != nil {
			return err
		}
		if err := tx.query("SELECT count(*) FROM synthetic_sale s JOIN synthetic_provider p ON s.rowid=p.rowid", nil).Row(&joins); err != nil {
			return err
		}
		return tx.exec("DROP TABLE synthetic_sale; DROP TABLE synthetic_provider", nil)
	}, false); err != nil || joins != 1 {
		t.Fatal("paired rowid positive control")
	}
}

func TestSyntheticPaymentRejectPartialDriftAndCorruptRows(t *testing.T) {
	for _, mutation := range []string{
		"CREATE TABLE payment_partial (value TEXT)",
		"DROP TABLE payment_purchases",
		"DROP INDEX payment_sales_prune",
		"ALTER TABLE payment_sales ADD COLUMN private_extra TEXT",
		"UPDATE payment_meta SET identity='live'",
		"UPDATE payment_meta SET singleton=2",
		"UPDATE payment_purchases SET funding_state='blocked00000000',sale_id=NULL,create_state='prepared',stripe_session_id=NULL,stripe_payment_intent_id=NULL,session_created_ms=NULL",
		"UPDATE payment_sales SET sale_date='2026-02-30'",
		"UPDATE payment_sales SET account_id='different-account'",
		"DELETE FROM payment_sales",
	} {
		t.Run(mutation, func(t *testing.T) {
			s, dir, now := paymentService(t)
			p := claimPurchase(t, s, preparePurchase(t, s, now, 1))
			r, err := s.FundStripePurchase(background, fundingEvidence(p, 1))
			must(t, r, err)
			// ignore_check/FK bypass models trusted SQL corruption only.
			db, err := connect(filepath.Join(dir, "data.db"), false)
			if err != nil {
				t.Fatal("fixture connect")
			}
			_, err = db.NewQuery("PRAGMA foreign_keys=OFF; PRAGMA ignore_check_constraints=ON; " + mutation).Execute()
			db.Close()
			if err != nil {
				t.Fatal("mutation fixture")
			}
			err = s.InitializeSyntheticPayments(background)
			if err == nil {
				t.Fatal("repeat init repaired malformed state")
			}
			s.Close()
			_, err = Open(background, dir)
			if err == nil {
				t.Fatal("malformed payment state accepted")
			}
			d, ok := err.(*Diagnostic)
			if !ok || (d.Code != CodeSchema && d.Code != CodeCorrupt) {
				t.Fatal("unsafe/unclassified validation failure")
			}
		})
	}
	// Partial optional state on a legacy ledger is refused, never completed.
	legacy, dir, _ := testService(t)
	if err := legacy.run(background, func(tx *transaction) error { return tx.exec("CREATE TABLE payment_meta (singleton INTEGER)", nil) }, false); err != nil {
		t.Fatal("partial fixture")
	}
	codeIs(t, legacy.InitializeSyntheticPayments(background), CodeSchema)
	legacy.Close()
	_, err := Open(background, dir)
	codeIs(t, err, CodeSchema)
}

func TestSyntheticPaymentBoundedPurgeAndCombinedBudget(t *testing.T) {
	s, _, now := paymentService(t)
	for i := 1; i <= CleanupLimit+1; i++ {
		preparePurchase(t, s, now, i)
	}
	advance(t, s, now+paymentLinkMS)
	result, err := s.PurgeSyntheticPayments(background)
	if must(t, result, err).Pruned != CleanupLimit || !result.More {
		t.Fatal("unbounded/missing purge backlog")
	}
	result, err = s.PurgeSyntheticPayments(background)
	if must(t, result, err).Pruned != 1 || result.More {
		t.Fatal("purge completion")
	}
	// Existing terminal/credit rows and payment deletions share the same budget.
	now += paymentLinkMS
	for i := 1; i <= CleanupLimit; i++ {
		c, err := s.Credit(background, CreditInput{"paymentaccount0", "1", key(now, i+1000)})
		must(t, c, err)
	}
	preparePurchase(t, s, now, 500)
	advance(t, s, now+paymentLinkMS)
	cleanup, err := s.Cleanup(background, true)
	if must(t, cleanup, err).Pruned != CleanupLimit {
		t.Fatal("combined budget exceeded")
	}
	if purchases, _, credits := paymentCounts(t, s); purchases != 1 || credits != 0 {
		t.Fatal("payment deletion did not reuse residual budget")
	}
	cleanup, err = s.Cleanup(background, true)
	if must(t, cleanup, err).Pruned != 1 {
		t.Fatal("cleanup did not drain")
	}
}

func TestSyntheticPaymentFailureProjectionAndUnknownCompletion(t *testing.T) {
	for code, want := range map[Code]failuretype.Type{CodePaymentDisabled: failuretype.PolicyRefused, CodePaymentMissing: failuretype.EvidenceUnavailable, CodePaymentExpired: failuretype.PolicyRefused, CodePaymentConflict: failuretype.PolicyRefused, CodePaymentRandom: failuretype.IdentityUnavailable} {
		d := CanonicalFailure(diagnostic(code), failuretype.TransactionWorkUnknown)
		if d.Code != code || d.Kind() != want || d.Stage == "" || d.Constraint == "" {
			t.Fatal("payment diagnostic lost honest existing category")
		}
	}
	s, _, now := paymentService(t)
	p := claimPurchase(t, s, preparePurchase(t, s, now, 1))
	e := fundingEvidence(p, 1)
	ctx, report := WithFailureReport(background)
	bad := e
	bad.Session.LiveMode = true
	_, err := s.FundStripePurchase(ctx, bad)
	codeIs(t, err, CodePaymentConflict)
	kinds, count, valid, primary := report.TakeWithPrimary()
	if !valid || !primary || count != 1 || kinds[0] != failuretype.PolicyRefused {
		t.Fatal("payment refusal projected as storage fault or recounted")
	}
	original := s.app
	s.app = failedBegin{original, errors.New("SYNTHETIC_PRIVATE_PROVIDER_URL_SECRET")}
	_, err = s.FundStripePurchase(background, e)
	codeIs(t, err, CodeBegin)
	if strings.Contains(Failure(err).NextStep, "SYNTHETIC_PRIVATE") {
		t.Fatal("raw cause")
	}
	s.app = lostCompletionApp{original}
	result, err := s.FundStripePurchase(context.Background(), e)
	codeIs(t, err, CodeCommit)
	if result != (PaymentFundingResult{}) {
		t.Fatal("unknown completion supplied receipt")
	}
	s.app = original
	// The fixture discarded completion evidence AFTER a real commit. Original
	// identities resolve it without a second credit; no new create/credit key.
	result, err = s.FundStripePurchase(background, e)
	if must(t, result, err).Status != PaymentDuplicate || balance(t, s, p.AccountID).Available != "5000000" {
		t.Fatal("unknown completion replayed funding")
	}
	ctx, report = WithFailureReport(background)
	s.app = afterCommitFailure{original}
	result, err = s.FundStripePurchase(ctx, e)
	s.app = original
	if must(t, result, err).Status != PaymentDuplicate {
		t.Fatal("known receipt erased by after-completion fault")
	}
	kinds, count, valid, primary = report.TakeWithPrimary()
	if !valid || primary || count != 1 || kinds[0] != failuretype.TransactionAfterCompletionUnknown {
		t.Fatal("after-completion failure ownership changed")
	}
}

func TestSyntheticPaymentRacesSettlementExpiryAndPrune(t *testing.T) {
	s, _, now := paymentService(t)
	fund(t, s, "paymentaccount0", "100", now)
	hold := reserve(t, s, ReserveInput{"paymentaccount0", "50", 1, key(now, 2)})
	p := claimPurchase(t, s, preparePurchase(t, s, now, 1))
	e := fundingEvidence(p, 1)
	advance(t, s, now+1000)
	var wg sync.WaitGroup
	failures := make(chan error, 3)
	wg.Add(3)
	go func() { defer wg.Done(); _, err := s.FundStripePurchase(background, e); failures <- err }()
	go func() {
		defer wg.Done()
		_, err := s.Settle(background, SettleInput{hold.ReservationID, "50"})
		if d, ok := err.(*Diagnostic); ok && d.Code == CodeExpired {
			err = nil
		}
		failures <- err
	}()
	go func() { defer wg.Done(); _, err := s.Cleanup(background, true); failures <- err }()
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatalf("accounting race: %s", Failure(err).Code)
		}
	}
	if b := balance(t, s, p.AccountID); b.Available != "5000100" || len(b.PendingReservations) != 0 {
		t.Fatal("payment raced accounting outside serialized owner")
	}
	advance(t, s, int64(p.UnlinkAt))
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := s.FundStripePurchase(background, e)
		if d, ok := err.(*Diagnostic); !ok || (d.Code != CodePaymentExpired && d.Code != CodePaymentMissing) {
			t.Error("cutoff race funded")
		}
	}()
	go func() {
		defer wg.Done()
		if _, err := s.PurgeSyntheticPayments(background); err != nil {
			t.Error("cutoff purge")
		}
	}()
	wg.Wait()
	if purchases, sales, _ := paymentCounts(t, s); purchases != 0 || sales != 1 {
		t.Fatal("cutoff race retained mapping or duplicated sale")
	}
	if balance(t, s, p.AccountID).Available != "5000100" {
		t.Fatal("cutoff race changed funds")
	}
}
