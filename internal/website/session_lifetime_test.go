package website

import (
	"net/url"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/tools/types"
)

// The approved pairing is independent: a twelve-hour native customer token does
// not extend the website's one-hour absolute / ten-minute idle session.
func TestTwelveHourNativeTokenStillCappedToOneHourSession(t *testing.T) {
	b := newBrowser(t)
	c, err := b.app.FindCollectionByNameOrId("website_synthetic")
	if err != nil {
		t.Fatal("SITE_TEST_COLLECTION_LOOKUP")
	}
	// Twelve hours, the intake customer auth default; unrelated to the session.
	c.AuthToken.Duration = 12 * 60 * 60
	c.AuthRule = types.Pointer("")
	if b.app.Save(c) != nil {
		t.Fatal("SITE_TEST_COLLECTION_UPDATE")
	}
	if b.host.sessions.Lifetime != time.Hour || b.host.sessions.IdleTimeout != 10*time.Minute {
		t.Fatal("SITE_TEST_SESSION_POLICY_CHANGED")
	}
	before := time.Now()
	b.login(t)
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
