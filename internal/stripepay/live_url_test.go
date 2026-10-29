package stripepay

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestLiveHostedURLOpaqueInternalsPreservedBySDKCreate(t *testing.T) {
	for name, suffix := range map[string]string{
		"root_path":         "/",
		"opaque_path":       "/provider-owned/synthetic/route",
		"opaque_query":      "/synthetic?opaque=%zz",
		"query_fragment":    "/synthetic?opaque=%00%ff+value&next=%2f#%00%ff%20",
		"long_fragment":     "/synthetic#" + strings.Repeat("A", 2049),
		"encoded_path":      "/%73ynthetic/%2froute?opaque=%2f#%41%2f",
		"unicode":           "/synthetic/é?opaque=é#é",
		"test_looking_path": "/c/pay/" + fixtureSession,
		"empty_query":       "/synthetic?",
		"empty_fragment":    "/synthetic#",
		"total_bound":       "/" + strings.Repeat("A", 4096-len(sandboxCheckoutOrigin)-1),
	} {
		t.Run(name, func(t *testing.T) {
			f := newLiveFixture(t, 500, 40)
			c := liveClient(t, f.server.URL)
			makeCreateFixture(f)
			want := sandboxCheckoutOrigin + suffix
			f.session["url"] = want
			captureOutput(t, func() {
				// Policy checks are pure; only the explicit create may dispatch.
				allowed := c.CheckoutURLAllowed(want)
				if len(f.snapshot()) != 0 {
					t.Fatal("live navigation policy dispatched")
				}
				got, err := c.CreateCheckout(context.Background(), createInput(f))
				if err != nil || got.HostedURL != want || !allowed || !c.CheckoutURLAllowed(got.HostedURL) {
					t.Fatal("live opaque navigation was rejected or rewritten")
				}
				projection, err := json.Marshal(got.Session)
				if err != nil || strings.Contains(string(projection), want) || strings.Contains(string(projection), hostile) {
					t.Fatal("hosted URL or provider detail escaped Session projection")
				}
			})
			requests := f.snapshot()
			if len(requests) != 1 || requests[0].method != http.MethodPost || requests[0].header.Get("X-Stripe-Client-Telemetry") != "" || !requests[0].close {
				t.Fatal("live create dispatch or privacy boundary changed")
			}
		})
	}
}

func TestLiveHostedURLRejectsUnsafeNavigationWithoutDispatch(t *testing.T) {
	f := newLiveFixture(t, 500, 40)
	c := liveClient(t, f.server.URL)
	for name, value := range map[string]string{
		"empty": "", "no_path": sandboxCheckoutOrigin,
		"query_no_path":    sandboxCheckoutOrigin + "?opaque=synthetic",
		"fragment_no_path": sandboxCheckoutOrigin + "#synthetic",
		"http":             "http://checkout.stripe.com/synthetic",
		"uppercase_scheme": "HTTPS://checkout.stripe.com/synthetic",
		"uppercase_host":   "https://CHECKOUT.STRIPE.COM/synthetic",
		"port":             "https://checkout.stripe.com:443/synthetic",
		"empty_port":       "https://checkout.stripe.com:/synthetic",
		"userinfo":         "https://user@checkout.stripe.com/synthetic",
		"empty_userinfo":   "https://@checkout.stripe.com/synthetic",
		"credentials":      "https://user:password@checkout.stripe.com/synthetic",
		"alternate_host":   "https://api.stripe.com/synthetic",
		"subdomain":        "https://sub.checkout.stripe.com/synthetic",
		"suffix_host":      "https://checkout.stripe.com.invalid/synthetic",
		"trailing_dot":     "https://checkout.stripe.com./synthetic",
		"opaque":           "https:checkout.stripe.com/synthetic",
		"relative":         "/synthetic", "scheme_relative": "//checkout.stripe.com/synthetic",
		"malformed_path":     sandboxCheckoutOrigin + "/%zz",
		"malformed_fragment": sandboxCheckoutOrigin + "/synthetic#%zz",
		"malformed_host":     "https://[checkout.stripe.com/synthetic",
		"oversize":           sandboxCheckoutOrigin + "/" + strings.Repeat("A", 4096-len(sandboxCheckoutOrigin)),
	} {
		t.Run(name, func(t *testing.T) {
			if c.CheckoutURLAllowed(value) {
				t.Fatal("unsafe live navigation accepted")
			}
		})
	}
	unsafeBytes := []byte{0x7f, '\\'}
	for b := byte(0); b <= 0x20; b++ {
		unsafeBytes = append(unsafeBytes, b)
	}
	for _, b := range unsafeBytes {
		t.Run("raw_byte_"+strconv.Itoa(int(b)), func(t *testing.T) {
			for _, value := range []string{
				string(b) + sandboxCheckoutOrigin + "/synthetic",
				sandboxCheckoutOrigin + "/synthetic" + string(b),
				sandboxCheckoutOrigin + "/synthetic?opaque=" + string(b),
				sandboxCheckoutOrigin + "/synthetic#" + string(b),
			} {
				if c.CheckoutURLAllowed(value) {
					t.Fatal("ambiguous raw navigation byte accepted")
				}
			}
		})
	}
	if len(f.snapshot()) != 0 {
		t.Fatal("navigation checks dispatched provider work")
	}
}

func TestLiveCreateRequiresLiveSessionObjectNotURLSyntax(t *testing.T) {
	for _, scenario := range []string{"test_mode", "test_id"} {
		t.Run(scenario, func(t *testing.T) {
			f := newLiveFixture(t, 500, 40)
			c := liveClient(t, f.server.URL)
			makeCreateFixture(f)
			// A fixed-origin URL cannot establish the API object's identity/mode.
			f.session["url"] = sandboxCheckoutOrigin + "/provider-owned?opaque=synthetic#%00"
			constraint := ConstraintLiveContext
			if scenario == "test_mode" {
				f.session["livemode"] = false
			} else {
				f.session["id"] = fixtureSession
				constraint = ConstraintSession
			}
			captureOutput(t, func() {
				got, err := c.CreateCheckout(context.Background(), createInput(f))
				d := requireFailure(t, err, StageCreate, constraint, OutcomeCreateUnknown)
				if got != (Checkout{}) || d.HTTPStatus != 200 || len(f.snapshot()) != 1 {
					t.Fatal("live object refusal lost outcome or replayed")
				}
			})
		})
	}
}
