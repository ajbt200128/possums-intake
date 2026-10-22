package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
	"possums-intake/internal/payments"
	"possums-intake/internal/stripepay"
	"possums-intake/internal/website"
)

// Authored synthetic shapes only. Never inspect inherited Stripe inputs.
const sandboxLocalTestKey = "sk_test_SyntheticRuntimeNotACredential123"
const sandboxLocalTestSecret = "whsec_SyntheticRuntimeNotACredential123"

func sandboxLocalTestInput(key, webhook string) func(string) string {
	return func(name string) string {
		switch name {
		case "STRIPE_API_KEY":
			return key
		case "STRIPE_WEBHOOK_SECRET":
			return webhook
		default:
			panic("SYNTHETIC_UNEXPECTED_SECRET_LOOKUP")
		}
	}
}

func TestStripeSandboxLocalPreStateRefusals(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	for _, args := range [][]string{
		{"--state=/SYNTHETIC_PRIVATE_SENTINEL"}, {"--operator"}, {"--tinfoil-volume=ledger"}, {"--initialize-volume"},
		{"--api-key=SYNTHETIC_PRIVATE_SENTINEL"}, {"--webhook-secret=SYNTHETIC_PRIVATE_SENTINEL"}, {"--provider=https://example.invalid"},
		{"extra"}, {"--listen=0.0.0.0:8443"}, {"--listen=localhost:8443"}, {"--listen=[::1]:8443"}, {"--listen=127.0.0.2:8443"},
		{"--listen=[127.0.0.1]:8443"}, {"--listen=127.0.0.1:0"}, {"--listen=127.0.0.1:08443"}, {"--listen=127.0.0.1:+8443"}, {"--listen=127.0.0.1:65536"},
		{"--webhook-listen=127.0.0.1:8443"}, {"--webhook-listen=localhost:8844"}, {"--webhook-listen=[::1]:8844"},
		{"--webhook-listen=127.0.0.1:0"}, {"--webhook-listen=127.0.0.1:08844"}, {"--webhook-listen=192.0.2.1:8844"},
	} {
		err := executeStripeSandboxLocalInputs(context.Background(), args, nil, func(string) string { t.Fatal("SANDBOX_TEST_OPTIONS_READ_SECRET"); return "" }, func() bool { return false })
		if err == nil || ledger.Failure(err).Code != codeSyntheticOptions {
			t.Fatal("SANDBOX_TEST_OPTION_ACCEPTED")
		}
		assertClosedRuntime(t, err)
	}
	for _, credentials := range [][2]string{
		{"", sandboxLocalTestSecret}, {sandboxLocalTestKey, ""}, {"sk_live_Synthetic123", sandboxLocalTestSecret},
		{"rk_live_Synthetic123", sandboxLocalTestSecret}, {"sk_test_possums_offline_fixture", sandboxLocalTestSecret},
		{sandboxLocalTestKey, "whsec_possums_offline_fixture"}, {"SYNTHETIC_PRIVATE_SENTINEL", sandboxLocalTestSecret},
		{sandboxLocalTestKey, "SYNTHETIC_PRIVATE_SENTINEL"},
	} {
		o := &runtimeObserver{}
		err := executeStripeSandboxLocalInputs(context.Background(), nil, o, sandboxLocalTestInput(credentials[0], credentials[1]), func() bool { return false })
		d := ledger.Failure(err)
		if err == nil || d.Code != ledger.Code(stripepay.CodeSandboxConfiguration) || d.Stage != string(stripepay.StageSandboxConfiguration) || d.Constraint != string(stripepay.ConstraintSandbox) || d.Outcome != string(stripepay.OutcomeNotAttempted) {
			t.Fatal("SANDBOX_TEST_CONFIGURATION_TYPE_LOST")
		}
		o.want(t, failuretype.RequestInvalid)
		encoded, e := json.Marshal(d)
		if e != nil || strings.Contains(string(encoded), sandboxLocalTestKey) || strings.Contains(string(encoded), sandboxLocalTestSecret) {
			t.Fatal("SANDBOX_TEST_SECRET_DIAGNOSTIC")
		}
		assertClosedRuntime(t, err)
	}
	err := executeStripeSandboxLocalInputs(context.Background(), nil, nil, func(string) string { t.Fatal("SANDBOX_TEST_VOLUME_READ_SECRET"); return "" }, func() bool { return true })
	if err == nil || ledger.Failure(err).Code != codeVolumeOptions {
		t.Fatal("SANDBOX_TEST_VOLUME_ACCEPTED")
	}
	// Exercise explicit dispatch with only authored environment values.
	t.Setenv("STRIPE_API_KEY", "sk_live_Synthetic123")
	t.Setenv("STRIPE_WEBHOOK_SECRET", sandboxLocalTestSecret)
	t.Setenv("LEDGER_VOLUME_KEY_B64", "SYNTHETIC_VOLUME_SENTINEL")
	if err := executeContext(context.Background(), []string{"stripe-sandbox-local"}); err == nil || ledger.Failure(err).Code != codeVolumeOptions {
		t.Fatal("SANDBOX_TEST_DISPATCH")
	}
	entries, e := os.ReadDir(tmp)
	if e != nil || len(entries) != 0 {
		t.Fatal("SANDBOX_TEST_REFUSAL_TOUCHED_STATE")
	}
}

func TestStripeSandboxLocalDefaultsAndCancelledAdmission(t *testing.T) {
	o, err := parseSandboxLocalOptions(nil)
	if err != nil || o.website != "127.0.0.1:8443" || o.webhook != "127.0.0.1:8844" {
		t.Fatal("SANDBOX_TEST_DEFAULTS")
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if executeStripeSandboxLocalInputs(ctx, nil, nil, sandboxLocalTestInput(sandboxLocalTestKey, sandboxLocalTestSecret), func() bool { return false }) != nil {
		t.Fatal("SANDBOX_TEST_CANCELLED_ADMISSION")
	}
	entries, e := os.ReadDir(tmp)
	if e != nil || len(entries) != 0 {
		t.Fatal("SANDBOX_TEST_CANCELLED_STATE")
	}
}

func TestStripeSandboxLocalSecondBindFailureJoinsSetup(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("SANDBOX_TEST_LISTENER")
	}
	defer occupied.Close()
	address := syntheticAddress(t)
	o := &runtimeObserver{}
	err = executeStripeSandboxLocalInputs(context.Background(), []string{"--listen", address, "--webhook-listen", occupied.Addr().String()}, o, sandboxLocalTestInput(sandboxLocalTestKey, sandboxLocalTestSecret), func() bool { return false })
	if err == nil || ledger.Failure(err).Code != codeBind {
		t.Fatal("SANDBOX_TEST_SECOND_BIND_FAILURE")
	}
	o.want(t, failuretype.RuntimeServeUnknown)
	assertClosedRuntime(t, err)
	rebound, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("SANDBOX_TEST_FIRST_LISTENER_NOT_CLOSED")
	}
	rebound.Close()
	entries, e := os.ReadDir(tmp)
	if e != nil || len(entries) != 0 {
		t.Fatal("SANDBOX_TEST_SETUP_NOT_DISPOSED")
	}
}

// No true provider dispatch is reachable through the positive website fixture.
// Any unexpected action is a closed local failure recorded by this spy.
type sandboxLocalActions struct {
	calls         atomic.Int64
	sandboxOrigin atomic.Bool
}

func (a *sandboxLocalActions) StartTopup(context.Context, string, ledger.PaymentForm, string) (website.TopupResult, error) {
	a.calls.Add(1)
	return website.TopupResult{}, errors.New("SYNTHETIC_PRIVATE_SENTINEL")
}
func (a *sandboxLocalActions) TopupStatus(context.Context, string, string) (website.TopupResult, error) {
	a.calls.Add(1)
	return website.TopupResult{}, errors.New("SYNTHETIC_PRIVATE_SENTINEL")
}
func (*sandboxLocalActions) CheckoutURLAllowed(string) bool { return false }
func (a *sandboxLocalActions) CheckoutOrigin() string {
	if a.sandboxOrigin.Load() {
		return "https://checkout.stripe.com"
	}
	return "http://127.0.0.1:19443"
}

func TestStripeSandboxLocalHTTPSLoginTopupAndDedicatedHTTPWebhook(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	if ledger.Init(ctx, dir) != nil {
		t.Fatal("SANDBOX_TEST_INIT")
	}
	service, err := ledger.Open(ctx, dir)
	if err != nil {
		t.Fatal("SANDBOX_TEST_OPEN")
	}
	defer service.Close()
	if service.InitializeSyntheticPayments(ctx) != nil {
		t.Fatal("SANDBOX_TEST_PAYMENT_SCHEMA")
	}
	actions := &sandboxLocalActions{}
	actions.sandboxOrigin.Store(true)
	host, err := newSandboxLocalWebsite(ctx, service, actions)
	if err != nil || actions.calls.Load() != 0 {
		t.Fatal("SANDBOX_TEST_FIXTURE_ADMISSION")
	}
	fixtureApp, err := service.CustomerAuthApp()
	if err != nil {
		t.Fatal("SANDBOX_TEST_ACCOUNT_APP")
	}
	fixtureRecord, err := fixtureApp.FindFirstRecordByData(localWebsiteCollection, "key_lookup", localWebsiteIdentity)
	if err != nil {
		t.Fatal("SANDBOX_TEST_ACCOUNT_RECORD")
	}
	snapshot, err := service.Balance(ctx, fixtureRecord.Id)
	if err != nil || snapshot.AccountBalance.Balance != "0" {
		t.Fatal("SANDBOX_TEST_ZERO_ACCOUNT")
	}
	tlsConfig, err := localWebsiteTLS()
	if err != nil {
		t.Fatal("SANDBOX_TEST_CERTIFICATE")
	}
	web, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("SANDBOX_TEST_HTTPS_BIND")
	}
	defer web.Close()
	webhook, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("SANDBOX_TEST_WEBHOOK_BIND")
	}
	defer webhook.Close()
	// Construct the real adapter/coordinator without making a request. Only
	// unsigned/invalid signatures reach it, so evidence retrieval is unreachable.
	client, err := stripepay.NewSandbox(stripepay.SandboxConfig{ReturnOrigin: "https://" + web.Addr().String(), APIKey: sandboxLocalTestKey, WebhookSecret: sandboxLocalTestSecret})
	if err != nil {
		t.Fatal("SANDBOX_TEST_SANDBOX_CONSTRUCTOR")
	}
	pay, err := payments.NewSandbox(ctx, service, client)
	if err != nil {
		t.Fatal("SANDBOX_TEST_COORDINATOR_CONSTRUCTOR")
	}
	pair := newSandboxLocalServers(localServer(ctx, host), localServer(ctx, pay.WebhookHandler()), webhook, nil)
	work, stopWork := context.WithCancel(ctx)
	defer stopWork()
	done := make(chan error, 1)
	go func() {
		done <- coordinateServeObserved(ctx, work, stopWork, service, pair, tls.NewListener(web, tlsConfig), nil, time.Now(), nil)
	}()
	defer func() {
		cancel()
		if <-done != nil {
			t.Error("SANDBOX_TEST_JOINED_SHUTDOWN")
		}
	}()
	cert, err := x509.ParseCertificate(tlsConfig.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal("SANDBOX_TEST_PUBLIC_CERTIFICATE")
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	jar, _ := cookiejar.New(nil)
	https := &http.Client{Transport: transport, Jar: jar, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	base := "https://" + web.Addr().String()
	r, _ := websiteRequest(t, https, base, "GET", "/topup", nil)
	if r.StatusCode != 303 {
		t.Fatal("SANDBOX_TEST_TOPUP_ANONYMOUS")
	}
	_, markup := websiteRequest(t, https, base, "GET", "/login", nil)
	form := url.Values{"key": {localWebsiteKey}, "csrf_token": {websiteHidden(t, markup, "csrf_token")}, "revision": {websiteHidden(t, markup, "revision")}}
	withoutCSRF := url.Values{"key": {localWebsiteKey}, "revision": {form.Get("revision")}}
	r, _ = websiteRequest(t, https, base, "POST", "/login", withoutCSRF)
	if r.StatusCode != 403 {
		t.Fatal("SANDBOX_TEST_CSRF_REQUIRED")
	}
	r, _ = websiteRequest(t, https, base, "POST", "/login", form)
	if r.StatusCode != 303 {
		t.Fatal("SANDBOX_TEST_SDK_LOGIN")
	}
	r, markup = websiteRequest(t, https, base, "GET", "/topup", nil)
	if r.StatusCode != 200 || !strings.Contains(markup, "<h1>Add credit</h1>") || !strings.Contains(markup, "Sandbox/test only") || !strings.Contains(markup, "$5.00") || !strings.Contains(markup, `rel="stylesheet"`) || !strings.Contains(r.Header.Get("Content-Security-Policy"), actions.CheckoutOrigin()) {
		t.Fatal("SANDBOX_TEST_TOPUP_PAGE")
	}
	for _, cookie := range r.Cookies() {
		if !cookie.Secure || !cookie.HttpOnly {
			t.Fatal("SANDBOX_TEST_SECURE_COOKIES")
		}
	}
	// Invalid denomination is refused before the action capability. Never
	// submit a valid denomination to the real sandbox service in offline tests.
	form = url.Values{"csrf_token": {websiteHidden(t, markup, "csrf_token")}, "revision": {websiteHidden(t, markup, "revision")}, "attempt": {websiteHidden(t, markup, "attempt")}, "denomination": {"invalid"}}
	r, _ = websiteRequest(t, https, base, "POST", "/topup/start", form)
	if r.StatusCode != 400 || actions.calls.Load() != 0 {
		t.Fatal("SANDBOX_TEST_UNEXPECTED_PAYMENT_ACTION")
	}
	r, _ = websiteRequest(t, https, base, "GET", payments.WebhookPath, nil)
	if r.StatusCode == 200 {
		t.Fatal("SANDBOX_TEST_WEBHOOK_MOUNTED_ON_WEBSITE")
	}
	plainTransport := &http.Transport{Proxy: nil}
	defer plainTransport.CloseIdleConnections()
	plain := &http.Client{Transport: plainTransport, Timeout: 5 * time.Second}
	plainBase := "http://" + webhook.Addr().String()
	for _, test := range []struct {
		method, path, signature string
		status                  int
	}{
		{"GET", "/", "", 404}, {"GET", "/login", "", 404}, {"GET", "/home", "", 404}, {"GET", "/api/collections", "", 404},
		{"GET", "/healthz", "", 404}, {"GET", "/v1/balance", "", 404}, {"GET", payments.WebhookPath, "", 405},
		{"POST", payments.WebhookPath, "", 400}, {"POST", payments.WebhookPath, "SYNTHETIC_PRIVATE_SENTINEL", 400},
		{"POST", payments.WebhookPath + "?selector=synthetic", "", 404},
	} {
		req, _ := http.NewRequest(test.method, plainBase+test.path, strings.NewReader("{}"))
		if test.signature != "" {
			req.Header.Set("Stripe-Signature", test.signature)
		}
		response, e := plain.Do(req)
		if e != nil {
			t.Fatal("SANDBOX_TEST_WEBHOOK_REQUEST")
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 8192))
		response.Body.Close()
		if readErr != nil || response.StatusCode != test.status || len(response.Cookies()) != 0 {
			t.Fatal("SANDBOX_TEST_WEBHOOK_SCOPE")
		}
		for _, secret := range []string{sandboxLocalTestKey, sandboxLocalTestSecret, "SYNTHETIC_PRIVATE_SENTINEL", "goroutine "} {
			if strings.Contains(string(body), secret) {
				t.Fatal("SANDBOX_TEST_WEBHOOK_DIAGNOSTIC_PRIVACY")
			}
		}
	}
	request, _ := http.NewRequest("POST", plainBase+payments.WebhookPath, strings.NewReader(strings.Repeat("x", stripepay.MaxWebhookBytes+1)))
	response, err := plain.Do(request)
	if err != nil {
		t.Fatal("SANDBOX_TEST_OVERSIZED_WEBHOOK")
	}
	response.Body.Close()
	if response.StatusCode != 413 {
		t.Fatal("SANDBOX_TEST_WEBHOOK_BODY_BOUND")
	}
	if actions.calls.Load() != 0 {
		t.Fatal("SANDBOX_TEST_CONSTRUCTOR_OR_PAGE_DISPATCH")
	}
	// The SDK identity never bypasses current token/existence checks.
	app, err := service.CustomerAuthApp()
	if err != nil {
		t.Fatal("SANDBOX_TEST_AUTH_APP")
	}
	record, err := app.FindFirstRecordByData(localWebsiteCollection, "key_lookup", localWebsiteIdentity)
	if err != nil || app.Delete(record) != nil {
		t.Fatal("SANDBOX_TEST_AUTH_DELETE")
	}
	r, _ = websiteRequest(t, https, base, "GET", "/topup", nil)
	if r.StatusCode != 303 {
		t.Fatal("SANDBOX_TEST_SDK_REVALIDATION_BYPASSED")
	}
}

type sandboxLocalCloseSpy struct {
	calls int
	err   error
}

func (s *sandboxLocalCloseSpy) CloseObserved(failureowner.Observer) error { s.calls++; return s.err }

func TestStripeSandboxLocalUnconfirmedTeardownPreservesState(t *testing.T) {
	primary := sandboxLocalBindFailure()
	for _, result := range []error{&unconfirmedHTTPDrain{primary}, runtimeFailure(codeShutdown, "shutdown", "completed_http_drain", "unknown", "Inspect privately.")} {
		scratch := t.TempDir()
		closer := &sandboxLocalCloseSpy{}
		safe, err := closeSandboxLocal(closer, result, nil)
		if safe || err != nil || closer.calls != 0 || cleanupScratch(scratch, safe) == nil {
			t.Fatal("SANDBOX_TEST_UNCONFIRMED_STORAGE_CLOSED")
		}
		if _, err := os.Stat(scratch); err != nil {
			t.Fatal("SANDBOX_TEST_UNCONFIRMED_STATE_REMOVED")
		}
	}
	closer := &sandboxLocalCloseSpy{err: errors.New("SYNTHETIC_PRIVATE_SENTINEL")}
	safe, err := closeSandboxLocal(closer, primary, nil)
	if safe || err == nil || closer.calls != 1 || ledger.Failure(primary).Code != codeBind {
		t.Fatal("SANDBOX_TEST_CLOSE_FAILURE_POLICY")
	}
}

// Source guard complements the old-mode compiled HTTPS test. Neither existing
// constructor supplies the optional payment capability, regardless of env.
func TestStripeSandboxLocalExistingConstructorsDoNotSupplyPayments(t *testing.T) {
	for _, path := range []string{"website.go", "website_local.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal("SANDBOX_TEST_RUNTIME_SOURCE")
		}
		configs := 0
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			typ, ok := literal.Type.(*ast.SelectorExpr)
			if !ok || typ.Sel.Name != "Config" {
				return true
			}
			pkg, ok := typ.X.(*ast.Ident)
			if !ok || pkg.Name != "website" {
				return true
			}
			configs++
			for _, element := range literal.Elts {
				field, ok := element.(*ast.KeyValueExpr)
				if !ok {
					t.Fatal("SANDBOX_TEST_REQUALIFY_RUNTIME_CONFIG")
				}
				name, ok := field.Key.(*ast.Ident)
				if !ok || name.Name == "Payments" {
					t.Fatal("SANDBOX_TEST_EXISTING_MODE_PAYMENT_CAPABILITY")
				}
			}
			return true
		})
		if configs != 1 {
			t.Fatal("SANDBOX_TEST_REQUALIFY_EXISTING_CONSTRUCTOR")
		}
	}
}
