package website

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestActualIdleAbsoluteAndSDKTokenExpiration(t *testing.T) {
	for _, mode := range []string{"idle", "absolute", "sdk_token"} {
		t.Run(mode, func(t *testing.T) {
			b := newBrowser(t)
			b.login(t)
			ctx := sessionContext(t, b)
			cookie := sessionCookie(t, b)
			switch mode {
			case "idle":
				b.host.sessions.IdleTimeout = time.Millisecond
			case "absolute":
				b.host.sessions.SetDeadline(ctx, time.Now().Add(-time.Second))
			case "sdk_token":
				record, err := b.app.FindAuthRecordByEmail("website_synthetic", testIdentity)
				if err != nil {
					t.Fatal("SITE_TEST_EXPIRY_RECORD")
				}
				token, err := record.NewStaticAuthToken(time.Nanosecond)
				if err != nil {
					t.Fatal("SITE_TEST_EXPIRED_SDK_TOKEN")
				}
				b.host.sessions.Put(ctx, "credential", token)
			}
			if _, _, err := b.host.sessions.Commit(ctx); err != nil {
				t.Fatal("SITE_TEST_EXPIRY_COMMIT")
			}
			if mode == "idle" {
				time.Sleep(10 * time.Millisecond)
			}
			response, _ := b.request(t, http.MethodGet, "/home", nil, "")
			if response.StatusCode != 303 || response.Header.Get("Location") != "/login" {
				t.Fatal("SITE_TEST_EXPIRED_SESSION_ACCESS")
			}
			if _, found, err := b.host.store.Find(cookie.Value); err != nil || found {
				t.Fatal("SITE_TEST_EXPIRED_SESSION_RELOAD")
			}
		})
	}
}

func TestRenderFailurePreservesPreviouslyEstablishedAuthOutcomes(t *testing.T) {
	b := newBrowser(t)
	for _, outcome := range []string{"sdk_auth_completed_website_session_unconfirmed", "session_destroyed_not_credential_revocation"} {
		// This is a post-action render boundary fixture, not a claim that the normal
		// 303 login/logout flow evaluates a page before its redirect.
		out := httptest.NewRecorder()
		state, request := b.host.ownResponse(out, httptest.NewRequest("GET", "https://website.invalid/login", nil))
		setOutcome(request, outcome)
		b.host.page(state, request, Route{Entry: "absent-synthetic-entry"}, 200, "")
		if out.Code != 503 || !strings.Contains(out.Body.String(), outcome) {
			t.Fatal("SITE_TEST_RENDER_KNOWN_AUTH_OUTCOME")
		}
	}
}
