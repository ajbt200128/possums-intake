package stripepay

import (
	"net/netip"
	"net/url"
	"regexp"
	"strings"

	"github.com/stripe/stripe-go/v87"
)

var (
	liveSessionPattern = regexp.MustCompile(`^cs_live_[A-Za-z0-9_]{1,192}$`)
	taxCodePattern     = regexp.MustCompile(`^txcd_[0-9]{8}$`)
)

// LiveConfig is trusted caller configuration, never browser input. Shape checks
// do not prove remote key mode/permissions, product classification or tax setup.
// No endpoint override, environment lookup or constructor network work exists.
type LiveConfig struct {
	ReturnOrigin  string
	APIKey        string
	WebhookSecret string
	TaxCode       string
}

func NewLive(cfg LiveConfig) (client *Client, err error) {
	defer recoverBoundary(&err, StageLiveConfiguration, OutcomeNotAttempted)
	if !publicHTTPSOrigin(cfg.ReturnOrigin) ||
		!(boundedSecret(cfg.APIKey, "sk_live_") || boundedSecret(cfg.APIKey, "rk_live_")) ||
		!boundedSecret(cfg.WebhookSecret, "whsec_") || !taxCodePattern.MatchString(cfg.TaxCode) || stripe.APIVersion != APIVersion {
		return nil, failure(CodeLiveConfiguration, StageLiveConfiguration, ConstraintLive, OutcomeNotAttempted, 0)
	}
	return &Client{sdk: newFixedSDK(cfg.APIKey, sandboxAPIOrigin), origin: sandboxCheckoutOrigin,
		returnURL: cfg.ReturnOrigin + ReturnPath, webhookSecret: cfg.WebhookSecret, live: true, taxCode: cfg.TaxCode}, nil
}

// publicHTTPSOrigin validates spelling only; it never resolves DNS. Public DNS
// names must have at least two canonical lowercase labels and an alphabetic TLD.
// Special-use local/test names are not live return origins; DNS routing and the
// operator's ownership/TLS deployment remain separate qualification boundaries.
func publicHTTPSOrigin(value string) bool {
	if len(value) > 261 {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.String() != value || value != "https://"+u.Host || u.Host != u.Hostname() {
		return false
	}
	host := u.Host
	if len(host) > 253 || strings.ToLower(host) != host {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, b := range []byte(label) {
			if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-') {
				return false
			}
		}
	}
	tld := labels[len(labels)-1]
	if len(tld) < 2 {
		return false
	}
	for _, b := range []byte(tld) {
		if b < 'a' || b > 'z' {
			return false
		}
	}
	switch tld {
	case "localhost", "local", "internal", "invalid", "test", "example", "onion":
		return false
	}
	return host != "home.arpa" && !strings.HasSuffix(host, ".home.arpa")
}

// IsLive and ProductTaxCode expose constructor-owned configuration only, not
// remote identity or tax/legal qualification. Neither accessor performs work.
func (c *Client) IsLive() bool { return c.configured() && c.live }
func (c *Client) ProductTaxCode() string {
	if !c.IsLive() {
		return ""
	}
	return c.taxCode
}

func (c *Client) validSessionID(id string) bool {
	if !c.configured() {
		return false
	}
	if c.live {
		return liveSessionPattern.MatchString(id)
	}
	return sessionPattern.MatchString(id)
}

func (c *Client) validPurchase(p Purchase) bool {
	return validPurchase(p) && ((!c.live && p.TaxCode == "") || (c.live && taxCodePattern.MatchString(p.TaxCode)))
}

// Retain legacy fixture diagnostics unchanged, without misleading test/offline
// labels on live failures. No exception or private input is retained.
func (c *Client) failure(code Code, stage Stage, constraint Constraint, outcome Outcome, status int) *Error {
	if c != nil && c.live {
		switch constraint {
		case ConstraintContext:
			constraint = ConstraintLiveContext
		case ConstraintOffline:
			constraint = ConstraintLive
			if code == CodeConfiguration {
				code = CodeLiveConfiguration
			}
		}
	}
	e := failure(code, stage, constraint, outcome, status)
	if c != nil && c.live {
		if e.NextStep == NextFixFixture {
			e.NextStep = NextFixLive
		}
		if e.NextStep == NextWebhook {
			e.NextStep = NextWebhookLive
		}
	}
	return e
}

func (c *Client) providerFailure(err error, o *observation, stage Stage, outcome Outcome) error {
	e := providerFailure(err, o, stage, outcome)
	if e == nil {
		return nil
	}
	d := e.(*Error)
	return c.failure(d.Code, d.Stage, d.Constraint, d.Outcome, d.HTTPStatus)
}
