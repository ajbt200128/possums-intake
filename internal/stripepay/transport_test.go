package stripepay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestWarmClientFailureUsesFreshConnectionAndDoesNotReplay(t *testing.T) {
	f := newFixture(t)
	makeCreateFixture(f)
	var calls atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.record(t, r)
		if calls.Add(1) == 1 {
			_ = json.NewEncoder(w).Encode(f.session)
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error("fixture hijack failed")
			return
		}
		_ = conn.Close()
	}))
	defer server.Close()
	f.session["url"] = server.URL + "/checkout/synthetic"
	c := offlineClient(t, server.URL)
	if _, err := c.CreateCheckout(context.Background(), createInput(f)); err != nil {
		t.Fatal(err)
	}
	_, err := c.CreateCheckout(context.Background(), createInput(f))
	requireFailure(t, err, StageCreate, ConstraintTransport, OutcomeCreateUnknown)
	requests := f.snapshot()
	if calls.Load() != 2 || len(requests) != 2 || requests[0].remote == requests[1].remote || !requests[0].close || !requests[1].close {
		t.Fatal("warm-client transport reused a connection or replayed a mutation")
	}
}

func TestCancelledAndInterruptedCreateNeverReplay(t *testing.T) {
	f := newFixture(t)
	c := offlineClient(t, f.server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.CreateCheckout(ctx, createInput(f))
	requireFailure(t, err, StageCreate, ConstraintTransport, OutcomeCreateUnknown)
	if len(f.snapshot()) != 0 {
		t.Fatal("cancelled operation dispatched")
	}

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		// Drain the synthetic body before waiting for disconnect cancellation.
		if r.ParseForm() != nil {
			t.Error("fixture form invalid")
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	c = offlineClient(t, server.URL)
	ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = c.CreateCheckout(ctx, createInput(f))
	requireFailure(t, err, StageCreate, ConstraintTransport, OutcomeCreateUnknown)
	if time.Since(start) > 3*time.Second || calls.Load() != 1 {
		t.Fatal("create cancellation/replay bound violated")
	}
}

func TestAllFixedDenominationsUseIntegerWireAmounts(t *testing.T) {
	for _, amount := range []uint64{500, 1000, 2500, 5000} {
		t.Run(strconv.FormatUint(amount, 10), func(t *testing.T) {
			f := newFixture(t)
			makeCreateFixture(f)
			f.purchase.GrossCents = amount
			f.session["amount_total"] = amount
			f.session["amount_subtotal"] = amount
			lineItem(f)["amount_total"] = amount
			lineItem(f)["amount_subtotal"] = amount
			price(f)["unit_amount"] = amount
			price(f)["unit_amount_decimal"] = strconv.FormatUint(amount, 10)
			c := offlineClient(t, f.server.URL)
			got, err := c.CreateCheckout(context.Background(), createInput(f))
			if err != nil {
				t.Fatal(err)
			}
			if got.Session.GrossCents != amount || f.snapshot()[0].form.Get("line_items[0][price_data][unit_amount]") != strconv.FormatUint(amount, 10) {
				t.Fatal("integer denomination changed")
			}
		})
	}
}
