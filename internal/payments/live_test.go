package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v87/webhook"
	"possums-intake/internal/ledger"
	"possums-intake/internal/stripepay"
)

const liveTaxCode = "txcd_10000000"
const liveFixtureSecret = "whsec_SyntheticOnlyNotASigningSecret123456"

func liveAdapter(t *testing.T, code string) *stripepay.Client {
	t.Helper()
	client, err := stripepay.NewLive(stripepay.LiveConfig{ReturnOrigin: "https://payments.example.com", APIKey: "sk_live_SyntheticOnlyNotACredential123456", WebhookSecret: liveFixtureSecret, TaxCode: code})
	if err != nil {
		t.Fatal("authored live shape fixture rejected")
	}
	return client
}

// Only canonical create/retrieve DTOs are faked. Accessors, URL policy and raw
// signature/API/mode verification use the actual SDK client. No endpoint/private
// SDK state is overridden and no network-capable live method is ever invoked.
type liveFixtureAdapter struct {
	*stripepay.Client
	mu                 sync.Mutex
	creates, reads     int
	created            stripepay.Purchase
	expected           stripepay.Purchase
	evidence           stripepay.Evidence
	createErr, readErr error
	onCreate, onRead   func()
}

func liveSession(p stripepay.Purchase) stripepay.Session {
	return stripepay.Session{Reference: p.Reference, SessionID: "cs_live_synthetic", CreatedSeconds: p.IssuedAt.Unix(), ExpiresSeconds: p.ExpiresAt.Unix(), Currency: "usd", GrossCents: p.GrossCents, Quantity: 1, LiveMode: true, TaxCode: p.TaxCode}
}
func liveEvidence(p stripepay.Purchase, tax uint64) stripepay.Evidence {
	s := liveSession(p)
	s.PaymentIntentID = testIntent
	return stripepay.Evidence{Session: s, Tax: &stripepay.TaxEvidence{AmountCents: tax, TotalCents: p.GrossCents + tax, Complete: true, Exclusive: true, LiabilitySelf: true}, AmountReceived: p.GrossCents + tax, AmountCaptured: p.GrossCents + tax, Paid: true, Captured: true, ChargeCreatedSeconds: s.CreatedSeconds}
}
func (a *liveFixtureAdapter) CreateCheckout(_ context.Context, r stripepay.CreateRequest) (stripepay.Checkout, error) {
	a.mu.Lock()
	a.creates++
	a.created = r.Purchase
	a.evidence = liveEvidence(r.Purchase, 47)
	callback, err := a.onCreate, a.createErr
	a.mu.Unlock()
	if callback != nil {
		callback()
	}
	if err != nil {
		return stripepay.Checkout{}, err
	}
	return stripepay.Checkout{Session: liveSession(r.Purchase), HostedURL: "https://checkout.stripe.com/c/pay/cs_live_synthetic#SyntheticOnly"}, nil
}
func (a *liveFixtureAdapter) RetrieveEvidence(_ context.Context, _ string, p stripepay.Purchase) (stripepay.Evidence, error) {
	a.mu.Lock()
	a.reads++
	a.expected = p
	e, err, callback := a.evidence, a.readErr, a.onRead
	if e.Tax != nil {
		tax := *e.Tax
		e.Tax = &tax
	}
	a.mu.Unlock()
	if callback != nil {
		callback()
	}
	return e, err
}
func (a *liveFixtureAdapter) change(fn func()) { a.mu.Lock(); defer a.mu.Unlock(); fn() }
func (a *liveFixtureAdapter) calls() (int, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.creates, a.reads
}
func newLiveFixture(t *testing.T) (*fixture, *liveFixtureAdapter) {
	t.Helper()
	f := newFixture(t, false)
	if err := f.core.InitializeLiveStripePayments(context.Background()); err != nil {
		t.Fatal(err)
	}
	client := liveAdapter(t, liveTaxCode)
	var err error
	f.service, err = NewLive(context.Background(), f.core, client)
	if err != nil {
		t.Fatal(err)
	}
	a := &liveFixtureAdapter{Client: client}
	f.service.stripe = a
	return f, a
}
func liveStart(t *testing.T, f *fixture) string {
	t.Helper()
	r, err := f.service.StartTopup(context.Background(), testAccount, f.form, "usd5")
	if err != nil || r.Reference == "" || r.Status != "awaiting_payment" || !f.service.CheckoutURLAllowed(r.URL) {
		t.Fatal("live start")
	}
	return r.Reference
}
func deliverLive(t *testing.T, s *Service, ref string) (int, webhookReply) {
	t.Helper()
	e := event(ref)
	e["livemode"] = true
	snapshot := e["data"].(object)["object"].(object)
	snapshot["livemode"], snapshot["id"] = true, "cs_live_synthetic"
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal("synthetic event encoding")
	}
	req := httptest.NewRequest("POST", WebhookPath, bytes.NewReader(raw))
	req.Header.Set("Stripe-Signature", webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: raw, Secret: liveFixtureSecret, Timestamp: time.Now()}).Header)
	response := httptest.NewRecorder()
	s.WebhookHandler().ServeHTTP(response, req)
	for _, forbidden := range []string{ref, testIntent, "cs_live_synthetic", liveTaxCode, liveFixtureSecret, privateSentinel, "https://"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatal("private live response")
		}
	}
	var reply webhookReply
	if json.Unmarshal(response.Body.Bytes(), &reply) != nil {
		t.Fatal("live response decoding")
	}
	return response.Code, reply
}

func TestAdmissionPersistedIdentityAndAdapterModeMatrix(t *testing.T) {
	constructors := []struct {
		name       string
		newService func(context.Context, *ledger.Service, *stripepay.Client) (*Service, error)
		clientMode int
		identity   ledger.PaymentIdentity
	}{{"offline", NewOffline, 2, ledger.PaymentIdentitySynthetic}, {"sandbox", NewSandbox, 3, ledger.PaymentIdentitySynthetic}, {"live", NewLive, 4, ledger.PaymentIdentityLiveStripeTax}}
	for _, identity := range []ledger.PaymentIdentity{"", ledger.PaymentIdentitySynthetic, ledger.PaymentIdentityLiveStripeTax} {
		t.Run(string(identity), func(t *testing.T) {
			f := newFixture(t, false)
			switch identity {
			case ledger.PaymentIdentitySynthetic:
				if err := f.core.InitializeSyntheticPayments(context.Background()); err != nil {
					t.Fatal(err)
				}
			case ledger.PaymentIdentityLiveStripeTax:
				if err := f.core.InitializeLiveStripePayments(context.Background()); err != nil {
					t.Fatal(err)
				}
				// Its historical success must NOT authorize synthetic admission.
				if err := f.core.InitializeSyntheticPayments(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			clients := []*stripepay.Client{nil, new(stripepay.Client), f.client, sandboxAdapter(t), liveAdapter(t, liveTaxCode)}
			for _, constructor := range constructors {
				for mode, client := range clients {
					s, err := constructor.newService(context.Background(), f.core, client)
					want := identity == constructor.identity && mode == constructor.clientMode
					if want != (err == nil && s != nil) || (!want && s != nil) {
						t.Fatalf("admission matrix %s/%d", constructor.name, mode)
					}
					if err != nil {
						requireErrorType(t, err)
					}
				}
			}
			if identity == "" {
				if _, err := f.core.PaymentIdentity(context.Background()); err == nil {
					t.Fatal("admission created schema")
				}
			}
			if posts, gets := f.requests(); posts != 0 || gets != 0 {
				t.Fatal("admission dispatched")
			}
		})
	}
	f, _ := newLiveFixture(t)
	for _, input := range []struct {
		ctx  context.Context
		core *ledger.Service
	}{{nil, f.core}, {context.Background(), nil}} {
		if s, err := NewLive(input.ctx, input.core, liveAdapter(t, liveTaxCode)); s != nil || err == nil {
			t.Fatal("invalid live admission")
		}
	}
}

func TestLiveAdmissionDrainsPairedBatchesAndCancellationAdmitsNothing(t *testing.T) {
	f, a := newLiveFixture(t)
	var cutoff int64
	const pairs = ledger.CleanupLimit/2 + 1
	for i := 0; i < pairs; i++ {
		form := f.form
		form.Attempt = fmt.Sprintf("%064x", i+1)
		p, err := f.core.PreparePayment(context.Background(), ledger.PreparePaymentInput{AccountID: testAccount, Denomination: "usd5", Form: form, TaxCode: liveTaxCode})
		if err != nil {
			t.Fatal(err)
		}
		if _, claimed, err := f.core.ClaimPaymentCreate(context.Background(), testAccount, p.Reference); err != nil || !claimed {
			t.Fatal("purge fixture claim")
		}
		expected, err := adapterPurchase(p)
		if err != nil {
			t.Fatal(err)
		}
		e := liveEvidence(expected, 47)
		e.Session.SessionID = fmt.Sprintf("cs_live_purge%d", i)
		e.Session.PaymentIntentID = fmt.Sprintf("pi_purge%d", i)
		if r, err := f.core.FundStripePurchase(context.Background(), fundingEvidence(e)); err != nil || r.Status != ledger.PaymentFunded {
			t.Fatal("purge fixture funding")
		}
		cutoff = max(cutoff, int64(p.UnlinkAt))
	}
	f.exec(t, "UPDATE ledger_meta SET clock_high_water_ms=?", cutoff)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s, err := NewLive(ctx, f.core, a.Client); s != nil || err == nil {
		t.Fatal("canceled purge admitted")
	}
	if f.count(t, "payment_sales") != pairs || f.count(t, "payment_purchases") != pairs {
		t.Fatal("canceled purge mutated rows")
	}
	for _, input := range []struct {
		constructor func(context.Context, *ledger.Service, *stripepay.Client) (*Service, error)
		client      *stripepay.Client
	}{{NewOffline, f.client}, {NewSandbox, sandboxAdapter(t)}} {
		if s, err := input.constructor(context.Background(), f.core, input.client); s != nil || err == nil {
			t.Fatal("wrong mode admitted live rows")
		}
	}
	if f.count(t, "payment_purchases") != pairs {
		t.Fatal("mode refusal purged")
	}
	s, err := NewLive(context.Background(), f.core, a.Client)
	if err != nil || s == nil || f.count(t, "payment_sales") != 0 || f.count(t, "payment_purchases") != 0 {
		t.Fatal("paired backlog admitted before drain")
	}
	if posts, reads := a.calls(); posts != 0 || reads != 0 {
		t.Fatal("purge made remote call")
	}
}

func TestLiveTrustedTaxCodeOriginalMappingAndConcurrentFunding(t *testing.T) {
	for _, tax := range []uint64{0, 47} {
		t.Run(fmt.Sprint(tax), func(t *testing.T) {
			f, a := newLiveFixture(t)
			ref := liveStart(t, f)
			repeat, err := f.service.StartTopup(context.Background(), testAccount, f.form, "usd5")
			if err != nil || repeat.Reference != ref || repeat.URL != "" {
				t.Fatal("live duplicate start replayed URL")
			}
			if creates, reads := a.calls(); creates != 1 || reads != 0 {
				t.Fatal("live duplicate start dispatched")
			}
			p, err := f.core.GetPayment(context.Background(), testAccount, ref)
			if err != nil || p.TaxCode != liveTaxCode || int64(p.IssuedAt)%1000 != 789 {
				t.Fatal("trusted original code/time not stored")
			}
			expected, err := adapterPurchase(p)
			if err != nil || expected.GrossCents != 500 || expected.TaxCode != liveTaxCode || expected.IssuedAt.UnixMilli() != int64(p.IssuedAt) || expected.ExpiresAt.UnixMilli() != int64(p.CheckoutExpiresAt) {
				t.Fatal("original terms mapping")
			}
			a.change(func() { a.evidence = liveEvidence(expected, tax) })
			// A separately configured replacement keeps the original purchase code.
			replacement := liveAdapter(t, "txcd_20000000")
			s, err := NewLive(context.Background(), f.core, replacement)
			if err != nil {
				t.Fatal(err)
			}
			a.change(func() { a.Client = replacement })
			s.stripe = a
			f.service = s
			assertStatus(t, s, ref, "awaiting_payment")
			const n = 10
			var wg sync.WaitGroup
			statuses := make(chan string, n)
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					code, r := deliverLive(t, s, ref)
					if code != 200 {
						t.Error("live funding refused")
					}
					statuses <- r.Status
				}()
			}
			wg.Wait()
			close(statuses)
			funded := 0
			for status := range statuses {
				if status == "funded" {
					funded++
				} else if status != "already_funded" {
					t.Fatal("invalid concurrent result")
				}
			}
			if funded != 1 || a.expected.TaxCode != liveTaxCode || a.created.TaxCode != liveTaxCode {
				t.Fatal("funding count or original code lost")
			}
			f.assertBalance(t, "5000000")
			if f.count(t, "payment_sales") != 1 || f.count(t, "ledger_credits") != 0 {
				t.Fatal("once-only sale")
			}
			creates, reads := a.calls()
			assertStatus(t, s, ref, "funded")
			if afterCreates, afterReads := a.calls(); afterCreates != creates || afterReads != reads {
				t.Fatal("funded status dispatched")
			}
			checkTableValues(t, f.dir, "data.db", "payment_sales", []string{ref, testIntent, liveFixtureSecret, privateSentinel, "https://"})
		})
	}
}

func TestLiveTaxEvidenceMappingAndMissingWrongModesNeverFund(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*stripepay.Evidence)
	}{
		{"missing_tax", func(e *stripepay.Evidence) { e.Tax = nil }},
		{"test_mode", func(e *stripepay.Evidence) { e.Session.LiveMode = false }},
		{"missing_code", func(e *stripepay.Evidence) { e.Session.TaxCode = "" }},
		{"wrong_code", func(e *stripepay.Evidence) { e.Session.TaxCode = "txcd_20000000" }},
		{"unfinished", func(e *stripepay.Evidence) { e.Tax.Complete = false }},
		{"inclusive", func(e *stripepay.Evidence) { e.Tax.Exclusive = false }},
		{"other_liability", func(e *stripepay.Evidence) { e.Tax.LiabilitySelf = false }},
		{"arithmetic", func(e *stripepay.Evidence) { e.Tax.TotalCents++ }},
		{"received_principal", func(e *stripepay.Evidence) { e.AmountReceived = 500 }},
		{"captured_principal", func(e *stripepay.Evidence) { e.AmountCaptured = 500 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, a := newLiveFixture(t)
			ref := liveStart(t, f)
			a.change(func() { test.change(&a.evidence) })
			code, reply := deliverLive(t, f.service, ref)
			if code != 503 || reply.Error == nil {
				t.Fatal("invalid live evidence admitted")
			}
			f.assertBalance(t, "0")
			if f.count(t, "payment_sales") != 0 {
				t.Fatal("invalid evidence sold")
			}
		})
	}
	e := liveEvidence(stripepay.Purchase{GrossCents: 1000}, 81)
	mapped := fundingEvidence(e)
	if mapped.Session.GrossMinor != "1000" || mapped.IntentAmount != "1081" || mapped.ChargeAmount != "1081" || mapped.AmountReceived != "1081" || mapped.AmountCaptured != "1081" || mapped.Tax == nil || mapped.Tax.Amount != "81" {
		t.Fatal("tax/principal mapping")
	}
	e.Tax.AmountCents = 999
	if mapped.Tax.Amount != "81" {
		t.Fatal("tax projection aliased")
	}
	e.Session.LiveMode = false
	if m := fundingEvidence(e); m.IntentAmount != "1000" || m.ChargeAmount != "1000" || m.Tax == nil {
		t.Fatal("synthetic mapping hid invalid tax evidence")
	}
	if m := fundingEvidence(stripepay.Evidence{}); m.Tax != nil {
		t.Fatal("fabricated default tax")
	}
}

func TestLiveCanonicalBlocksStickyAndKnownFundingNeverClawedBack(t *testing.T) {
	for _, kind := range []string{"refund", "dispute", "listed"} {
		for _, fundFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", kind, fundFirst), func(t *testing.T) {
				f, a := newLiveFixture(t)
				ref := liveStart(t, f)
				if fundFirst {
					if code, r := deliverLive(t, f.service, ref); code != 200 || r.Status != "funded" {
						t.Fatal("initial fund")
					}
				}
				a.change(func() {
					switch kind {
					case "refund":
						a.evidence.AmountRefunded = 1
					case "dispute":
						a.evidence.Disputed = true
					case "listed":
						a.evidence.RefundObserved = true
					}
					a.readErr = &stripepay.Error{Code: stripepay.CodeEvidence, Stage: stripepay.StageCharge, Constraint: stripepay.ConstraintRefund, Outcome: stripepay.OutcomeReadUnavailable}
				})
				want, balance := "blocked", ledger.Money("0")
				if fundFirst {
					want, balance = "already_funded", "5000000"
				}
				if code, r := deliverLive(t, f.service, ref); code != 200 || r.Status != want {
					t.Fatal("block/receipt ordering")
				}
				a.change(func() {
					a.evidence.AmountRefunded = 0
					a.evidence.Disputed = false
					a.evidence.RefundObserved = false
					a.readErr = nil
				})
				if code, r := deliverLive(t, f.service, ref); code != 200 || r.Status != want {
					t.Fatal("sticky state lost")
				}
				f.assertBalance(t, balance)
			})
		}
	}
}

func TestLiveCreateUncertaintyAndBindingFailureNeverReplay(t *testing.T) {
	for _, bindFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(bindFailure), func(t *testing.T) {
			f, a := newLiveFixture(t)
			if bindFailure {
				a.onCreate = func() {
					if err := f.core.Close(); err != nil {
						t.Error("close fixture")
					}
				}
			} else {
				a.createErr = &stripepay.Error{Code: stripepay.CodeProvider, Stage: stripepay.StageCreate, Constraint: stripepay.ConstraintTransport, Outcome: stripepay.OutcomeCreateUnknown}
			}
			result, err := f.service.StartTopup(context.Background(), testAccount, f.form, "usd5")
			requireErrorType(t, err)
			if result.Reference == "" || result.URL != "" || result.Status == "funded" {
				t.Fatal("unknown create generated receipt")
			}
			if bindFailure {
				var d *Error
				var cause *ledger.Diagnostic
				if !errors.As(err, &d) || d.Outcome != "checkout_created_local_binding_unconfirmed" || !errors.As(err, &cause) {
					t.Fatal("binding outcome lost")
				}
				f.core, err = ledger.Open(context.Background(), f.dir)
				if err != nil {
					t.Fatal(err)
				}
				f.service, err = NewLive(context.Background(), f.core, a.Client)
				if err != nil {
					t.Fatal(err)
				}
				f.service.stripe = a
			}
			assertStatus(t, f.service, result.Reference, "unconfirmed")
			r, err := f.service.StartTopup(context.Background(), testAccount, f.form, "usd5")
			if err != nil || r.URL != "" || r.Reference != result.Reference {
				t.Fatal("uncertain create replay")
			}
			if creates, reads := a.calls(); creates != 1 || reads != 0 {
				t.Fatal("uncertain local action dispatched")
			}
			if code, r := deliverLive(t, f.service, result.Reference); code != 200 || r.Status != "funded" {
				t.Fatal("canonical reconciliation failed")
			}
			f.assertBalance(t, "5000000")
		})
	}
}

func TestLiveStatusAndExpiredPrunedReplayNeverDispatch(t *testing.T) {
	f, a := newLiveFixture(t)
	p, err := f.core.PreparePayment(context.Background(), ledger.PreparePaymentInput{AccountID: testAccount, Denomination: "usd5", Form: f.form, TaxCode: liveTaxCode})
	if err != nil {
		t.Fatal(err)
	}
	assertStatus(t, f.service, p.Reference, "unconfirmed") // prepared
	if _, claimed, err := f.core.ClaimPaymentCreate(context.Background(), testAccount, p.Reference); err != nil || !claimed {
		t.Fatal("claim")
	}
	assertStatus(t, f.service, p.Reference, "unconfirmed") // attempted
	if err := f.core.MarkPaymentCreateUnknown(context.Background(), testAccount, p.Reference); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, f.service, p.Reference, "unconfirmed") // unknown
	expected, err := adapterPurchase(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.core.BindPaymentSession(context.Background(), sessionEvidence(liveSession(expected))); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, f.service, p.Reference, "awaiting_payment")
	e := liveEvidence(expected, 47)
	e.RefundObserved = true
	if _, err := f.core.FundStripePurchase(context.Background(), fundingEvidence(e)); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, f.service, p.Reference, "support_required")
	other, err := f.service.TopupStatus(context.Background(), "otheraccount000", p.Reference)
	if err != nil || other.Status != "unavailable_after_unlink" || other.URL != "" || other.Reference != "" {
		t.Fatal("cross-account status")
	}
	f.exec(t, "UPDATE ledger_meta SET clock_high_water_ms=?", int64(p.UnlinkAt))
	assertStatus(t, f.service, p.Reference, "unavailable_after_unlink")
	if code, r := deliverLive(t, f.service, p.Reference); code != 200 || r.Status != "ineligible" {
		t.Fatal("expired replay admitted")
	}
	if _, err := f.core.PurgeLiveStripePayments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code, r := deliverLive(t, f.service, p.Reference); code != 200 || r.Status != "ineligible" {
		t.Fatal("pruned replay admitted")
	}
	assertStatus(t, f.service, p.Reference, "unavailable_after_unlink")
	if creates, reads := a.calls(); creates != 0 || reads != 0 {
		t.Fatal("local/expired status dispatched")
	}
	f.assertBalance(t, "0")
}

func TestLiveConfigurationMismatchDiagnosticsAreInertAndModeAware(t *testing.T) {
	f, a := newLiveFixture(t)
	a.Client = f.client // Only a private test seam can disagree with construction.
	if f.service.configured() || f.service.CheckoutOrigin() != "" || f.service.CheckoutURLAllowed("https://checkout.stripe.com/c/pay/cs_live_synthetic") {
		t.Fatal("mode drift granted navigation")
	}
	for _, stage := range []string{"payment_start", "payment_status"} {
		var err error
		if stage == "payment_start" {
			_, err = f.service.StartTopup(context.Background(), testAccount, f.form, "usd5")
		} else {
			_, err = f.service.TopupStatus(context.Background(), testAccount, strings.Repeat("a", 64))
		}
		var d *Error
		if !errors.As(err, &d) || d.Code != "PAYMENT_LIVE_CONFIGURATION_INVALID" || d.Stage != stage || strings.Contains(d.Constraint, "offline") {
			t.Fatal("misleading live diagnostic")
		}
	}
	if code, r := deliverLive(t, f.service, strings.Repeat("a", 64)); code != 503 || r.Error == nil || r.Error.Code != "PAYMENT_LIVE_CONFIGURATION_INVALID" {
		t.Fatal("live webhook diagnostic")
	}
	if creates, reads := a.calls(); creates != 0 || reads != 0 {
		t.Fatal("diagnostic dispatched")
	}
}
