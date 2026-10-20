package website

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/siteauth"
)

func TestCustomerSessionCannotSilentlySwitchBinding(t *testing.T) {
	b := newBrowser(t)
	if siteauth.InitializeCustomerAuth(b.app) != nil {
		t.Fatal("SITE_TEST_CUSTOMER_SCHEMA")
	}
	c, err := b.app.FindCollectionByNameOrId(siteauth.CustomerCollection)
	if err != nil {
		t.Fatal("SITE_TEST_CUSTOMER_COLLECTION")
	}
	key := strings.Repeat("A", 128)
	lookup := sha256.Sum256([]byte("possums-intake/customer-key/lookup/v1\x00" + key))
	password := sha256.Sum256([]byte("possums-intake/customer-key/password/v1\x00" + key))
	r := core.NewRecord(c)
	r.Set("key_lookup", "v1_"+hex.EncodeToString(lookup[:]))
	r.Set("account_id", "synthetic-account-a")
	r.SetPassword(base64.RawURLEncoding.EncodeToString(password[:]))
	if b.app.Save(r) != nil {
		t.Fatal("SITE_TEST_CUSTOMER_CREDENTIAL")
	}
	adapter, err := siteauth.NewCustomer(b.app)
	if err != nil {
		t.Fatal("SITE_TEST_CUSTOMER_ADAPTER")
	}
	b.host.auth = adapter
	login := func() {
		form := b.form(t, "/login")
		form.Set("key", key)
		response, body := b.request(t, "POST", "/login", form, b.server.URL)
		if response.StatusCode != 303 || strings.Contains(body, key) {
			t.Fatal("SITE_TEST_CUSTOMER_LOGIN")
		}
	}
	login()
	response, _ := b.request(t, "GET", "/home", nil, "")
	if response.StatusCode != 200 {
		t.Fatal("SITE_TEST_CUSTOMER_HOME")
	}
	r.Set("account_id", "synthetic-account-b")
	if b.app.Save(r) != nil {
		t.Fatal("SITE_TEST_CUSTOMER_REBIND")
	}
	response, _ = b.request(t, "GET", "/home", nil, "")
	if response.StatusCode != 303 || response.Header.Get("Location") != "/login" {
		t.Fatal("SITE_TEST_STALE_BINDING_NOT_LOGGED_OUT")
	}
	login()
	response, _ = b.request(t, "GET", "/home", nil, "")
	if response.StatusCode != 200 {
		t.Fatal("SITE_TEST_FRESH_BINDING_LOGIN")
	}
	// No browser-provided account field is accepted, even with the correct key.
	form := b.form(t, "/login")
	form.Set("key", key)
	form.Set("account_id", "synthetic-account-a")
	response, _ = b.request(t, "POST", "/login", form, b.server.URL)
	if response.StatusCode != 400 {
		t.Fatal("SITE_TEST_BROWSER_ACCOUNT_SELECTOR_ACCEPTED")
	}
}
