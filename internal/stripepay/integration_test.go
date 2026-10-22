package stripepay

import (
	"context"
	"testing"
	"time"
)

func TestFractionalIssuanceKeepsOriginalAndRoundsExpiryDown(t *testing.T) {
	f := newFixture(t)
	f.purchase.IssuedAt = f.purchase.IssuedAt.Add(789 * time.Millisecond)
	f.purchase.ExpiresAt = f.purchase.IssuedAt.Add(time.Hour).Truncate(time.Second)
	original := f.purchase.IssuedAt
	c := offlineClient(t, f.server.URL)
	makeCreateFixture(f)
	if _, err := c.CreateCheckout(context.Background(), createInput(f)); err != nil {
		t.Fatal(err)
	}
	if !f.purchase.IssuedAt.Equal(original) || f.purchase.IssuedAt.Nanosecond() != 789000000 {
		t.Fatal("original issuance rounded")
	}
	if c.CheckoutOrigin() != f.server.URL || !c.CheckoutURLAllowed(f.server.URL+"/checkout/synthetic") || c.CheckoutURLAllowed("https://checkout.stripe.com/c/synthetic") {
		t.Fatal("fixed origin policy")
	}
	var zero Client
	if zero.CheckoutOrigin() != "" || zero.CheckoutURLAllowed(f.server.URL+"/checkout/synthetic") || (*Client)(nil).CheckoutOrigin() != "" {
		t.Fatal("zero client grants origin")
	}
}

func TestRefundValidationCannotReturnBlockFromInvalidRelationships(t *testing.T) {
	for _, change := range []func(*fixture){
		func(f *fixture) { f.charge["amount_refunded"] = -1 },
		func(f *fixture) { f.charge["amount_refunded"] = 501 },
		func(f *fixture) { f.charge["payment_intent"] = "pi_other"; f.charge["disputed"] = true },
		func(f *fixture) { f.charge["refunds"] = nil; f.charge["refunded"] = true },
		func(f *fixture) { f.charge["refunds"].(object)["data"] = []any{nil} },
		func(f *fixture) {
			f.charge["refunds"].(object)["data"] = []any{object{"object": "refund", "id": "re_synthetic", "amount": 1, "currency": "usd", "charge": "ch_other", "payment_intent": fixtureIntent}}
		},
	} {
		f := newFixture(t)
		change(f)
		got, err := offlineClient(t, f.server.URL).RetrieveEvidence(context.Background(), fixtureSession, f.purchase)
		if err == nil || got != (Evidence{}) {
			t.Fatal("invalid canonical refund returned usable evidence")
		}
	}
}
