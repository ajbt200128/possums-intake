package website

import (
	"strings"
	"testing"
)

func TestCustomerSessionUsesCurrentSDKTokenAndSoleAccountID(t *testing.T) {
	f := newAccountFixture(t)
	if accountLogin(t, f.browser, keyHTTPA) != 303 {
		t.Fatal("SITE_TEST_CUSTOMER_LOGIN")
	}
	response, _ := f.request(t, "GET", "/home", nil, "")
	if response.StatusCode != 200 {
		t.Fatal("SITE_TEST_CUSTOMER_HOME")
	}
	r, err := f.sdk.FindRecordById(f.credential.Collection(), f.credential.Id)
	if err != nil || r.Collection().Fields.GetByName("account_id") != nil {
		t.Fatal("SITE_TEST_SOLE_IDENTITY")
	}
	// Trusted native token invalidation cannot silently preserve an old session.
	r.RefreshTokenKey()
	if f.sdk.Save(r) != nil {
		t.Fatal("SITE_TEST_CUSTOMER_TOKEN_INVALIDATION")
	}
	before := f.counter.calls.Load()
	response, body := f.request(t, "GET", "/home", nil, "")
	if response.StatusCode != 303 || response.Header.Get("Location") != "/login" || f.counter.calls.Load() != before || strings.Contains(body, keyHTTPA) {
		t.Fatal("SITE_TEST_STALE_TOKEN_NOT_LOGGED_OUT")
	}
	if accountLogin(t, f.browser, keyHTTPA) != 303 {
		t.Fatal("SITE_TEST_FRESH_SDK_LOGIN")
	}
	// No browser-provided account field is accepted, even with the correct key.
	form := f.form(t, "/login")
	form.Set("key", keyHTTPA)
	form.Set("account_id", accountHTTPB)
	response, _ = f.request(t, "POST", "/login", form, f.server.URL)
	if response.StatusCode != 400 {
		t.Fatal("SITE_TEST_BROWSER_ACCOUNT_SELECTOR_ACCEPTED")
	}
}
