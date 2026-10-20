package website

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
	"possums-intake/internal/siteauth"
)

const hostileFailure = "synthetic-private-password-token-cookie-url-sql-panic"

type numericObserver struct {
	mu    sync.Mutex
	kinds []failuretype.Type
}

func (o *numericObserver) ObserveFailure(k failuretype.Type) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.kinds = append(o.kinds, k)
}
func (o *numericObserver) InvalidateFailures() {}
func (o *numericObserver) counts() []failuretype.Type {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]failuretype.Type(nil), o.kinds...)
}

type faultyStore struct {
	scs.Store
	operation  string
	panicValue bool
	cause      error
}

func (s faultyStore) fail() error {
	if s.panicValue {
		panic(hostileFailure)
	}
	if s.cause != nil {
		return s.cause
	}
	return errors.New(hostileFailure)
}
func (s faultyStore) Find(token string) ([]byte, bool, error) {
	if s.operation == "find" {
		return nil, false, s.fail()
	}
	return s.Store.Find(token)
}
func (s faultyStore) Commit(token string, b []byte, e time.Time) error {
	if s.operation == "commit" {
		return s.fail()
	}
	return s.Store.Commit(token, b, e)
}
func (s faultyStore) Delete(token string) error {
	if s.operation == "delete" {
		return s.fail()
	}
	return s.Store.Delete(token)
}

type faultyAuth struct {
	customerAuth
	validate   bool
	panicValue bool
	cause      error
}

func (a faultyAuth) fail() error {
	if a.panicValue {
		panic(hostileFailure)
	}
	if a.cause != nil {
		return a.cause
	}
	return errors.New(hostileFailure)
}
func (a faultyAuth) Login(context.Context, string) (string, siteauth.Principal, error) {
	return "", siteauth.Principal{}, a.fail()
}
func (a faultyAuth) Validate(ctx context.Context, token string) (siteauth.Principal, error) {
	if a.validate {
		return siteauth.Principal{}, a.fail()
	}
	return a.customerAuth.Validate(ctx, token)
}

func assertClosedHTTP(t *testing.T, response *http.Response, body, code, outcome string) {
	t.Helper()
	if response.StatusCode != 503 || !strings.Contains(body, code) || !strings.Contains(body, outcome) || strings.Contains(body, hostileFailure) || response.Header.Get("Location") != "" || strings.Contains(body, "stage=") == false {
		t.Fatal("SITE_TEST_CLOSED_FAILURE")
	}
}
func TestHostileSessionStoreErrorsPanicsAndCommitContainment(t *testing.T) {
	for _, operation := range []string{"find", "commit", "delete"} {
		for _, panicValue := range []bool{false, true} {
			t.Run(operation+map[bool]string{false: "_error", true: "_panic"}[panicValue], func(t *testing.T) {
				b := newBrowser(t)
				form := b.form(t, "/login")
				path, outcome := "/login", "no_action"
				if operation == "find" {
					b.login(t)
				}
				if operation == "delete" {
					b.login(t)
					_, body := b.request(t, "GET", "/home", nil, "")
					form = url.Values{"csrf_token": {hidden(t, body, "csrf_token")}, "revision": {hidden(t, body, "revision")}}
					path, outcome = "/logout", "logout_completion_unknown"
				} else {
					form.Set("key", testPassword)
				}
				observer := &numericObserver{}
				b.host.observer = observer
				b.host.sessions.Store = faultyStore{Store: b.host.store, operation: operation, panicValue: panicValue}
				if operation == "commit" {
					outcome = "sdk_auth_completed_website_session_unconfirmed"
				}
				var response *http.Response
				var body string
				captured := captureWebsiteOutput(t, func() { response, body = b.request(t, "POST", path, form, b.server.URL) })
				if captured != "" {
					t.Fatal("SITE_TEST_STORE_STDIO_NOT_QUIET")
				}
				assertClosedHTTP(t, response, body, "SITE_SESSION_UNAVAILABLE", outcome)
				if operation == "commit" && (strings.Contains(body, "<!doctype") || len(response.Cookies()) > 0) {
					t.Fatal("SITE_TEST_COMMIT_RESPONSE_SUPPRESSION")
				}
				kinds := observer.counts()
				want := failuretype.NativeWriteUnknown
				stage := "website_session_" + operation
				if operation == "find" {
					want, stage = failuretype.NativeReadUnknown, "website_session_load"
				}
				if operation == "delete" {
					stage = "website_session_delete"
				}
				if !strings.Contains(body, stage) {
					t.Fatal("SITE_TEST_SESSION_STAGE")
				}
				if len(kinds) != 1 || kinds[0] != want {
					t.Fatal("SITE_TEST_SESSION_ONCE_ONLY_OWNER")
				}
			})
		}
	}
}
func TestHostileAuthErrorsAndPanicsRemainPrivate(t *testing.T) {
	for _, validate := range []bool{false, true} {
		for _, panicValue := range []bool{false, true} {
			b := newBrowser(t)
			form := b.form(t, "/login")
			method, path, outcome := "POST", "/login", "sdk_auth_completion_unknown_no_session_granted"
			if validate {
				b.login(t)
				method, path, outcome = "GET", "/home", "session_destroyed_credential_revalidation_refused"
			}
			form.Set("key", testPassword)
			observer := &numericObserver{}
			b.host.observer = observer
			b.host.auth = faultyAuth{customerAuth: b.host.auth, validate: validate, panicValue: panicValue}
			var response *http.Response
			var body string
			captured := captureWebsiteOutput(t, func() {
				if validate {
					response, body = b.request(t, method, path, nil, "")
				} else {
					response, body = b.request(t, method, path, form, b.server.URL)
				}
			})
			if validate && panicValue {
				outcome = "no_action"
			}
			assertClosedHTTP(t, response, body, "SITE_AUTH_UNAVAILABLE", outcome)
			if captured != "" {
				t.Fatal("SITE_TEST_STDIO_NOT_QUIET")
			}
			kinds := observer.counts()
			if len(kinds) != 1 || kinds[0] != failuretype.NativeAuthUnknown {
				t.Fatal("SITE_TEST_AUTH_ONCE_ONLY_OWNER")
			}
		}
	}
}

// Only synthetic fixtures run while process outputs are redirected. Never echo
// captured bytes, even on failure. No test in this package runs in parallel.
func captureWebsiteOutput(t *testing.T, run func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal("SITE_TEST_OUTPUT_PIPE")
	}
	priorOut, priorErr, priorLog := os.Stdout, os.Stderr, log.Writer()
	os.Stdout, os.Stderr = writer, writer
	log.SetOutput(writer)
	result := make(chan string, 1)
	go func() { data, _ := io.ReadAll(io.LimitReader(reader, 65536)); result <- string(data) }()
	defer func() {
		os.Stdout, os.Stderr = priorOut, priorErr
		log.SetOutput(priorLog)
		writer.Close()
		reader.Close()
	}()
	run()
	writer.Close()
	return <-result
}

type failingResponse struct {
	header     http.Header
	panicValue bool
	short      bool
	status     int
}

func (w *failingResponse) Header() http.Header    { return w.header }
func (w *failingResponse) WriteHeader(status int) { w.status = status }
func (w *failingResponse) Write(b []byte) (int, error) {
	if w.panicValue {
		panic(hostileFailure)
	}
	if w.short {
		return 0, nil
	}
	return 0, errors.New(hostileFailure)
}
func TestDeliveryFailuresPreserveAuthAndLogoutEffects(t *testing.T) {
	for _, action := range []string{"login", "logout"} {
		for _, mode := range []string{"error", "panic", "short"} {
			b := newBrowser(t)
			form := b.form(t, "/login")
			path, outcome := "/login", "sdk_auth_completed_website_session_unconfirmed"
			var priorSession string
			if action == "logout" {
				b.login(t)
				priorSession = sessionCookie(t, b).Value
				_, body := b.request(t, "GET", "/home", nil, "")
				form = url.Values{"csrf_token": {hidden(t, body, "csrf_token")}, "revision": {hidden(t, body, "revision")}}
				path, outcome = "/logout", "session_destroyed_not_credential_revocation"
			} else {
				form.Set("key", testPassword)
			}
			// A redirect has no body. Force a distinct post-action body write through
			// the same middleware, exercising commit then delivery without replay.
			original := b.host.pages
			var state *responseState
			b.host.pages = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				original.ServeHTTP(w, r)
				state = responseFor(r)
				_, _ = w.Write([]byte("synthetic delivery"))
			})
			observer := &numericObserver{}
			b.host.observer = observer
			request := httptest.NewRequest("POST", "https://website.invalid"+path, strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("Origin", "https://website.invalid")
			u, _ := url.Parse(b.server.URL)
			for _, cookie := range b.client.Jar.Cookies(u) {
				request.AddCookie(cookie)
			}
			writer := &failingResponse{header: make(http.Header), panicValue: mode == "panic", short: mode == "short"}
			b.host.ServeHTTP(writer, request)
			kinds := observer.counts()
			if state == nil || state.outcome != outcome || len(kinds) != 1 || kinds[0] != failuretype.ResponseDeliveryUnknown || writer.status != 303 {
				t.Fatal("SITE_TEST_DELIVERY_OUTCOME_OWNERSHIP")
			}
			if action == "logout" {
				if _, found, err := b.host.store.Find(priorSession); err != nil || found {
					t.Fatal("SITE_TEST_DELIVERY_DID_NOT_UNDO_DESTROY")
				}
			} else {
				stored := false
				for _, cookie := range (&http.Response{Header: writer.header}).Cookies() {
					if cookie.Name != "__Host-possums" {
						continue
					}
					ctx, err := b.host.sessions.Load(context.Background(), cookie.Value)
					if err == nil && b.host.sessions.GetString(ctx, "credential") != "" {
						stored = true
					}
				}
				if !stored {
					t.Fatal("SITE_TEST_DELIVERY_DID_NOT_UNDO_SESSION_COMMIT")
				}
			}
		}
	}
}
func TestTypedCauseProjectionAndClosedDiagnosticText(t *testing.T) {
	if failuretype.Count != 39 {
		t.Fatal("SITE_TEST_CATEGORY_CARDINALITY")
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New(hostileFailure), &ledger.Diagnostic{Code: ledger.CodeInput, Stage: hostileFailure, Constraint: hostileFailure, Outcome: hostileFailure, NextStep: hostileFailure}} {
		d := classifiedHTTPFault("SITE_SESSION_UNAVAILABLE", cause)
		if !d.Kind().Valid() || strings.Contains(diagnosticText(d), hostileFailure) {
			t.Fatal("SITE_TEST_TYPED_CAUSE_PRIVACY")
		}
	}
	// A propagated auth refusal and a separate render failure are each owned
	// once. Rendering must not erase the earlier action/SDK outcome.
	b := newBrowser(t)
	observer := &numericObserver{}
	b.host.observer = observer
	form := b.form(t, "/login")
	form.Set("key", testPassword)
	b.host.auth = faultyAuth{customerAuth: b.host.auth}
	for i := range b.host.revision.Routes {
		if b.host.revision.Routes[i].Name == b.host.loginRoute().Name {
			b.host.revision.Routes[i].Entry = "missing-synthetic-entry"
		}
	}
	response, body := b.request(t, "POST", "/login", form, b.server.URL)
	assertClosedHTTP(t, response, body, "SITE_RENDER_UNAVAILABLE", "sdk_auth_completion_unknown_no_session_granted")
	kinds := observer.counts()
	if len(kinds) != 2 || kinds[0] != failuretype.NativeAuthUnknown || kinds[1] != failuretype.ResponseEncodingUnknown {
		t.Fatal("SITE_TEST_PROPAGATION_AND_RENDER_OWNER")
	}
}
func TestTLSRequiredAndNoAuthStorageOrTokensInDiagnostics(t *testing.T) {
	b := newBrowser(t)
	response := httptest.NewRecorder()
	b.host.ServeHTTP(response, httptest.NewRequest("GET", "http://website.invalid/login", nil))
	if response.Code != 400 || !strings.Contains(response.Body.String(), "SITE_HTTPS_REQUIRED") || len(response.Result().Cookies()) != 0 {
		t.Fatal("SITE_TEST_TLS_REQUIRED")
	}
	for _, path := range []string{"/", "/login"} {
		_, body := b.request(t, "GET", path, nil, "")
		for _, forbidden := range []string{"localStorage", "sessionStorage", "<script", "Authorization", testIdentity, testPassword} {
			if strings.Contains(body, forbidden) {
				t.Fatal("SITE_TEST_BROWSER_SECRET_BOUNDARY")
			}
		}
	}
	if !strings.Contains(diagnosticText(httpFault("SITE_SESSION_UNAVAILABLE")), "cause_unknown") {
		t.Fatal("SITE_TEST_UNKNOWN_CAUSE_EXPLICIT")
	}
}
