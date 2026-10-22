package stripepay

import (
	"strings"

	"github.com/stripe/stripe-go/v87"
)

const (
	sandboxAPIOrigin      = "https://api.stripe.com"
	sandboxCheckoutOrigin = "https://checkout.stripe.com"
	maxSecretBytes        = 256
)

// SandboxConfig has no endpoint/transport override. ReturnOrigin is exactly
// https://127.0.0.1:<canonical explicit port>, for a later local website only.
// Credentials are transient private inputs, never environment lookups here.
// Shape does not prove sandbox identity, permissions or provider compatibility.
type SandboxConfig struct {
	ReturnOrigin  string
	APIKey        string
	WebhookSecret string
}

// NewSandbox constructs an inert-until-called, test-key-only SDK client. It does
// no network work and never changes ordinary serving or offline fixture policy.
func NewSandbox(cfg SandboxConfig) (client *Client, err error) {
	defer recoverBoundary(&err, StageSandboxConfiguration, OutcomeNotAttempted)
	origin, ok := loopbackOrigin(cfg.ReturnOrigin)
	if !ok || origin.Scheme != "https" || origin.Hostname() != "127.0.0.1" || origin.String() != cfg.ReturnOrigin ||
		!(boundedSecret(cfg.APIKey, "sk_test_") || boundedSecret(cfg.APIKey, "rk_test_")) ||
		!boundedSecret(cfg.WebhookSecret, "whsec_") || stripe.APIVersion != APIVersion {
		return nil, failure(CodeSandboxConfiguration, StageSandboxConfiguration, ConstraintSandbox, OutcomeNotAttempted, 0)
	}
	return &Client{
		sdk: newFixedSDK(cfg.APIKey, sandboxAPIOrigin), origin: sandboxCheckoutOrigin,
		returnURL: origin.String() + ReturnPath, webhookSecret: cfg.WebhookSecret, sandbox: true,
	}, nil
}

// IsSandbox is constructor-owned mode evidence, not proof of remote identity.
// Nil/zero and offline clients return false without performing any work.
func (c *Client) IsSandbox() bool { return c.configured() && c.sandbox }

// Alphanumeric suffixes exclude public underscore-separated offline placeholders
// as well as controls/non-ASCII. The bound includes the credential prefix.
func boundedSecret(value, prefix string) bool {
	if len(value) > maxSecretBytes || !strings.HasPrefix(value, prefix) || len(value) == len(prefix) {
		return false
	}
	for _, b := range []byte(value[len(prefix):]) {
		if !('a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9') {
			return false
		}
	}
	return true
}
