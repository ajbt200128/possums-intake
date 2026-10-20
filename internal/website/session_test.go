package website

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"possums-intake/internal/siteauth"
)

func sessionCookie(t *testing.T, b *browser) *http.Cookie {
	t.Helper()
	u, _ := url.Parse(b.server.URL)
	for _, c := range b.client.Jar.Cookies(u) {
		if c.Name == "__Host-possums" {
			return c
		}
	}
	t.Fatal("SITE_TEST_SESSION_COOKIE_MISSING")
	return nil
}
func sessionContext(t *testing.T, b *browser) context.Context {
	t.Helper()
	ctx, err := b.host.sessions.Load(context.Background(), sessionCookie(t, b).Value)
	if err != nil {
		t.Fatal("SITE_TEST_SESSION_CONTEXT")
	}
	return ctx
}
func TestSessionFixationRestartAndExpiry(t *testing.T) {
	b := newBrowser(t)
	// Seed an anonymous stock session so login must rotate a real stored token.
	ctx, err := b.host.sessions.Load(context.Background(), "")
	if err != nil {
		t.Fatal("SITE_TEST_LOAD")
	}
	b.host.sessions.Put(ctx, "anonymous", true)
	old, _, err := b.host.sessions.Commit(ctx)
	if err != nil {
		t.Fatal("SITE_TEST_COMMIT")
	}
	u, _ := url.Parse(b.server.URL)
	b.client.Jar.SetCookies(u, []*http.Cookie{{Name: "__Host-possums", Value: old, Path: "/", Secure: true}})
	b.login(t)
	current := sessionCookie(t, b).Value
	if old == current {
		t.Fatal("SITE_TEST_FIXATION_ROTATION")
	}
	if _, found, err := b.host.store.Find(old); err != nil || found {
		t.Fatal("SITE_TEST_FIXATION_OLD_TOKEN")
	}
	ctx = sessionContext(t, b)
	claims := jwt.MapClaims{}
	_, _, err = jwt.NewParser().ParseUnverified(b.host.sessions.GetString(ctx, "credential"), claims)
	expiry, e := claims.GetExpirationTime()
	if err != nil || e != nil || expiry == nil || b.host.sessions.Deadline(ctx).After(expiry.Time) {
		t.Fatal("SITE_TEST_TOKEN_DEADLINE_CAP")
	}

	// Actual stock Commit expiry is the earlier idle window or absolute deadline.
	before := time.Now()
	_, idleExpiry, err := b.host.sessions.Commit(ctx)
	if err != nil || idleExpiry.Before(before.Add(b.host.sessions.IdleTimeout)) || idleExpiry.After(time.Now().Add(b.host.sessions.IdleTimeout)) {
		t.Fatal("SITE_TEST_IDLE_EXPIRY_BOUND")
	}
	absolute := time.Now().Add(time.Minute)
	b.host.sessions.SetDeadline(ctx, absolute)
	_, absoluteExpiry, err := b.host.sessions.Commit(ctx)
	if err != nil || !absoluteExpiry.Equal(absolute) {
		t.Fatal("SITE_TEST_ABSOLUTE_EXPIRY_BOUND")
	}
	// No wall-clock sleep: commit the same stock snapshot with past expiry.
	// Find must deny it; disabled cleanup is not required for access denial.
	bytes, found, err := b.host.store.Find(current)
	if err != nil || !found {
		t.Fatal("SITE_TEST_STORED_SESSION")
	}
	if b.host.store.Commit(current, bytes, time.Now().Add(-time.Second)) != nil {
		t.Fatal("SITE_TEST_EXPIRE")
	}
	r, _ := b.request(t, "GET", "/home", nil, "")
	if r.StatusCode != 303 {
		t.Fatal("SITE_TEST_EXPIRED_ACCESS")
	}

	b.login(t)
	replacement, err := NewFixture(b.host.revision, b.host.auth, b.host.fallback, Config{Lifetime: time.Hour, IdleTimeout: time.Minute, ConcurrentRequests: 2})
	if err != nil {
		t.Fatal("SITE_TEST_RESTART_HOST")
	}
	// Old cookie presented to a new process-owned store cannot authenticate.
	req := httptest.NewRequest("GET", "https://website.invalid/home", nil)
	req.AddCookie(sessionCookie(t, b))
	out := httptest.NewRecorder()
	replacement.ServeHTTP(out, req)
	if out.Code != 303 || out.Header().Get("Location") != "/login" {
		t.Fatal("SITE_TEST_RESTART_SESSION_LOSS")
	}
}

func TestSessionCredentialRevalidationActualSDK(t *testing.T) {
	for _, change := range []string{"delete", "password", "token_key", "auth_rule"} {
		t.Run(change, func(t *testing.T) {
			b := newBrowser(t)
			b.login(t)
			cookie := sessionCookie(t, b)
			record, err := b.app.FindAuthRecordByEmail("website_synthetic", testIdentity)
			if err != nil {
				t.Fatal("SITE_TEST_RECORD")
			}
			switch change {
			case "delete":
				err = b.app.Delete(record)
			case "password":
				record.SetPassword("replacement-synthetic-password")
				err = b.app.Save(record)
			case "token_key":
				record.RefreshTokenKey()
				err = b.app.Save(record)
			case "auth_rule":
				collection, e := b.app.FindCollectionByNameOrId("website_synthetic")
				if e != nil {
					t.Fatal("SITE_TEST_COLLECTION_LOOKUP")
				}
				collection.AuthRule = nil
				err = b.app.Save(collection)
			}
			if err != nil {
				t.Fatal("SITE_TEST_REVALIDATION_MUTATION")
			}
			response, _ := b.request(t, "GET", "/home", nil, "")
			if response.StatusCode != 303 || response.Header.Get("Location") != "/login" {
				t.Fatal("SITE_TEST_REVALIDATION_ACCESS")
			}
			if _, found, err := b.host.store.Find(cookie.Value); err != nil || found {
				t.Fatal("SITE_TEST_REVALIDATION_SESSION_DESTROY")
			}
		})
	}
}

type tokenAuth struct{ token string }

func (a tokenAuth) Login(context.Context, string) (string, siteauth.Principal, error) {
	return a.token, siteauth.Principal{}, nil
}
func (a tokenAuth) Validate(context.Context, string) (siteauth.Principal, error) {
	return siteauth.Principal{}, nil
}
func TestTokenExpiryCapsSessionAndRejectsExpiredGrant(t *testing.T) {
	b := newBrowser(t)
	for _, past := range []bool{false, true} {
		deadline := time.Now().Add(30 * time.Second).Truncate(time.Second)
		if past {
			deadline = time.Now().Add(-time.Minute)
		}
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"exp": deadline.Unix()}).SignedString([]byte("synthetic-only-test-key"))
		if err != nil {
			t.Fatal("SITE_TEST_TOKEN_FIXTURE")
		}
		// Stub isolates the host's expiry cap; SDK verification is covered separately.
		b.host.auth = tokenAuth{token}
		form := b.form(t, "/login")
		form.Set("key", "synthetic")
		response, body := b.request(t, "POST", "/login", form, b.server.URL)
		if past {
			if response.StatusCode != 503 || !strings.Contains(body, "sdk_auth_completed_website_session_unconfirmed") {
				t.Fatal("SITE_TEST_EXPIRED_GRANT")
			}
		} else {
			if response.StatusCode != 303 || !b.host.sessions.Deadline(sessionContext(t, b)).Equal(deadline) {
				t.Fatal("SITE_TEST_SHORT_TOKEN_CAP")
			}
		}
	}
}
func TestPublicLogoutStillProtectedAndFormFieldLimits(t *testing.T) {
	b := newBrowser(t)
	for i := range b.host.revision.Routes {
		if b.host.revision.Routes[i].Action == "auth/logout" {
			b.host.revision.Routes[i].Policy = "public"
		}
	}
	form := b.form(t, "/login")
	response, body := b.request(t, "POST", "/logout", form, b.server.URL)
	if response.StatusCode != 401 || !strings.Contains(body, "SITE_AUTH_REQUIRED") {
		t.Fatal("SITE_TEST_PUBLIC_LOGOUT_AUTHORIZATION")
	}
	// Only the single key field is an admitted customer input. The retired
	// identity/password SDK selectors, duplicates, absent and unbounded keys all
	// fail closed before any authentication work is entered.
	for _, bad := range []string{"missing", "duplicate", "limit", "invalid_utf8", "identity_selector", "password_selector", "unknown"} {
		values := form.Clone()
		values.Set("key", testPassword)
		switch bad {
		case "missing":
			values.Del("key")
		case "duplicate":
			values.Add("key", "synthetic")
		case "limit":
			values.Set("key", strings.Repeat("k", 256))
		case "invalid_utf8":
			values.Set("key", string([]byte{0xff}))
		case "identity_selector":
			values.Set("identity", testIdentity)
		case "password_selector":
			values.Set("password", testPassword)
		case "unknown":
			values.Set("account", "synthetic")
		}
		response, body = b.request(t, "POST", "/login", values, b.server.URL)
		if response.StatusCode != 400 || !strings.Contains(body, "SITE_FORM_REFUSED") {
			t.Fatal("SITE_TEST_FIELD_LIMIT")
		}
	}
	b.login(t)
	response, body = b.request(t, "GET", "/home", nil, "")
	logout := url.Values{"csrf_token": {hidden(t, body, "csrf_token")}, "revision": {hidden(t, body, "revision")}}
	response, _ = b.request(t, "POST", "/logout", logout, b.server.URL)
	if response.StatusCode != 303 {
		t.Fatal("SITE_TEST_PUBLIC_LOGOUT_VALID")
	}
}
