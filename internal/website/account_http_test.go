package website

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/ledger"
	"possums-intake/internal/siteauth"
)

const accountHTTPA = "SYNTHETIC_A\n\t\x00雪\u202e<script>"
const accountHTTPB = "SYNTHETIC_B"
const keyHTTPA = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
const keyHTTPB = "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"

type balanceCounter struct {
	BalanceReader
	calls atomic.Int64
}

func (c *balanceCounter) Balance(ctx context.Context, id string) (ledger.BalanceSnapshot, error) {
	c.calls.Add(1)
	return c.BalanceReader.Balance(ctx, id)
}

type accountFixture struct {
	*browser
	service    *ledger.Service
	sdk        core.App
	credential *core.Record
	counter    *balanceCounter
	observer   *numericObserver
}

func newAccountFixture(t *testing.T) *accountFixture {
	t.Helper()
	ctx, dir := context.Background(), t.TempDir()
	if ledger.Init(ctx, dir) != nil {
		t.Fatal("SITE_TEST_ACCOUNT_INIT")
	}
	service, err := ledger.Open(ctx, dir)
	if err != nil {
		t.Fatal("SITE_TEST_ACCOUNT_OPEN")
	}
	t.Cleanup(func() {
		if service.Close() != nil {
			t.Error("SITE_TEST_ACCOUNT_CLOSE")
		}
	})
	app, err := service.CustomerAuthApp()
	if err != nil || siteauth.InitializeCustomerAuth(app) != nil {
		t.Fatal("SITE_TEST_ACCOUNT_SCHEMA")
	}
	collection, err := app.FindCollectionByNameOrId(siteauth.CustomerCollection)
	if err != nil {
		t.Fatal("SITE_TEST_ACCOUNT_COLLECTION")
	}
	var credential *core.Record
	for i, account := range []string{accountHTTPA, accountHTTPB} {
		if _, err := service.Provision(ctx, account); err != nil {
			t.Fatal("SITE_TEST_ACCOUNT_PROVISION")
		}
		key := []string{keyHTTPA, keyHTTPB}[i]
		lookup := sha256.Sum256([]byte("possums-intake/customer-key/lookup/v1\x00" + key))
		password := sha256.Sum256([]byte("possums-intake/customer-key/password/v1\x00" + key))
		record := core.NewRecord(collection)
		record.Set("key_lookup", "v1_"+hex.EncodeToString(lookup[:]))
		record.Set("account_id", account)
		record.SetPassword(base64.RawURLEncoding.EncodeToString(password[:]))
		if app.Save(record) != nil {
			t.Fatal("SITE_TEST_ACCOUNT_CREDENTIAL")
		}
		if i == 0 {
			credential = record
		}
	}
	auth, err := siteauth.NewCustomer(app)
	if err != nil {
		t.Fatal("SITE_TEST_ACCOUNT_AUTH")
	}
	counter := &balanceCounter{BalanceReader: service}
	observer := &numericObserver{}
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(418) })
	host, err := NewFixture(prepared(t), auth, fallback, Config{Lifetime: time.Hour, IdleTimeout: 10 * time.Minute, ConcurrentRequests: 16, Balances: counter, FailureObserver: observer})
	if err != nil {
		t.Fatal("SITE_TEST_ACCOUNT_HOST")
	}
	server := httptest.NewTLSServer(host)
	t.Cleanup(server.Close)
	client := server.Client()
	client.Jar, _ = cookiejar.New(nil)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &accountFixture{browser: &browser{client: client, server: server, host: host}, service: service, sdk: app, credential: credential, counter: counter, observer: observer}
}
func accountLogin(t *testing.T, b *browser, key string) int {
	t.Helper()
	form := b.form(t, "/login")
	form.Set("key", key)
	response, body := b.request(t, "POST", "/login", form, b.server.URL)
	if strings.Contains(body, key) {
		t.Fatal("SITE_TEST_ACCOUNT_KEY_DISCLOSURE")
	}
	return response.StatusCode
}
func accountingKey(n int) string { return fmt.Sprintf("v1.%013d.%032x", time.Now().UnixMilli(), n) }

func TestAccountSDKHTTPCompleteIsolationAndBinding(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	if _, err := f.service.Credit(ctx, ledger.CreditInput{AccountID: accountHTTPA, Amount: "18446744073709551615", IdempotencyKey: accountingKey(1)}); err != nil {
		t.Fatal("SITE_TEST_ACCOUNT_CREDIT")
	}
	for i, maximum := range []ledger.Money{"7", "11"} {
		if _, err := f.service.Reserve(ctx, ledger.ReserveInput{AccountID: accountHTTPA, Maximum: maximum, TimeoutSeconds: 60, IdempotencyKey: accountingKey(i + 2)}); err != nil {
			t.Fatal("SITE_TEST_ACCOUNT_RESERVE")
		}
	}
	for _, path := range []string{"/", "/login", "/home"} {
		f.request(t, "GET", path, nil, "")
	}
	if f.counter.calls.Load() != 0 {
		t.Fatal("SITE_TEST_ANONYMOUS_ACCOUNT_READ")
	}
	if accountLogin(t, f.browser, keyHTTPA) != 303 || f.counter.calls.Load() != 1 {
		t.Fatal("SITE_TEST_BOUND_ACCOUNT_LOGIN_READ")
	}
	response, body := f.request(t, "GET", "/home", nil, "")
	decoded := html.UnescapeString(body)
	if response.StatusCode != 200 || f.counter.calls.Load() != 2 || strings.Contains(body, "<script>") || strings.Contains(decoded, accountHTTPB) {
		t.Fatal("SITE_TEST_ACCOUNT_HTTP_SCOPE")
	}
	for _, field := range []string{strconv.QuoteToASCII(accountHTTPA), "Balance (microunits): 18446744073709551615", "Available (microunits): 18446744073709551597", "Enabled: true", "maximum=7", "maximum=11", "timeout_seconds=60", "expires_at=", `state="active"`, "charged_amount=null", "terminal_at=null", "retain_until=null"} {
		if !strings.Contains(decoded, field) {
			t.Fatal("SITE_TEST_ACCOUNT_HTTP_FIELD")
		}
	}
	if strings.Count(decoded, "reservation_id=") != 2 {
		t.Fatal("SITE_TEST_ACCOUNT_HTTP_HOLD_COUNT")
	}
	// A second cookie jar shares the revision/service, never A's private view.
	other := *f.browser
	otherClient := *f.server.Client()
	other.client = &otherClient
	other.client.Jar, _ = cookiejar.New(nil)
	other.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if accountLogin(t, &other, keyHTTPB) != 303 {
		t.Fatal("SITE_TEST_SECOND_ACCOUNT_LOGIN")
	}
	_, body = other.request(t, "GET", "/home", nil, "")
	if !strings.Contains(body, "Available (microunits): 0") || strings.Contains(body, "18446744073709551615") || strings.Contains(body, "reservation_id=") || strings.Contains(body, "SYNTHETIC_A") {
		t.Fatal("SITE_TEST_ACCOUNT_VIEW_LEAK")
	}
	// Reassignment cannot silently switch an already signed-in browser to B.
	f.credential.Set("account_id", accountHTTPB)
	if f.sdk.Save(f.credential) != nil {
		t.Fatal("SITE_TEST_ACCOUNT_REBIND")
	}
	before := f.counter.calls.Load()
	response, _ = f.request(t, "GET", "/home", nil, "")
	if response.StatusCode != 303 || f.counter.calls.Load() != before {
		t.Fatal("SITE_TEST_REBOUND_SESSION_ACCOUNT_READ")
	}
	if accountLogin(t, f.browser, keyHTTPA) != 303 {
		t.Fatal("SITE_TEST_REBOUND_FRESH_LOGIN")
	}
	_, body = f.request(t, "GET", "/home", nil, "")
	if strings.Contains(body, "SYNTHETIC_A") || !strings.Contains(body, "Available (microunits): 0") {
		t.Fatal("SITE_TEST_REBOUND_ACCOUNT_VIEW")
	}
	// Trusted native deletion is permitted; no account/session initialization is recovery.
	record, err := f.sdk.FindFirstRecordByData("ledger_accounts", "account_id", accountHTTPB)
	if err != nil || f.sdk.Delete(record) != nil {
		t.Fatal("SITE_TEST_NATIVE_ACCOUNT_DELETE")
	}
	failuresBefore := len(f.observer.counts())
	before = f.counter.calls.Load()
	response, body = f.request(t, "GET", "/home", nil, "")
	if response.StatusCode != 503 || f.counter.calls.Load() != before+1 || strings.Contains(body, accountHTTPB) || strings.Contains(body, "Balance (microunits)") || len(f.observer.counts()) != failuresBefore+1 {
		t.Fatal("SITE_TEST_ACCOUNT_MISSING_ONCE_ONLY")
	}
	if accountLogin(t, f.browser, keyHTTPA) != 503 {
		t.Fatal("SITE_TEST_MISSING_ACCOUNT_LOGIN_GRANTED")
	}
	_, err = f.service.Balance(ctx, accountHTTPB)
	if err == nil {
		t.Fatal("SITE_TEST_LOGIN_RECREATED_ACCOUNT")
	}
}
