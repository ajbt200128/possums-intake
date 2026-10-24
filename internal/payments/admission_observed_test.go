package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
	"possums-intake/internal/stripepay"
	"possums-intake/internal/testfixture"
)

type admissionObserver struct {
	kinds       []failuretype.Type
	invalid     int
	panicOnCall bool
	onObserve   func()
}

func (o *admissionObserver) ObserveFailure(kind failuretype.Type) {
	o.kinds = append(o.kinds, kind)
	if o.onObserve != nil {
		o.onObserve()
	}
	if o.panicOnCall {
		panic(privateSentinel)
	}
}
func (o *admissionObserver) InvalidateFailures() {
	o.invalid++
	if o.panicOnCall {
		panic(privateSentinel)
	}
}
func (o *admissionObserver) want(t *testing.T, kinds ...failuretype.Type) {
	t.Helper()
	if !reflect.DeepEqual(o.kinds, kinds) || o.invalid != 0 {
		t.Fatalf("closed admission observations differ: got %v / invalid %d; want %v", o.kinds, o.invalid, kinds)
	}
}

// No server, transport override or SDK operation is needed for admission.
func admissionFixture(t *testing.T, identity ledger.PaymentIdentity) *fixture {
	t.Helper()
	f := &fixture{dir: filepath.Join(t.TempDir(), "state")}
	ctx := context.Background()
	if err := ledger.Init(ctx, f.dir); err != nil {
		t.Fatal("synthetic ledger initialization")
	}
	var err error
	f.core, err = ledger.Open(ctx, f.dir)
	if err != nil {
		t.Fatal("synthetic ledger open")
	}
	t.Cleanup(func() { _ = f.core.Close() })
	if _, err := testfixture.Create(ctx, f.core, testAccount); err != nil {
		t.Fatal("synthetic account")
	}
	switch identity {
	case ledger.PaymentIdentityLiveStripeTax:
		err = f.core.InitializeLiveStripePayments(ctx)
	case ledger.PaymentIdentitySynthetic:
		err = f.core.InitializeSyntheticPayments(ctx)
	}
	if err != nil {
		t.Fatal("explicit feature initialization")
	}
	now := time.Now().UnixMilli()
	f.form = ledger.PaymentForm{Attempt: strings.Repeat("d", 64), IssuedAt: ledger.Millis(now), ExpiresAt: ledger.Millis(now + 600000)}
	return f
}

func requireAdmissionError(t *testing.T, service *Service, err error, code string) {
	t.Helper()
	var local *Error
	if service != nil || !errors.As(err, &local) || local.Code != code || local.Stage != "payment_admission" {
		t.Fatal("admission did not preserve closed public failure or returned partial service")
	}
	wire, encodingErr := json.Marshal(local)
	if encodingErr != nil || strings.Contains(string(wire), privateSentinel) {
		t.Fatal("admission diagnostic exposed hostile data")
	}
}

func TestLiveObservedPreflightAndContextOwnedOnce(t *testing.T) {
	client := liveAdapter(t, liveTaxCode)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	deadline, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for _, test := range []struct {
		name   string
		ctx    context.Context
		core   *ledger.Service
		client *stripepay.Client
		code   string
		kind   failuretype.Type
	}{
		{"nil_context", nil, new(ledger.Service), client, "PAYMENT_LIVE_CONFIGURATION_INVALID", failuretype.RequestInvalid},
		{"nil_core", context.Background(), nil, client, "PAYMENT_LIVE_CONFIGURATION_INVALID", failuretype.RequestInvalid},
		{"nil_client", context.Background(), new(ledger.Service), nil, "PAYMENT_LIVE_CONFIGURATION_INVALID", failuretype.RequestInvalid},
		{"zero_client", context.Background(), new(ledger.Service), new(stripepay.Client), "PAYMENT_LIVE_CONFIGURATION_INVALID", failuretype.RequestInvalid},
		{"sandbox_mode", context.Background(), new(ledger.Service), sandboxAdapter(t), "PAYMENT_LIVE_CONFIGURATION_INVALID", failuretype.RequestInvalid},
		{"cancelled", cancelled, new(ledger.Service), client, "PAYMENT_PURGE_UNAVAILABLE", failuretype.Cancelled},
		{"deadline", deadline, new(ledger.Service), client, "PAYMENT_PURGE_UNAVAILABLE", failuretype.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			o := &admissionObserver{}
			s, err := NewLiveObserved(test.ctx, test.core, test.client, o)
			requireAdmissionError(t, s, err, test.code)
			o.want(t, test.kind) // NotReady would reveal an unintended run.
			legacy, legacyErr := NewLive(test.ctx, test.core, test.client)
			requireAdmissionError(t, legacy, legacyErr, test.code)
			nilObserver, nilErr := NewLiveObserved(test.ctx, test.core, test.client, nil)
			requireAdmissionError(t, nilObserver, nilErr, test.code)
			if !reflect.DeepEqual(projectFailure(err), projectFailure(legacyErr)) || !reflect.DeepEqual(projectFailure(err), projectFailure(nilErr)) {
				t.Fatal("observation changed public failure")
			}
		})
	}
}

func TestLiveObservedModeDecisionAfterIdentityOwnsOnce(t *testing.T) {
	f := admissionFixture(t, ledger.PaymentIdentitySynthetic)
	o := &admissionObserver{onObserve: func() {
		// A successful second run proves observation is outside the writer gate.
		if id, err := f.core.PaymentIdentity(context.Background()); err != nil || id != ledger.PaymentIdentitySynthetic {
			t.Error("observation retained writer gate or changed identity")
		}
	}}
	ctx, outer := ledger.WithFailureReport(context.Background())
	s, err := NewLiveObserved(ctx, f.core, liveAdapter(t, liveTaxCode), o)
	requireAdmissionError(t, s, err, "PAYMENT_MODE_MISMATCH")
	o.want(t, failuretype.PolicyRefused)
	if outer.WasUsed() {
		t.Fatal("admission reused caller's run report")
	}
}

func TestLiveObservedRunPrimariesCountOnce(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*testing.T, *fixture)
		code   ledger.Code
		kind   failuretype.Type
	}{
		{"identity_schema", func(t *testing.T, f *fixture) { f.exec(t, "DROP TABLE payment_sales") }, ledger.CodeSchema, failuretype.SchemaRejected},
		{"purge_clock_schema", func(t *testing.T, f *fixture) { f.exec(t, "DELETE FROM ledger_meta") }, ledger.CodeSchema, failuretype.SchemaRejected},
		{"purge_clock_corrupt", func(t *testing.T, f *fixture) { f.exec(t, "UPDATE ledger_meta SET clock_high_water_ms=9999999999999") }, ledger.CodeCorrupt, failuretype.StateRejected},
		{"not_ready", func(t *testing.T, f *fixture) {
			if err := f.core.Close(); err != nil {
				t.Fatal("synthetic close")
			}
		}, ledger.CodeClosed, failuretype.NotReady},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := admissionFixture(t, ledger.PaymentIdentityLiveStripeTax)
			test.change(t, f)
			o := &admissionObserver{}
			ctx, outer := ledger.WithFailureReport(context.Background())
			s, err := NewLiveObserved(ctx, f.core, liveAdapter(t, liveTaxCode), o)
			var d *ledger.Diagnostic
			if s != nil || !errors.As(err, &d) || d.Code != test.code || ledger.FailureKind(err, 0) != test.kind {
				t.Fatal("run primary lost its public diagnostic or closed category")
			}
			o.want(t, test.kind)
			if outer.WasUsed() {
				t.Fatal("primary published into caller report")
			}
			s, err = NewLiveObserved(context.Background(), f.core, liveAdapter(t, liveTaxCode), nil)
			if s != nil || err == nil || !ledger.FailureKind(err, 0).Valid() {
				t.Fatal("nil observer lost typed core failure")
			}
		})
	}
}

func TestLiveObservedDrainsMultiplePairedRunsWithoutReportReuse(t *testing.T) {
	f := admissionFixture(t, ledger.PaymentIdentityLiveStripeTax)
	var cutoff int64
	const pairs = ledger.CleanupLimit/2 + 1
	for i := range pairs {
		form := f.form
		form.Attempt = fmt.Sprintf("%064x", i+1)
		p, err := f.core.PreparePayment(context.Background(), ledger.PreparePaymentInput{AccountID: testAccount, Denomination: "usd5", Form: form, TaxCode: liveTaxCode})
		if err != nil {
			t.Fatal("purge fixture prepare")
		}
		if _, claimed, err := f.core.ClaimPaymentCreate(context.Background(), testAccount, p.Reference); err != nil || !claimed {
			t.Fatal("purge fixture claim")
		}
		purchase, err := adapterPurchase(p)
		if err != nil {
			t.Fatal("purge fixture projection")
		}
		e := liveEvidence(purchase, 47)
		e.Session.SessionID = fmt.Sprintf("cs_live_observed%d", i)
		e.Session.PaymentIntentID = fmt.Sprintf("pi_observed%d", i)
		if r, err := f.core.FundStripePurchase(context.Background(), fundingEvidence(e)); err != nil || r.Status != ledger.PaymentFunded {
			t.Fatal("synthetic canonical funding")
		}
		cutoff = max(cutoff, int64(p.UnlinkAt))
	}
	f.exec(t, "UPDATE ledger_meta SET clock_high_water_ms=?", cutoff)
	ctx, outer := ledger.WithFailureReport(context.Background())
	o := &admissionObserver{}
	s, err := NewLiveObserved(ctx, f.core, liveAdapter(t, liveTaxCode), o)
	if err != nil || s == nil || s.identity != ledger.PaymentIdentityLiveStripeTax || !s.configured() || f.count(t, "payment_purchases") != 0 || f.count(t, "payment_sales") != 0 {
		t.Fatal("admission did not drain multiple paired batches")
	}
	o.want(t) // Reusing even a successful report must invalidate, never healthy zero.
	if outer.WasUsed() {
		t.Fatal("admission carried caller report across runs")
	}
	// The untouched outer report can still own exactly one later ledger call.
	if _, err := f.core.PaymentIdentity(ctx); err != nil {
		t.Fatal("caller report no longer usable")
	}
	ledger.ObserveRunFailures(o, outer, nil, failuretype.RuntimeSetupUnknown)
	o.want(t)
	f.assertBalance(t, ledger.Money(fmt.Sprint(uint64(pairs)*5000000)))
}

type panicAdmissionContext struct{ context.Context }

func (panicAdmissionContext) Deadline() (time.Time, bool) { panic(privateSentinel) }

func TestLiveObservedPanicsClearServiceAndObserverCannotChangeOutcome(t *testing.T) {
	o := &admissionObserver{}
	s, err := NewLiveObserved(panicAdmissionContext{context.Background()}, new(ledger.Service), liveAdapter(t, liveTaxCode), o)
	requireAdmissionError(t, s, err, "PAYMENT_COMPONENT_CAUSE_UNKNOWN")
	o.want(t, failuretype.RuntimeSetupUnknown)
	var d *Error
	if !errors.As(err, &d) || !d.CauseUnknown || d.Outcome != "unknown" {
		t.Fatal("panic invented an outcome or cause")
	}
	// The same deferred owner must clear an already assigned partial result.
	s, err = func() (out *Service, err error) {
		defer recoverAdmission(&out, &err, nil)
		out = &Service{}
		panic(privateSentinel)
	}()
	requireAdmissionError(t, s, err, "PAYMENT_COMPONENT_CAUSE_UNKNOWN")
	for _, core := range []*ledger.Service{nil, new(ledger.Service)} {
		o := &admissionObserver{panicOnCall: true}
		s, err := NewLiveObserved(context.Background(), core, liveAdapter(t, liveTaxCode), o)
		if s != nil || err == nil || len(o.kinds) != 1 || o.invalid != 1 {
			t.Fatal("observer panic changed admission or recounted its failure")
		}
		if core == nil {
			requireAdmissionError(t, s, err, "PAYMENT_LIVE_CONFIGURATION_INVALID")
		} else if ledger.FailureKind(err, 0) != failuretype.NotReady {
			t.Fatal("observer panic erased core primary")
		}
	}
}

var _ failureowner.Observer = (*admissionObserver)(nil)
