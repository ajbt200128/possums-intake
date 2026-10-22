package website

import (
	"net/url"
	"testing"
	"time"

	"possums-intake/internal/siteauth"
)

// The approved pairing is independent: a twelve-hour native customer token does
// not extend the website's one-hour absolute / ten-minute idle session.
func TestTwelveHourNativeTokenStillCappedToOneHourSession(t *testing.T) {
	f := newAccountFixture(t)
	b := f.browser
	c, err := f.sdk.FindCollectionByNameOrId(siteauth.CustomerCollection)
	if err != nil || c.AuthToken.Duration != 12*60*60 {
		t.Fatal("SITE_TEST_MERGED_CUSTOMER_TOKEN_LIFETIME")
	}
	// Exercise the actual merged account's supported SDK login, without a
	// separate credential binding or overriding the initialized token lifetime.
	if b.host.sessions.Lifetime != time.Hour || b.host.sessions.IdleTimeout != 10*time.Minute {
		t.Fatal("SITE_TEST_SESSION_POLICY_CHANGED")
	}
	before := time.Now()
	if accountLogin(t, b, keyHTTPA) != 303 {
		t.Fatal("SITE_TEST_MERGED_CUSTOMER_LOGIN")
	}
	ctx := sessionContext(t, b)
	// One-hour absolute deadline, not the twelve-hour native token expiry.
	if delta := b.host.sessions.Deadline(ctx).Sub(before); delta > time.Hour+time.Second || delta < 59*time.Minute {
		t.Fatal("SITE_TEST_ABSOLUTE_LIFETIME_NOT_ONE_HOUR")
	}
	// Committed expiry is the earlier idle window: ten minutes of silence.
	commit := time.Now()
	_, expiry, err := b.host.sessions.Commit(ctx)
	if err != nil || expiry.Sub(commit) > 10*time.Minute+time.Second || expiry.Sub(commit) < 9*time.Minute {
		t.Fatal("SITE_TEST_IDLE_EXPIRY_NOT_TEN_MINUTES")
	}
	u, _ := url.Parse(b.server.URL)
	found := false
	for _, cookie := range b.client.Jar.Cookies(u) {
		if cookie.Name == "__Host-possums" {
			// Sessions stay non-persistent, so the browser cookie carries no expiry.
			found = cookie.MaxAge == 0 && cookie.Expires.IsZero()
		}
	}
	if !found {
		t.Fatal("SITE_TEST_SESSION_COOKIE_BECAME_PERSISTENT")
	}
}
