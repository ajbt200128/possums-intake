package payments

import (
	"context"
	"errors"
	"testing"
	"time"

	"possums-intake/internal/ledger"
	"possums-intake/internal/stripepay"
)

func sandboxAdapter(t *testing.T) *stripepay.Client {
	t.Helper()
	// Authored synthetic shape fixtures, not provider-issued credentials. Tests
	// never invoke any network-capable sandbox operation.
	c, err := stripepay.NewSandbox(stripepay.SandboxConfig{
		ReturnOrigin: "https://127.0.0.1:8443", APIKey: "sk_test_SyntheticOnlyNotACredential123456",
		WebhookSecret: "whsec_SyntheticOnlyNotASigningSecret123456",
	})
	if err != nil {
		t.Fatal("synthetic sandbox adapter configuration")
	}
	return c
}

func TestSandboxAndOfflineModeMismatchRefusedBeforePurge(t *testing.T) {
	f := newFixture(t, true)
	p, err := f.core.PreparePayment(context.Background(), ledger.PreparePaymentInput{AccountID: testAccount, Denomination: "usd5", Form: f.form})
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, "UPDATE ledger_meta SET clock_high_water_ms=?", int64(p.UnlinkAt))
	sandbox := sandboxAdapter(t)
	for _, test := range []struct {
		newService func(context.Context, *ledger.Service, *stripepay.Client) (*Service, error)
		client     *stripepay.Client
		code       string
	}{
		{NewOffline, sandbox, "PAYMENT_OFFLINE_CONFIGURATION_INVALID"},
		{NewSandbox, f.client, "PAYMENT_SANDBOX_CONFIGURATION_INVALID"},
	} {
		service, err := test.newService(context.Background(), f.core, test.client)
		var d *Error
		if service != nil || !errors.As(err, &d) || d.Code != test.code || d.Stage != "payment_admission" || d.Outcome != "not_attempted" {
			t.Fatal("mode mismatch not refused at admission")
		}
		if f.count(t, "payment_purchases") != 1 {
			t.Fatal("mode mismatch performed overdue purge")
		}
	}
	service, err := NewSandbox(context.Background(), f.core, sandbox)
	if err != nil || service == nil || f.count(t, "payment_purchases") != 0 {
		t.Fatal("sandbox admission did not drain overdue purge")
	}
	if service.CheckoutOrigin() != "https://checkout.stripe.com" || !service.CheckoutURLAllowed("https://checkout.stripe.com/c/pay/cs_test_synthetic#SyntheticFragment") || service.CheckoutURLAllowed(f.provider.URL+"/checkout/synthetic") {
		t.Fatal("sandbox navigation policy forwarding changed")
	}
	if posts, gets := f.requests(); posts != 0 || gets != 0 {
		t.Fatal("admission performed provider work")
	}
}

func TestSandboxAdmissionRequiresExplicitSchemaAndValidContext(t *testing.T) {
	f := newFixture(t, false)
	client := sandboxAdapter(t)
	service, err := NewSandbox(context.Background(), f.core, client)
	var d *ledger.Diagnostic
	if service != nil || !errors.As(err, &d) || d.Code != ledger.CodePaymentDisabled {
		t.Fatal("sandbox admission implicitly initialized schema or lost typed failure")
	}
	if err := f.core.InitializeSyntheticPayments(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*stripepay.Client{nil, new(stripepay.Client), f.client} {
		if service, err := NewSandbox(context.Background(), f.core, c); err == nil || service != nil {
			t.Fatal("unconfigured/non-sandbox client admitted")
		}
	}
	if service, err := NewSandbox(nil, f.core, client); err == nil || service != nil {
		t.Fatal("nil context admitted")
	}
	if service, err := NewSandbox(context.Background(), nil, client); err == nil || service != nil {
		t.Fatal("nil ledger admitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service, err = NewSandbox(ctx, f.core, client)
	var local *Error
	if service != nil || !errors.As(err, &local) || local.Code != "PAYMENT_PURGE_UNAVAILABLE" {
		t.Fatal("canceled sandbox admission enabled or unclassified")
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if service, err := NewSandbox(ctx, f.core, client); err == nil || service != nil {
		t.Fatal("expired sandbox admission enabled")
	}
	if posts, gets := f.requests(); posts != 0 || gets != 0 {
		t.Fatal("sandbox admission performed provider work")
	}
}
