package website

import (
	"context"
	"crypto/sha256"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
	"possums-intake/internal/siteauth"
)

// The fixture SDK record keeps an identity/password pair, but customers only
// ever present the single demo key. testPassword is that key.
const testIdentity = "website-synthetic@example.invalid"
const testPassword = "website-synthetic-password-123"

type browser struct {
	client *http.Client
	server *httptest.Server
	host   *Host
	app    *core.BaseApp
}

func newBrowser(t *testing.T) *browser {
	t.Helper()
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir(), QueryTimeout: time.Second})
	app.Settings().Logs.MaxDays = 0
	if app.Bootstrap() != nil {
		t.Fatal("SITE_TEST_SDK_BOOTSTRAP")
	}
	t.Cleanup(func() {
		if app.ResetBootstrapState() != nil {
			t.Error("SITE_TEST_SDK_CLOSE")
		}
	})
	app.Settings().Logs.MaxDays = 0
	if app.Save(app.Settings()) != nil {
		t.Fatal("SITE_TEST_QUIET_SETTINGS")
	}
	c := core.NewAuthCollection("website_synthetic")
	c.AuthAlert.Enabled = false
	c.AuthRule = types.Pointer("")
	c.PasswordAuth.Enabled = true
	c.PasswordAuth.IdentityFields = []string{"email"}
	if app.Save(c) != nil {
		t.Fatal("SITE_TEST_COLLECTION")
	}
	record := core.NewRecord(c)
	record.SetEmail(testIdentity)
	record.SetPassword(testPassword)
	if app.Save(record) != nil {
		t.Fatal("SITE_TEST_CREDENTIAL")
	}
	adapter, err := siteauth.New(app, c.Name)
	if err != nil {
		t.Fatal("SITE_TEST_AUTH_ADAPTER")
	}
	// One-key front door: fixed synthetic SDK identity, pasted key as password.
	auth := siteauth.FixtureKeyAdapter{Adapter: adapter, Identity: testIdentity}
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Synthetic-Fallback", "true")
		w.WriteHeader(418)
	})
	h, err := NewFixture(prepared(t), auth, fallback, Config{Lifetime: time.Hour, IdleTimeout: 10 * time.Minute, ConcurrentRequests: 16})
	if err != nil {
		t.Fatal("SITE_TEST_HOST")
	}
	server := httptest.NewTLSServer(h)
	t.Cleanup(server.Close)
	client := server.Client()
	client.Jar, _ = cookiejar.New(nil)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &browser{client, server, h, app}
}
func (b *browser) request(t *testing.T, method, path string, form url.Values, origin string) (*http.Response, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r, err := http.NewRequest(method, b.server.URL+path, body)
	if err != nil {
		t.Fatal("SITE_TEST_REQUEST")
	}
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	response, err := b.client.Do(r)
	if err != nil {
		t.Fatal("SITE_TEST_TLS_REQUEST")
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal("SITE_TEST_RESPONSE")
	}
	if strings.Contains(string(data), testIdentity) || strings.Contains(string(data), testPassword) {
		t.Fatal("SITE_TEST_CREDENTIAL_DISCLOSURE")
	}
	return response, string(data)
}
func hidden(t *testing.T, markup, name string) string {
	t.Helper()
	m := regexp.MustCompile(`name="` + name + `" value="([^"]+)"`).FindStringSubmatch(markup)
	if len(m) != 2 {
		t.Fatal("SITE_TEST_HIDDEN_FIELD")
	}
	return html.UnescapeString(m[1])
}
func (b *browser) form(t *testing.T, path string) url.Values {
	t.Helper()
	r, html := b.request(t, "GET", path, nil, "")
	if r.StatusCode != 200 {
		t.Fatal("SITE_TEST_FORM_GET")
	}
	return url.Values{"csrf_token": {hidden(t, html, "csrf_token")}, "revision": {hidden(t, html, "revision")}}
}
func (b *browser) login(t *testing.T) {
	t.Helper()
	form := b.form(t, "/login")
	form.Set("key", testPassword)
	response, _ := b.request(t, "POST", "/login", form, b.server.URL)
	if response.StatusCode != 303 || response.Header.Get("Location") != "/home" {
		t.Fatalf("SITE_TEST_LOGIN_STATUS_%d", response.StatusCode)
	}
}
func TestActualTLSNoJSFormsSessionsAndLogout(t *testing.T) {
	b := newBrowser(t)
	response, html := b.request(t, "GET", "/", nil, "")
	if response.StatusCode != 200 || !strings.Contains(html, `href="/login"`) || response.Header.Get("Cache-Control") != "no-store" || !strings.Contains(response.Header.Get("Content-Security-Policy"), "script-src 'none'") {
		t.Fatal("SITE_TEST_PUBLIC_PAGE")
	}
	response, _ = b.request(t, "GET", "/home", nil, "")
	if response.StatusCode != 303 || response.Header.Get("Location") != "/login" {
		t.Fatal("SITE_TEST_SESSION_REQUIRED")
	}
	b.login(t)
	response, html = b.request(t, "GET", "/home", nil, "")
	if response.StatusCode != 200 || !strings.Contains(html, "You are signed in.") || !strings.Contains(html, `action="/logout"`) {
		t.Fatal("SITE_TEST_SIGNED_PAGE")
	}
	var sessionCookie *http.Cookie
	root, _ := url.Parse(b.server.URL)
	for _, c := range b.client.Jar.Cookies(root) {
		if c.Name == "__Host-possums" {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("SITE_TEST_SESSION_COOKIE")
	}
	ctx, err := b.host.sessions.Load(context.Background(), sessionCookie.Value)
	if err != nil {
		t.Fatal("SITE_TEST_SESSION_LOOKUP")
	}
	token := b.host.sessions.GetString(ctx, "credential")
	if token == "" || strings.Contains(html, token) {
		t.Fatal("SITE_TEST_PRIVATE_TOKEN_BOUNDARY")
	}
	logout := url.Values{"csrf_token": {hidden(t, html, "csrf_token")}, "revision": {hidden(t, html, "revision")}}
	response, _ = b.request(t, "POST", "/logout", logout, b.server.URL)
	if response.StatusCode != 303 || response.Header.Get("Location") != "/" {
		t.Fatal("SITE_TEST_LOGOUT")
	}
	response, _ = b.request(t, "GET", "/home", nil, "")
	if response.StatusCode != 303 {
		t.Fatal("SITE_TEST_LOGGED_OUT")
	}
	var count int
	for _, query := range []string{"SELECT count(*) FROM _logs", "SELECT count(*) FROM _authOrigins"} {
		db := b.app.DB()
		if strings.Contains(query, "_logs") {
			db = b.app.AuxDB()
		}
		if db.NewQuery(query).Row(&count) != nil || count != 0 {
			t.Fatal("SITE_TEST_ACTIVITY_RECORDED")
		}
	}
}
func TestCookieFlagsCSRFStaleFormsAndNeutralCredentials(t *testing.T) {
	b := newBrowser(t)
	response, html := b.request(t, "GET", "/login", nil, "")
	for _, c := range response.Cookies() {
		if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" || c.MaxAge != 0 || !c.Expires.IsZero() {
			t.Fatal("SITE_TEST_COOKIE_FLAGS")
		}
	}
	form := url.Values{"csrf_token": {hidden(t, html, "csrf_token")}, "revision": {hidden(t, html, "revision")}, "key": {testPassword}}
	response, body := b.request(t, "POST", "/login", form, "https://different.invalid")
	if response.StatusCode != 403 || !strings.Contains(body, "SITE_CSRF_REFUSED") {
		t.Fatal("SITE_TEST_CSRF_ORIGIN")
	}
	response, body = b.request(t, "POST", "/login", form, "")
	if response.StatusCode != 403 || !strings.Contains(body, "first-party cookies") {
		t.Fatal("SITE_TEST_CSRF_MISSING_ORIGIN")
	}
	stale := form.Clone()
	stale.Set("revision", "old-synthetic-revision")
	response, body = b.request(t, "POST", "/login", stale, b.server.URL)
	if response.StatusCode != 409 || !strings.Contains(body, "SITE_REVISION_STALE") {
		t.Fatal("SITE_TEST_STALE_ACTION")
	}
	// One neutral refusal: an incorrect key never reveals whether a fixture
	// record exists, and the submitted key is never echoed back.
	wrong := form.Clone()
	wrong.Set("key", "incorrect-synthetic-key")
	response, body = b.request(t, "POST", "/login", wrong, b.server.URL)
	if response.StatusCode != 401 || !strings.Contains(body, `<span id="form-error" role="alert">Invalid credentials. Please try again.</span>`) || !strings.Contains(body, `aria-describedby="form-error"`) {
		t.Fatal("SITE_TEST_AUTH_REFUSAL")
	}
	if strings.Index(body, `id="form-error"`) <= strings.Index(body, "</form>") {
		t.Fatal("SITE_TEST_AUTH_REFUSAL_BELOW_FORM")
	}
	if !strings.Contains(b.host.revision.CSS(), "#form-error{font-weight:700;color:#b91c1c}") {
		t.Fatal("SITE_TEST_AUTH_REFUSAL_RED")
	}
	for _, echoed := range []string{"incorrect-synthetic-key", "absent-website@example.invalid", "SITE_AUTH_REFUSED", "stage=", "constraint=", "outcome="} {
		if strings.Contains(body, echoed) {
			t.Fatal("SITE_TEST_KEY_ECHO")
		}
	}
	b.client.Jar = nil
	response, body = b.request(t, "POST", "/login", form, b.server.URL)
	if response.StatusCode != 403 || !strings.Contains(body, "Enable first-party cookies") {
		t.Fatal("SITE_TEST_COOKIES_DISABLED")
	}
}
func TestPinnedFontResourcesOverTLS(t *testing.T) {
	b := newBrowser(t)
	for _, a := range []struct {
		path, kind, hash string
		size             int
	}{
		{"/_site/fonts/iosevka-term-regular.woff2", "font/woff2", "2921da08b32396422927bb76e3618938e13ede0b3dfb1785d5ca9d88df863752", 1703712},
		{"/_site/fonts/iosevka-term-bold.woff2", "font/woff2", "3f9db216e17c47e03fd0c392b17e3a2f7cbd568c1bc4a8dfb165a106df273753", 1724824},
		{"/_site/fonts/OFL.txt", "text/plain; charset=utf-8", "4ba53c7c1cb39279aae5f8d7d22054c485c71169920e5a36ed098b115e2e3c5d", 4493},
	} {
		r, body := b.request(t, "GET", a.path, nil, "")
		if r.StatusCode != 200 || r.Header.Get("Content-Type") != a.kind || len(body) != a.size || fmt.Sprintf("%x", sha256.Sum256([]byte(body))) != a.hash || len(r.Cookies()) != 0 {
			t.Fatal("SITE_TEST_PINNED_ASSET")
		}
		r, body = b.request(t, "HEAD", a.path, nil, "")
		if r.StatusCode != 200 || body != "" || len(r.Cookies()) != 0 {
			t.Fatal("SITE_TEST_ASSET_HEAD")
		}
	}
	r, body := b.request(t, "GET", b.host.revision.CSSPath(), nil, "")
	if r.StatusCode != 200 || !strings.Contains(body, "font-display:swap") || !strings.Contains(body, "font-weight:700") || strings.Contains(body, "https:") || len(r.Cookies()) != 0 {
		t.Fatal("SITE_TEST_LOCAL_CSS")
	}
	for _, path := range []string{"/_site/fonts/NOTICE.md", "/_site/fonts/manifest.json", "/_site/fonts/../assets/OFL.txt"} {
		r, _ := b.request(t, "GET", path, nil, "")
		if r.StatusCode != 404 || len(r.Cookies()) != 0 {
			t.Fatal("SITE_TEST_ASSET_ALLOWLIST")
		}
	}
}

func TestReservedRoutesMethodsAndBounds(t *testing.T) {
	b := newBrowser(t)
	for _, path := range []string{"/api/collections", "/_/", "/healthz", "/balance", "/reservations/x", "/attestation"} {
		response, _ := b.request(t, "GET", path, nil, "")
		if response.StatusCode != 418 || response.Header.Get("X-Synthetic-Fallback") != "true" || len(response.Cookies()) != 0 {
			t.Fatal("SITE_TEST_RESERVED_INTERCEPTION")
		}
	}
	for _, item := range []struct {
		method, path string
		status       int
	}{{"GET", "/logout", 405}, {"GET", "/missing", 404}, {"GET", "/%6cogin", 400}, {"GET", "/login?token=synthetic", 400}, {"DELETE", "/login", 405}} {
		r, _ := b.request(t, item.method, item.path, nil, "")
		if r.StatusCode != item.status {
			t.Fatalf("SITE_TEST_METHOD_STATUS_%d_EXPECTED_%d", r.StatusCode, item.status)
		}
	}
	form := b.form(t, "/login")
	form.Set("key", strings.Repeat("x", 5000))
	response, _ := b.request(t, "POST", "/login", form, b.server.URL)
	if response.StatusCode != 400 {
		t.Fatal("SITE_TEST_BODY_LIMIT")
	}
}
