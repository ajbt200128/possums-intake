package website

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestTLSOffloadIsOwnerPolicyNotForwardedHeaders(t *testing.T) {
	b := newBrowser(t)
	for _, proto := range []string{"https", "http"} {
		r := httptest.NewRequest("GET", "http://synthetic.invalid/login", nil)
		r.Header.Set("X-Forwarded-Proto", proto)
		r.Header.Set("Forwarded", `proto=https;host=synthetic.invalid`)
		w := httptest.NewRecorder()
		b.host.ServeHTTP(w, r)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "SITE_HTTPS_REQUIRED") || len(w.Result().Cookies()) != 0 {
			t.Fatal("SITE_RUNTIME_FORWARDED_HEADER_TRUSTED")
		}
	}
	h, err := NewFixture(b.host.revision, b.host.auth, b.host.fallback, Config{Lifetime: time.Hour, IdleTimeout: 10 * time.Minute, ConcurrentRequests: 2, TLSOffloaded: true})
	if err != nil {
		t.Fatal("SITE_RUNTIME_OFFLOAD_SETUP")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://synthetic.invalid/login", nil))
	if w.Code != 200 {
		t.Fatal("SITE_RUNTIME_OFFLOAD_GET")
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("SITE_RUNTIME_OFFLOAD_COOKIES_MISSING")
	}
	for _, c := range cookies {
		if !c.Secure || !c.HttpOnly || c.Path != "/" || c.Domain != "" || c.SameSite != http.SameSiteLaxMode || c.MaxAge != 0 {
			t.Fatal("SITE_RUNTIME_OFFLOAD_COOKIE_WEAKENED")
		}
	}
	form := url.Values{"csrf_token": {hidden(t, w.Body.String(), "csrf_token")}, "revision": {hidden(t, w.Body.String(), "revision")}, "key": {testPassword}}
	for _, tc := range []struct {
		origin string
		status int
	}{{"http://synthetic.invalid", 403}, {"https://other.invalid", 403}, {"https://synthetic.invalid", 303}} {
		r := httptest.NewRequest("POST", "http://synthetic.invalid/login", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", tc.origin)
		// A hostile forwarded value cannot change the fixed HTTPS-origin policy.
		r.Header.Set("X-Forwarded-Proto", "http")
		for _, c := range cookies {
			r.AddCookie(c)
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, r)
		if response.Code != tc.status {
			t.Fatal("SITE_RUNTIME_OFFLOAD_CSRF_ORIGIN")
		}
	}
}

func TestDirectTLSGateCoversStaticAssets(t *testing.T) {
	b := newBrowser(t)
	for _, path := range []string{b.host.revision.CSSPath(), "/_site/fonts/iosevka-term-regular.woff2", "/_site/fonts/iosevka-term-bold.woff2", "/_site/fonts/OFL.txt"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			r := httptest.NewRequest(method, "http://synthetic.invalid"+path, nil)
			r.Header.Set("X-Forwarded-Proto", "https")
			w := httptest.NewRecorder()
			b.host.ServeHTTP(w, r)
			if w.Code != 400 || !strings.Contains(w.Body.String(), "SITE_HTTPS_REQUIRED") || len(w.Result().Cookies()) != 0 {
				t.Fatal("SITE_RUNTIME_STATIC_TLS_GATE_BYPASSED")
			}
		}
	}
}

func TestProcessLifetimeHostStartsClosedWithVolatileSessions(t *testing.T) {
	b := newBrowser(t)
	h, err := New(b.host.auth, b.host.fallback, Config{Lifetime: time.Hour, IdleTimeout: time.Minute, ConcurrentRequests: 2})
	if err != nil || h.sessions.Store != h.store || h.sessions.Cookie.Persist || !h.sessions.Cookie.Secure {
		t.Fatal("SITE_RUNTIME_PROCESS_STORE")
	}
	// The stock worker is deliberately process-lifetime; do not call its racy
	// StopCleanup API. These public APIs verify expiry, not physical map pruning.
	if h.store.Commit("synthetic-expired", []byte("synthetic"), time.Now().Add(-time.Second)) != nil {
		t.Fatal("SITE_RUNTIME_TEST_SESSION_COMMIT")
	}
	if _, exists, err := h.store.Find("synthetic-expired"); err != nil || exists {
		t.Fatal("SITE_RUNTIME_EXPIRED_SESSION_ACCESSIBLE")
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest("GET", "https://synthetic.invalid/", nil))
	if response.Code != 503 || !strings.Contains(response.Body.String(), "SITE_PUBLICATION_UNAVAILABLE") {
		t.Fatal("SITE_RUNTIME_IMPLICIT_DEMO")
	}
}
