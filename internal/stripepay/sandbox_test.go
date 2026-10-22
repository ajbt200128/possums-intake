package stripepay

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v87"
)

// Authored synthetic shape fixtures, never credentials obtained from a provider.
const (
	syntheticSandboxKey    = "sk_test_SyntheticOnlyNotACredential123456"
	syntheticSandboxSecret = "whsec_SyntheticOnlyNotASigningSecret123456"
)

func sandboxConfig() SandboxConfig {
	return SandboxConfig{ReturnOrigin: "https://127.0.0.1:8443", APIKey: syntheticSandboxKey, WebhookSecret: syntheticSandboxSecret}
}

func sandboxClient(t *testing.T) *Client {
	t.Helper()
	c, err := NewSandbox(sandboxConfig())
	if err != nil {
		t.Fatal("synthetic sandbox constructor refused")
	}
	return c
}

func sandboxBackends(c *Client) []stripe.Backend {
	return []stripe.Backend{c.sdk.V1CheckoutSessions.B, c.sdk.OAuth.B, c.sdk.V1Files.BUploads, c.sdk.V2BillingMeterEventStreams.BMeterEvents}
}

func TestSandboxConstructorFixedPrivateBackendsNoDispatch(t *testing.T) {
	f := newFixture(t)
	for _, key := range []string{syntheticSandboxKey, "rk_test_SyntheticOnlyRestrictedNotACredential123456"} {
		cfg := sandboxConfig()
		cfg.APIKey = key
		var c *Client
		captureOutput(t, func() {
			var err error
			c, err = NewSandbox(cfg)
			if err != nil {
				t.Fatal("synthetic constructor failed")
			}
		})
		if !c.IsSandbox() || c.CheckoutOrigin() != sandboxCheckoutOrigin || c.returnURL != cfg.ReturnOrigin+ReturnPath {
			t.Fatal("sandbox mode or separated navigation/return origin lost")
		}
		for _, raw := range sandboxBackends(c) {
			b, ok := raw.(*stripe.BackendImplementation)
			if !ok || b.URL != sandboxAPIOrigin || b.MaxNetworkRetries != 0 || b.StripeContext != nil {
				t.Fatal("backend fixed origin/retry/context boundary changed")
			}
			logger, ok := b.LeveledLogger.(*stripe.LeveledLogger)
			if !ok || logger.Level != stripe.LevelNull || reflect.ValueOf(b).Elem().FieldByName("enableTelemetry").Bool() {
				t.Fatal("backend logging/telemetry enabled")
			}
			h := b.HTTPClient
			tr, ok := h.Transport.(*offlineTransport)
			if !ok || tr.origin != sandboxAPIOrigin || h.Timeout != 5*time.Second || h.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
				t.Fatal("HTTP client boundary changed")
			}
			base := tr.base
			if base.Proxy != nil || !base.DisableKeepAlives || base.ForceAttemptHTTP2 || base.ResponseHeaderTimeout != requestTimeout || base.MaxResponseHeaderBytes != 32<<10 || base.TLSClientConfig != nil || base.DialTLSContext != nil {
				t.Fatal("transport privacy or default TLS verification changed")
			}
		}
	}
	var zero Client
	if zero.IsSandbox() || zero.CheckoutOrigin() != "" || (*Client)(nil).IsSandbox() || offlineClient(t, f.server.URL).IsSandbox() || len(f.snapshot()) != 0 {
		t.Fatal("constructor/zero/offline mode dispatched or granted sandbox")
	}
}

func TestSandboxConfigurationRefusals(t *testing.T) {
	cfg := sandboxConfig()
	for _, key := range []string{"", "sk_live_SyntheticNotACredential", "rk_live_SyntheticNotACredential", FixtureAPIKey, "sk_test_", "rk_test_", "sk_test_offline_fixture_other", "sk_test_Synthetic\nPrivate", "sk_test_Synthetic\x00Private", "sk_test_Syntheticé", "sk_test_" + strings.Repeat("A", maxSecretBytes)} {
		cfg.APIKey = key
		c, err := NewSandbox(cfg)
		d := requireFailure(t, err, StageSandboxConfiguration, ConstraintSandbox, OutcomeNotAttempted)
		if c != nil || d.Code != CodeSandboxConfiguration || d.NextStep != NextFixSandbox {
			t.Fatal("unsafe configuration admission/diagnostic")
		}
	}
	cfg = sandboxConfig()
	for _, secret := range []string{"", FixtureWebhookSecret, "whsec_", "whsec_offline_fixture_other", "whsec_Synthetic\rPrivate", "whsec_Synthetic Private", "whsec_Syntheticé", "whsec_" + strings.Repeat("A", maxSecretBytes)} {
		cfg.WebhookSecret = secret
		_, err := NewSandbox(cfg)
		requireFailure(t, err, StageSandboxConfiguration, ConstraintSandbox, OutcomeNotAttempted)
	}
	cfg = sandboxConfig()
	for _, origin := range []string{"", "http://127.0.0.1:8443", "https://localhost:8443", "https://[::1]:8443", "https://127.0.0.2:8443", "https://api.stripe.com", "https://127.0.0.1", "https://127.0.0.1:0", "https://127.0.0.1:65536", "https://127.0.0.1:08443", "https://user@127.0.0.1:8443", "https://127.0.0.1:8443/", "https://127.0.0.1:8443?", "https://127.0.0.1:8443#private", "https://127.0.0.1:8443#", "HTTPS://127.0.0.1:8443"} {
		cfg.ReturnOrigin = origin
		_, err := NewSandbox(cfg)
		requireFailure(t, err, StageSandboxConfiguration, ConstraintSandbox, OutcomeNotAttempted)
	}
	// A shape-valid synthetic sandbox key must still be refused by NewOffline.
	f := newFixture(t)
	_, err := NewOffline(OfflineConfig{Endpoint: f.server.URL, ReturnOrigin: f.server.URL, APIKey: syntheticSandboxKey, WebhookSecret: FixtureWebhookSecret})
	requireFailure(t, err, StageConfiguration, ConstraintOffline, OutcomeNotAttempted)
	if len(f.snapshot()) != 0 {
		t.Fatal("configuration refusal dispatched")
	}
}

func TestSandboxHostedURLCanonicalTestPathAndTransientFragment(t *testing.T) {
	c := sandboxClient(t)
	path := sandboxCheckoutOrigin + "/c/pay/" + fixtureSession
	for _, value := range []string{path, path + "#SyntheticOpaqueFragment123", path + "#Synthetic%2FFragment=123"} {
		if !c.CheckoutURLAllowed(value) {
			t.Fatal("canonical synthetic checkout URL refused")
		}
	}
	for _, value := range []string{"", "http://checkout.stripe.com/c/pay/" + fixtureSession, "https://checkout.stripe.com:443/c/pay/" + fixtureSession, "https://user@checkout.stripe.com/c/pay/" + fixtureSession, "https://checkout.stripe.com.invalid/c/pay/" + fixtureSession, "https://api.stripe.com/c/pay/" + fixtureSession, sandboxCheckoutOrigin + "/c/pay/cs_live_synthetic", sandboxCheckoutOrigin + "/c/pay/cs_test_", sandboxCheckoutOrigin + "/pay/" + fixtureSession, path + "/", path + "/../other", path + "?prefill_email=synthetic", path + "?", path + "#", path + "#%0aPrivate", path + "#%00Private", path + "#" + strings.Repeat("A", 2049), strings.Replace(path, "/c/pay/", "/c/%70ay/", 1), path + "%2fother", strings.ToUpper(path)} {
		if c.CheckoutURLAllowed(value) {
			t.Fatal("unsafe/noncanonical checkout URL allowed")
		}
	}
	f := newFixture(t)
	makeCreateFixture(f)
	want := path + "#Synthetic%2FFragment=123"
	f.session["url"] = want
	// Private test-only SDK substitution: fixed real API is never called.
	c.sdk = newFixedSDK(syntheticSandboxKey, f.server.URL)
	captureOutput(t, func() {
		for i := 0; i < 3; i++ {
			got, err := c.CreateCheckout(context.Background(), createInput(f))
			if err != nil || got.HostedURL != want {
				t.Fatal("sandbox URL fragment not preserved transiently")
			}
		}
	})
	requests := f.snapshot()
	if len(requests) != 3 {
		t.Fatal("unexpected synthetic SDK dispatch count")
	}
	for _, r := range requests {
		if r.header.Get("Authorization") != "Bearer "+syntheticSandboxKey || r.header.Get("X-Stripe-Client-Telemetry") != "" || !r.close || r.form.Get("success_url") != sandboxConfig().ReturnOrigin+ReturnPath || r.form.Get("cancel_url") != sandboxConfig().ReturnOrigin+ReturnPath {
			t.Fatal("sandbox wire privacy boundary changed")
		}
	}
	offline := offlineClient(t, f.server.URL)
	if offline.CheckoutURLAllowed(f.server.URL+"/checkout/synthetic#private") || offline.CheckoutURLAllowed(want) {
		t.Fatal("sandbox fragment policy leaked into offline")
	}
}

func TestFixedOriginTransportRejectsOutgoingContextBeforeDispatch(t *testing.T) {
	f := newFixture(t)
	tr := &offlineTransport{base: &http.Transport{}, origin: f.server.URL}
	defer tr.base.CloseIdleConnections()
	for _, name := range []string{"Stripe-Account", "Stripe-Context", "stripe-account", "sTrIpE-cOnTeXt"} {
		for _, value := range []string{"", hostile} {
			ctx, cancel, observed := requestContext(context.Background())
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.server.URL, nil)
			req.Header[name] = []string{value}
			_, err := tr.RoundTrip(req)
			cancel()
			if err == nil || observed.constraint != ConstraintContext || observed.status != 0 {
				t.Fatal("outgoing context override not refused before dispatch")
			}
		}
	}
	ctx, cancel, observed := requestContext(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.stripe.com/v1/checkout/sessions", nil)
	if _, err := tr.RoundTrip(req); err == nil || observed.constraint != ConstraintOffline || len(f.snapshot()) != 0 {
		t.Fatal("fixed-origin refusal dispatched")
	}
}

func TestSandboxDefaultTLSRefusesUntrustedLocalPeerWithoutHTTPRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	c := sandboxClient(t)
	b := c.sdk.V1CheckoutSessions.B.(*stripe.BackendImplementation)
	tr := b.HTTPClient.Transport.(*offlineTransport)
	// Retain production TLS defaults and api.stripe.com hostname; route TCP ONLY
	// to a synthetic local self-signed peer, never DNS or the actual API.
	tr.base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "api.stripe.com:443" {
			return nil, errOfflineTransport
		}
		return (&net.Dialer{Timeout: requestTimeout}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	f := newFixture(t)
	captureOutput(t, func() {
		_, err := c.CreateCheckout(context.Background(), createInput(f))
		requireFailure(t, err, StageCreate, ConstraintTransport, OutcomeCreateUnknown)
	})
	if calls.Load() != 0 {
		t.Fatal("untrusted TLS peer received HTTP credentials")
	}
}

func TestSandboxWebhookKeepsSignatureToleranceAndExactContext(t *testing.T) {
	c := sandboxClient(t)
	for _, change := range []func(object){nil, func(e object) { e["livemode"] = true }, func(e object) { e["account"] = hostile }, func(e object) { e["context"] = hostile }, func(e object) { e["api_version"] = "2026-10-31.endive" }} {
		e := eventFixture()
		if change != nil {
			change(e)
		}
		raw, _ := json.Marshal(e)
		got, err := c.DecodeWebhook(raw, signFixture(raw, syntheticSandboxSecret, time.Now()))
		if change != nil {
			requireFailure(t, err, StageWebhook, ConstraintContext, OutcomeWebhookRejected)
		} else if err != nil || got.Kind != EventCheckoutCompleted {
			t.Fatal("sandbox signing secret not used")
		}
	}
	raw, _ := json.Marshal(eventFixture())
	for _, signature := range []string{signFixture(raw, FixtureWebhookSecret, time.Now()), signFixture(raw, syntheticSandboxSecret, time.Now().Add(-6*time.Minute))} {
		_, err := c.DecodeWebhook(raw, signature)
		requireFailure(t, err, StageWebhook, ConstraintSignature, OutcomeWebhookRejected)
	}
}
