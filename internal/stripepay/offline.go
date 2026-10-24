package stripepay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v87"
)

const requestTimeout = 5 * time.Second

var (
	tokenPattern        = regexp.MustCompile(`^[0-9a-f]{64}$`)
	sessionPattern      = regexp.MustCompile(`^cs_test_[A-Za-z0-9_]{1,192}$`)
	intentPattern       = regexp.MustCompile(`^pi_[A-Za-z0-9_]{1,200}$`)
	chargePattern       = regexp.MustCompile(`^ch_[A-Za-z0-9_]{1,200}$`)
	errOfflineTransport = errors.New("offline transport refused")
)

// Client reaches only its constructor's fixed API origin.
// The Stripe client/backends are private so callers cannot enable production
// defaults, change the logger, override context, or dispatch extra operations.
type Client struct {
	sdk           *stripe.Client
	origin        string
	returnURL     string
	webhookSecret string
	sandbox       bool
	offline       bool
	live          bool
	taxCode       string
}

// NewOffline never reads environment credentials and never constructs a default
// Stripe backend. All four SDK backends receive explicit privacy settings even
// though only API is used. The zero Client is inert, not a production fallback.
func NewOffline(cfg OfflineConfig) (client *Client, err error) {
	defer recoverBoundary(&err, StageConfiguration, OutcomeNotAttempted)
	endpoint, ok := loopbackOrigin(cfg.Endpoint)
	if !ok || endpoint.Scheme != "http" {
		return nil, failure(CodeConfiguration, StageConfiguration, ConstraintOffline, OutcomeNotAttempted, 0)
	}
	origin, ok := loopbackOrigin(cfg.ReturnOrigin)
	if !ok || cfg.APIKey != FixtureAPIKey || cfg.WebhookSecret != FixtureWebhookSecret || stripe.APIVersion != APIVersion {
		return nil, failure(CodeConfiguration, StageConfiguration, ConstraintOffline, OutcomeNotAttempted, 0)
	}
	return &Client{sdk: newFixedSDK(cfg.APIKey, endpoint.String()), origin: endpoint.String(), returnURL: origin.String() + ReturnPath, webhookSecret: cfg.WebhookSecret, offline: true}, nil
}

func newFixedSDK(apiKey, endpoint string) *stripe.Client {
	// Do not inherit DefaultTransport or proxy environment settings. Disabling
	// connection reuse removes Go's eligible reused-connection retry path,
	// separately from the SDK retry setting. HTTP/2 is not enabled here.
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            (&net.Dialer{Timeout: requestTimeout}).DialContext,
		DisableKeepAlives:      true,
		ResponseHeaderTimeout:  requestTimeout,
		MaxResponseHeaderBytes: 32 << 10,
	}
	httpClient := &http.Client{
		Timeout:       requestTimeout,
		Transport:     &offlineTransport{base: transport, origin: endpoint},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	backend := func(kind stripe.SupportedBackend) stripe.Backend {
		return stripe.GetBackendWithConfig(kind, &stripe.BackendConfig{
			URL:               stripe.String(endpoint),
			HTTPClient:        httpClient,
			EnableTelemetry:   stripe.Bool(false),
			MaxNetworkRetries: stripe.Int64(0),
			LeveledLogger:     &stripe.LeveledLogger{Level: stripe.LevelNull},
		})
	}
	backends := &stripe.Backends{
		API: backend(stripe.APIBackend), Connect: backend(stripe.ConnectBackend),
		Uploads: backend(stripe.UploadsBackend), MeterEvents: backend(stripe.MeterEventsBackend),
	}
	return stripe.NewClient(apiKey, stripe.WithBackends(backends))
}

func loopbackOrigin(value string) (*url.URL, bool) {
	u, err := url.Parse(value)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Path != "" || u.RawPath != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, false
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || !ip.IsLoopback() || ip.Zone() != "" {
		return nil, false
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return nil, false
	}
	// Require the canonical origin, excluding unusual authority spellings.
	if u.Host != net.JoinHostPort(ip.String(), strconv.Itoa(port)) {
		return nil, false
	}
	return u, true
}

func validPurchase(p Purchase) bool {
	return tokenPattern.MatchString(p.Reference) && validAmount(p.GrossCents) &&
		validSeconds(p.IssuedAt.Unix()) && validSeconds(p.ExpiresAt.Unix()) && p.ExpiresAt.Equal(p.IssuedAt.Add(time.Hour).Truncate(time.Second))
}

func validAmount(cents uint64) bool {
	return cents == 500 || cents == 1000 || cents == 2500 || cents == 5000
}
func validSeconds(seconds int64) bool { return seconds > 0 && seconds <= 9999999999 }
func (c *Client) configured() bool {
	if c == nil || c.sdk == nil {
		return false
	}
	switch {
	case c.live && !c.sandbox && !c.offline:
		return c.origin == sandboxCheckoutOrigin && boundedSecret(c.webhookSecret, "whsec_") && taxCodePattern.MatchString(c.taxCode)
	case c.sandbox && !c.live && !c.offline:
		return c.origin == sandboxCheckoutOrigin && boundedSecret(c.webhookSecret, "whsec_") && c.taxCode == ""
	case c.offline && !c.live && !c.sandbox:
		return c.webhookSecret == FixtureWebhookSecret && c.taxCode == ""
	default:
		return false
	}
}

type observationKey struct{}
type observation struct {
	status     int
	constraint Constraint
}

func requestContext(ctx context.Context) (context.Context, context.CancelFunc, *observation) {
	o := &observation{}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	return context.WithValue(ctx, observationKey{}, o), cancel, o
}

// offlineTransport fixes the API origin and bounds bytes before SDK parsing.
// It does not parse/reimplement Stripe JSON. Per-call observation contains only
// status and a closed constraint, never payloads or raw causes; no shared history.
type offlineTransport struct {
	base   *http.Transport
	origin string
}

func (t *offlineTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	o, ok := req.Context().Value(observationKey{}).(*observation)
	if !ok {
		return nil, errOfflineTransport
	}
	if req.URL.Scheme+"://"+req.URL.Host != t.origin || req.URL.User != nil || req.URL.Fragment != "" {
		o.constraint = ConstraintOffline
		return nil, errOfflineTransport
	}
	for name := range req.Header {
		if strings.EqualFold(name, "Stripe-Account") || strings.EqualFold(name, "Stripe-Context") {
			o.constraint = ConstraintContext
			return nil, errOfflineTransport
		}
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		o.constraint = ConstraintTransport
		return nil, errOfflineTransport
	}
	o.status = resp.StatusCode
	if resp.Header.Get("Stripe-Account") != "" || resp.Header.Get("Stripe-Context") != "" ||
		(resp.Header.Get("Stripe-Version") != "" && resp.Header.Get("Stripe-Version") != APIVersion) {
		o.constraint = ConstraintContext
		_ = resp.Body.Close()
		return nil, errOfflineTransport
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	_ = resp.Body.Close()
	if len(body) > MaxResponseBytes {
		o.constraint = ConstraintResponseSize
		return nil, errOfflineTransport
	}
	if readErr != nil {
		o.constraint = ConstraintTransport
		return nil, errOfflineTransport
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

func providerFailure(err error, o *observation, stage Stage, outcome Outcome) error {
	if o.constraint != "" {
		return failure(CodeProvider, stage, o.constraint, outcome, o.status)
	}
	if o.status == 0 {
		return failure(CodeProvider, stage, ConstraintTransport, outcome, 0)
	}
	if o.status >= 300 && o.status < 400 {
		return failure(CodeProvider, stage, ConstraintRedirect, outcome, o.status)
	}
	if o.status < 200 || o.status >= 300 {
		return failure(CodeProvider, stage, ConstraintStatus, outcome, o.status)
	}
	if err != nil {
		return failure(CodeDecode, stage, ConstraintDecode, outcome, o.status)
	}
	return nil
}
