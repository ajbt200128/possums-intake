package payments

import (
	"context"
	"testing"

	"possums-intake/internal/ledger"
)

func TestServiceIsLiveConfigurationOnly(t *testing.T) {
	if (*Service)(nil).IsLive() || new(Service).IsLive() {
		t.Fatal("PAYMENT_MODE_UNCONFIGURED")
	}
	f, adapter := newLiveFixture(t)
	for range 3 {
		if !f.service.IsLive() {
			t.Fatal("PAYMENT_MODE_LIVE")
		}
	}
	if creates, reads := adapter.calls(); creates != 0 || reads != 0 {
		t.Fatal("PAYMENT_MODE_PROVIDER_WORK")
	}
	for _, mutate := range []func(*Service){
		func(s *Service) { s.ledger = nil },
		func(s *Service) { s.stripe = nil },
		func(s *Service) { s.identity = ledger.PaymentIdentitySynthetic },
		func(s *Service) { s.identity = "" },
		func(s *Service) { s.sandbox = true },
		func(s *Service) { s.stripe = sandboxAdapter(t) },
	} {
		copy := *f.service
		mutate(&copy)
		if copy.IsLive() {
			t.Fatal("PAYMENT_MODE_INCOMPLETE_OR_MISMATCHED")
		}
	}
	// The public constructor keeps the real, inert SDK client. Closing the
	// synthetic ledger proves the getter does not try to query durable state.
	real, err := NewLive(context.Background(), f.core, liveAdapter(t, liveTaxCode))
	if err != nil || !real.IsLive() {
		t.Fatal("PAYMENT_MODE_REAL_CLIENT_ADMISSION")
	}
	if f.core.Close() != nil || !real.IsLive() || !f.service.IsLive() {
		t.Fatal("PAYMENT_MODE_QUERIED_CLOSED_LEDGER")
	}
	if posts, gets := f.requests(); posts != 0 || gets != 0 {
		t.Fatal("PAYMENT_MODE_FIXTURE_PROVIDER_WORK")
	}
}

func TestServiceIsLiveSyntheticAndSandboxFalse(t *testing.T) {
	f := newFixture(t, true)
	if f.service.IsLive() {
		t.Fatal("PAYMENT_MODE_OFFLINE_LIVE")
	}
	sandbox, err := NewSandbox(context.Background(), f.core, sandboxAdapter(t))
	if err != nil || sandbox.IsLive() {
		t.Fatal("PAYMENT_MODE_SANDBOX_LIVE")
	}
	if posts, gets := f.requests(); posts != 0 || gets != 0 {
		t.Fatal("PAYMENT_MODE_TEST_PROVIDER_WORK")
	}
}
