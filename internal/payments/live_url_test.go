package payments

import (
	"context"
	"strings"
	"testing"

	"possums-intake/internal/stripepay"
)

// Only create's DTO is synthetic. The actual coordinator, accounting bindings
// and constructor-owned URL policy run unchanged; no live SDK method is called.
type opaqueLiveURLAdapter struct {
	*liveFixtureAdapter
	url string
}

func (a *opaqueLiveURLAdapter) CreateCheckout(ctx context.Context, in stripepay.CreateRequest) (stripepay.Checkout, error) {
	out, err := a.liveFixtureAdapter.CreateCheckout(ctx, in)
	out.HostedURL = a.url
	return out, err
}

func TestLiveOpaqueURLCoordinatorPreservesNavigationWithoutRetentionOrReplay(t *testing.T) {
	f, adapter := newLiveFixture(t)
	want := "https://checkout.stripe.com/%73ynthetic/%2froute?opaque=%00%ff#" + strings.Repeat("A", 2049)
	f.service.stripe = &opaqueLiveURLAdapter{liveFixtureAdapter: adapter, url: want}
	captureNoOutput(t, func() {
		result, err := f.service.StartTopup(context.Background(), testAccount, f.form, "usd5")
		if err != nil || result.URL != want || result.Status != "awaiting_payment" {
			t.Fatal("opaque live URL lost through coordinator binding")
		}
		// The website's second guard delegates to exactly this pure accessor.
		if !f.service.CheckoutURLAllowed(result.URL) || f.service.CheckoutOrigin() != "https://checkout.stripe.com" {
			t.Fatal("downstream navigation policy rejected opaque live URL")
		}
		repeat, err := f.service.StartTopup(context.Background(), testAccount, f.form, "usd5")
		if err != nil || repeat.URL != "" || repeat.Reference != result.Reference {
			t.Fatal("duplicate create replayed or retained URL")
		}
		status, err := f.service.TopupStatus(context.Background(), testAccount, result.Reference)
		if err != nil || status.URL != "" || status.Reference != "" || status.Status != "awaiting_payment" {
			t.Fatal("status retained navigation URL or changed outcome")
		}
		private := []string{want, "https://", "opaque=", strings.Repeat("A", 2049)}
		for _, table := range []string{"payment_purchases", "payment_sales", "ledger_credits", "ledger_reservations"} {
			checkTableValues(t, f.dir, "data.db", table, private)
		}
		checkTableValues(t, f.dir, "auxiliary.db", "_logs", private)
		f.assertBalance(t, "0")
	})
	if creates, reads := adapter.calls(); creates != 1 || reads != 0 {
		t.Fatal("navigation policy or status initiated provider work")
	}
	if posts, gets := f.requests(); posts != 0 || gets != 0 {
		t.Fatal("offline live coordinator fixture dispatched HTTP")
	}
}
