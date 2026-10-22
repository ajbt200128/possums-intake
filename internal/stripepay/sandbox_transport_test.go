package stripepay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSandboxSDKFaultsStayClosedAndNeverReplay(t *testing.T) {
	for _, scenario := range []string{"redirect", "account", "context", "version", "oversized_headers", "oversized_body", "hostile_error", "live_session", "unsafe_url"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			makeCreateFixture(f)
			f.session["url"] = sandboxCheckoutOrigin + "/c/pay/" + fixtureSession
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Stripe-Should-Retry", "true")
				w.Header().Set("Stripe-Notice", hostile)
				switch scenario {
				case "redirect":
					w.Header().Set("Location", f.server.URL+"/redirect-target")
					w.WriteHeader(307)
				case "account":
					w.Header().Set("Stripe-Account", hostile)
				case "context":
					w.Header().Set("Stripe-Context", hostile)
				case "version":
					w.Header().Set("Stripe-Version", "2020-01-01")
				case "oversized_headers":
					w.Header().Set("X-Synthetic-Private", strings.Repeat("A", 40<<10))
				case "oversized_body":
					_, _ = w.Write([]byte(strings.Repeat("A", MaxResponseBytes+1)))
					return
				case "hostile_error":
					w.WriteHeader(401)
					_, _ = w.Write([]byte(`{"error":{"type":"authentication_error","message":"` + hostile + `"}}`))
					return
				case "live_session":
					f.session["livemode"] = true
				case "unsafe_url":
					f.session["url"] = sandboxCheckoutOrigin + "/c/pay/cs_live_synthetic#SyntheticFragment"
				}
				_ = json.NewEncoder(w).Encode(f.session)
			}))
			defer server.Close()
			c := sandboxClient(t)
			// All SDK traffic uses this authored local fake, never api.stripe.com.
			c.sdk = newFixedSDK(syntheticSandboxKey, server.URL)
			captureOutput(t, func() {
				out, err := c.CreateCheckout(context.Background(), createInput(f))
				stage, constraint := StageCreate, ConstraintContext
				switch scenario {
				case "redirect":
					constraint = ConstraintRedirect
				case "oversized_headers":
					constraint = ConstraintTransport
				case "oversized_body":
					constraint = ConstraintResponseSize
				case "hostile_error":
					constraint = ConstraintStatus
				case "unsafe_url":
					stage, constraint = StageHostedURL, ConstraintSandboxURL
				}
				d := requireFailure(t, err, stage, constraint, OutcomeCreateUnknown)
				if out.HostedURL != "" || strings.Contains(d.Error(), syntheticSandboxKey) || strings.Contains(d.Error(), syntheticSandboxSecret) {
					t.Fatal("sandbox failure retained private values")
				}
				if scenario == "hostile_error" && d.HTTPStatus != 401 {
					t.Fatal("observed HTTP status lost")
				}
			})
			if calls.Load() != 1 || len(f.snapshot()) != 0 {
				t.Fatal("sandbox fault retried or followed redirect")
			}
		})
	}
}
