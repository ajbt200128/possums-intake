package website

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"possums-intake/internal/ledger"
)

type browserTopup struct {
	origin string
	starts atomic.Int64
}

func (b *browserTopup) CheckoutOrigin() string             { return b.origin }
func (b *browserTopup) CheckoutURLAllowed(raw string) bool { return raw == b.origin+"/checkout" }
func (b *browserTopup) StartTopup(context.Context, string, ledger.PaymentForm, string) (TopupResult, error) {
	b.starts.Add(1)
	return TopupResult{Reference: strings.Repeat("a", 64), URL: b.origin + "/checkout", Status: "awaiting_payment"}, nil
}
func (b *browserTopup) TopupStatus(context.Context, string, string) (TopupResult, error) {
	return TopupResult{Status: "awaiting_payment"}, nil
}

// Qualifies actual no-JS form/CSP navigation only. The independent payments
// package tests own real SDK/ledger funding; this browser fixture never charges.
func TestTopupBrowserNavigation(t *testing.T) {
	if os.Getenv("INTAKE_TOPUP_BROWSER") != "synthetic-only" {
		t.Skip("explicit isolated browser qualification not selected")
	}
	f := newAccountFixture(t)
	if _, err := f.service.Credit(context.Background(), ledger.CreditInput{AccountID: accountHTTPA, Amount: "5000000", IdempotencyKey: accountingKey(1)}); err != nil {
		t.Fatal("TOPUP_BROWSER_SYNTHETIC_CREDIT")
	}
	// Distinct loopback hosts model the processor boundary without sharing
	// account cookies. IPv4 provider sources are supported by browser CSP.
	account := httptest.NewUnstartedServer(f.host)
	_ = account.Listener.Close()
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Fatal("TOPUP_BROWSER_LOOPBACK_UNAVAILABLE")
	}
	account.Listener = listener
	accountOrigin := "https://" + listener.Addr().String()
	t.Cleanup(account.Close)
	var visits atomic.Int64
	var leaked atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/checkout" {
			http.NotFound(w, r)
			return
		}
		visits.Add(1)
		if r.Header.Get("Cookie") != "" || r.Header.Get("Referer") != "" {
			leaked.Store(true)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<a href="` + accountOrigin + `/topup/return">Return</a>`))
	}))
	t.Cleanup(provider.Close)
	fixture := &browserTopup{origin: provider.URL}
	f.host.payments = fixture
	// Publish immutable host capabilities before starting this serving goroutine.
	account.StartTLS()
	spki := sha256.Sum256(account.Certificate().RawSubjectPublicKeyInfo)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "topup-browser-test.mjs")
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "TOPUP_ACCOUNT_ORIGIN=" + account.URL, "TOPUP_ACCOUNT_SPKI=" + base64.StdEncoding.EncodeToString(spki[:]), "TOPUP_CHECKOUT_ORIGIN=" + provider.URL}
	output, err := command.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "TOPUP_BROWSER_OK" {
		stage := "completion_unknown"
		for _, allowed := range []string{"configuration", "tooling", "login", "login_refusal", "login_notice", "login_confirm", "account", "form", "checkout_navigation", "return", "privacy", "cleanup"} {
			if strings.TrimSpace(string(output)) == "TOPUP_BROWSER_FAIL "+allowed {
				stage = allowed
			}
		}
		t.Fatalf("TOPUP_BROWSER_UNCONFIRMED stage=%s; raw output withheld", stage)
	}
	if fixture.starts.Load() != 1 || visits.Load() != 1 || leaked.Load() {
		t.Fatal("TOPUP_BROWSER_PRIVACY_OR_EFFECT_COUNT")
	}
}
