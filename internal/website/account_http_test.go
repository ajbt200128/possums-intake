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

const accountHTTPA = "websiteaccounta"
const accountHTTPB = "websiteaccountb"
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
		key := []string{keyHTTPA, keyHTTPB}[i]
		lookup := sha256.Sum256([]byte("possums-intake/customer-key/lookup/v1\x00" + key))
		password := sha256.Sum256([]byte("possums-intake/customer-key/password/v1\x00" + key))
		record := core.NewRecord(collection)
		record.Set("key_lookup", "v1_"+hex.EncodeToString(lookup[:]))
		record.Id = account
		record.Set("balance", "0")
		record.Set("enabled", true)
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
	for _, field := range []string{"Balance: $18446744073709.551615 USD", "Available: $18446744073709.551597 USD", "Pending holds:\n", "Reserved: $0.000007 USD", "Reserved: $0.000011 USD", "Timeout: 60 seconds; state: active", "Charged: none; completed: none; retained until: none"} {
		if !strings.Contains(decoded, field) {
			t.Fatal("SITE_TEST_ACCOUNT_HTTP_FIELD")
		}
	}
	if strings.Count(decoded, "Reservation: ") != 2 || strings.Count(decoded, "Request key: ") != 2 || strings.Count(decoded, "Balance: ") != 1 || strings.Count(decoded, "Available: ") != 1 {
		t.Fatal("SITE_TEST_ACCOUNT_HTTP_HOLD_COUNT")
	}
	for _, forbidden := range []string{accountHTTPA, strconv.QuoteToASCII(accountHTTPA), "SYNTHETIC_A", "Enabled", "microunits", "18446744073709551615", "<details", "Account details"} {
		if strings.Contains(decoded, forbidden) {
			t.Fatal("SITE_TEST_ACCOUNT_HTTP_INTERNAL_FIELD")
		}
	}
	snapshot, err := f.service.Balance(ctx, accountHTTPA)
	if err != nil || len(snapshot.PendingReservations) != 2 {
		t.Fatal("SITE_TEST_ACCOUNT_HTTP_AUTHORITATIVE_ROWS")
	}
	for _, row := range snapshot.PendingReservations {
		for _, field := range []string{"Reservation: " + strconv.QuoteToASCII(row.ReservationID), "Request key: " + strconv.QuoteToASCII(row.IdempotencyKey), "expires: " + time.UnixMilli(int64(row.ExpiresAt)).UTC().Format(time.RFC3339Nano), "initial expiry: " + time.UnixMilli(int64(row.InitialExpiresAt)).UTC().Format(time.RFC3339Nano)} {
			if !strings.Contains(decoded, field) {
				t.Fatal("SITE_TEST_ACCOUNT_HTTP_COMPLETE_ROW")
			}
		}
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
	if !strings.Contains(body, "Balance: $0.00 USD") || !strings.Contains(body, "Available: $0.00 USD") || !strings.Contains(body, "Pending holds:\n(none)") || strings.Contains(body, "$18446744073709.551615") || strings.Contains(body, "Reservation: ") || strings.Contains(body, "SYNTHETIC_") {
		t.Fatal("SITE_TEST_ACCOUNT_VIEW_LEAK")
	}
	// There is no binding to reassign. Deleting the same auth/wallet row
	// invalidates B before a financial read and cannot switch B's session to A.
	if f.credential.Collection().Fields.GetByName("account_id") != nil {
		t.Fatal("SITE_TEST_SECOND_IDENTITY")
	}
	record, err := f.sdk.FindRecordById(ledger.AccountCollectionName, accountHTTPB)
	if err != nil || f.sdk.Delete(record) != nil {
		t.Fatal("SITE_TEST_NATIVE_ACCOUNT_DELETE")
	}
	before := f.counter.calls.Load()
	response, body = other.request(t, "GET", "/home", nil, "")
	if response.StatusCode != 303 || f.counter.calls.Load() != before || strings.Contains(body, "Balance: ") {
		t.Fatal("SITE_TEST_DELETED_SESSION_ACCOUNT_READ")
	}
	if accountLogin(t, &other, keyHTTPB) == 303 {
		t.Fatal("SITE_TEST_MISSING_ACCOUNT_LOGIN_GRANTED")
	}
	if _, err = f.service.Balance(ctx, accountHTTPB); err == nil {
		t.Fatal("SITE_TEST_LOGIN_RECREATED_ACCOUNT")
	}
	response, body = f.request(t, "GET", "/home", nil, "")
	if response.StatusCode != 200 || !strings.Contains(body, "$18446744073709.551615") {
		t.Fatal("SITE_TEST_OTHER_ACCOUNT_CHANGED")
	}
}
