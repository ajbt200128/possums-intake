package siteauth_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/ledger"
	"possums-intake/internal/operator"
	"possums-intake/internal/siteauth"
)

type keyFixture struct {
	app     core.App
	handler http.Handler
	admin   string
	dir     string
}

func newKeyFixture(t *testing.T) *keyFixture {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	if ledger.InitOperator(ctx, dir, ledger.OperatorCredentials{Email: "key-fixture@example.invalid", Password: "synthetic-native-key-password"}) != nil {
		t.Fatal("synthetic key fixture init unavailable")
	}
	service, err := ledger.Open(ctx, dir)
	if err != nil {
		t.Fatal("synthetic key fixture open unavailable")
	}
	t.Cleanup(func() {
		if service.Close() != nil {
			t.Error("synthetic key fixture close unavailable")
		}
	})
	app, err := service.OperatorApp()
	if err != nil {
		t.Fatal("synthetic native app unavailable")
	}
	if siteauth.InitializeCustomerAuth(app) != nil {
		t.Fatal("synthetic explicit key schema unavailable")
	}
	handler, err := operator.NewHandler(http.NotFoundHandler(), app, service)
	if err != nil {
		t.Fatal("synthetic key native router unavailable")
	}
	f := &keyFixture{app: app, handler: handler, dir: dir}
	response := f.request(t, "POST", "/api/collections/_superusers/auth-with-password", map[string]any{"identity": "key-fixture@example.invalid", "password": "synthetic-native-key-password"})
	var auth struct {
		Token string `json:"token"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &auth) != nil || auth.Token == "" {
		t.Fatal("synthetic native login unavailable")
	}
	f.admin = auth.Token
	return f
}

func (f *keyFixture) request(t *testing.T, method, path string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal("synthetic request encoding unavailable")
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	if f.admin != "" {
		req.Header.Set("Authorization", f.admin)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	return w
}

func syntheticMaterial(key string) (string, string) {
	lookup := sha256.Sum256([]byte("possums-intake/customer-key/lookup/v1\x00" + key))
	password := sha256.Sum256([]byte("possums-intake/customer-key/password/v1\x00" + key))
	return "v1_" + hex.EncodeToString(lookup[:]), base64.RawURLEncoding.EncodeToString(password[:])
}

func TestNativeCustomerKeyPreFormQualification(t *testing.T) {
	f := newKeyFixture(t)
	path := "/api/collections/" + siteauth.CustomerCollection + "/records"
	for _, size := range []int{32, 53, 54, 72, 96} {
		key := base64.RawURLEncoding.EncodeToString(make([]byte, size))
		lookup, secret := syntheticMaterial(key)
		response := f.request(t, "POST", path, map[string]any{"password": key, "passwordConfirm": key, "account_id": "synthetic-account"})
		if response.Code != 200 {
			t.Fatalf("native key create refused: status=%d", response.Code)
		}
		if strings.Contains(response.Body.String(), key) || strings.Contains(response.Body.String(), secret) {
			t.Fatal("native response released key material")
		}
		var saved struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(response.Body.Bytes(), &saved) != nil || saved.ID == "" {
			t.Fatal("native credential response invalid")
		}
		r, err := f.app.FindRecordById(siteauth.CustomerCollection, saved.ID)
		if err != nil || r.GetString("key_lookup") != lookup || !r.ValidatePassword(secret) || r.ValidatePassword(key) || r.GetString("password") != "" || r.GetString("account_id") != "synthetic-account" || r.Email() != "" {
			t.Fatal("native verifier-only storage mismatch")
		}
		adapter, err := siteauth.New(f.app, siteauth.CustomerCollection)
		if err != nil {
			t.Fatal("synthetic SDK adapter unavailable")
		}
		token, p, err := adapter.Login(context.Background(), lookup, secret)
		if err != nil || token == "" || p.RecordID != saved.ID {
			t.Fatal("stock final SDK auth refused derived key")
		}
		if _, _, err := adapter.Login(context.Background(), lookup, lookup); err == nil {
			t.Fatal("stored lookup used as SDK login secret")
		}
		assertNoKeyBytes(t, f.dir, key, secret)
		if f.app.Delete(r) != nil {
			t.Fatal("synthetic credential cleanup unavailable")
		}
	}
	for _, confirm := range []any{nil, "", base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)), "not-a-key", map[string]any{"hostile": "SYNTHETIC_INPUT_SENTINEL"}} {
		body := map[string]any{"password": strings.Repeat("A", 43), "account_id": "synthetic-account"}
		if confirm != nil {
			body["passwordConfirm"] = confirm
		}
		response := f.request(t, "POST", path, body)
		if response.Code != 400 {
			t.Fatalf("native confirmation not enforced: status=%d", response.Code)
		}
		if strings.Contains(response.Body.String(), "SYNTHETIC_INPUT_SENTINEL") || strings.Contains(response.Body.String(), strings.Repeat("A", 43)) {
			t.Fatal("native refusal disclosed input")
		}
		var count int
		if f.app.DB().NewQuery("SELECT count(*) FROM customer_keys").Row(&count) != nil || count != 0 {
			t.Fatal("invalid confirmation saved a credential")
		}
	}
	// SDK limits must run before our first parse, even for trusted admins.
	req := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
	req.ContentLength = 128 << 20
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", f.admin)
	limited := httptest.NewRecorder()
	f.handler.ServeHTTP(limited, req)
	if limited.Code != 413 {
		t.Fatal("stock body limit bypassed")
	}
	f.admin = ""
	guest := f.request(t, "POST", path, map[string]any{"password": "not-a-key", "passwordConfirm": "not-a-key", "account_id": "synthetic-account"})
	if guest.Code != 403 || strings.Contains(guest.Body.String(), "SITE_KEY_INPUT") {
		t.Fatal("stock create authority replaced by key middleware")
	}
	var logs, origins int
	if f.app.AuxDB().NewQuery("SELECT count(*) FROM _logs").Row(&logs) != nil || f.app.DB().NewQuery("SELECT count(*) FROM _authOrigins").Row(&origins) != nil || logs != 0 || origins != 0 {
		t.Fatal("native key flow retained activity")
	}
}

func TestNativeCustomerKeyUpdateAndExactBinding(t *testing.T) {
	f := newKeyFixture(t)
	path := "/api/collections/" + siteauth.CustomerCollectionID + "/records"
	key := strings.Repeat("A", 43)
	lookup, secret := syntheticMaterial(key)
	response := f.request(t, "POST", path, map[string]any{"password": key, "passwordConfirm": key, "account_id": " \t\n\x00synthetic-账户 "})
	if response.Code != 200 {
		t.Fatalf("exact binding refused: status=%d", response.Code)
	}
	record, err := f.app.FindFirstRecordByData(siteauth.CustomerCollection, "key_lookup", lookup)
	if err != nil || record.GetString("account_id") != " \t\n\x00synthetic-账户 " {
		t.Fatal("binding bytes changed")
	}
	adapter, err := siteauth.New(f.app, siteauth.CustomerCollection)
	if err != nil {
		t.Fatal("SDK adapter unavailable")
	}
	token, _, err := adapter.Login(context.Background(), lookup, secret)
	if err != nil {
		t.Fatal("first SDK credential refused")
	}
	other := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 96))
	otherLookup, otherSecret := syntheticMaterial(other)
	bad := f.request(t, "PATCH", path+"/"+record.Id, map[string]any{"password": other, "passwordConfirm": key})
	if bad.Code != 400 {
		t.Fatal("update confirmation bypassed")
	}
	if _, err := adapter.Validate(context.Background(), token); err != nil {
		t.Fatal("refused update changed SDK token")
	}
	updated := f.request(t, "PATCH", path+"/"+record.Id, map[string]any{"password": other, "passwordConfirm": other, "key_lookup": lookup})
	if updated.Code != 200 {
		t.Fatalf("native key update refused: status=%d", updated.Code)
	}
	record, err = f.app.FindRecordById(siteauth.CustomerCollection, record.Id)
	if err != nil || record.GetString("key_lookup") != otherLookup || !record.ValidatePassword(otherSecret) || record.ValidatePassword(secret) {
		t.Fatal("key update lost derived identity/verifier coherence")
	}
	if _, err := adapter.Validate(context.Background(), token); err == nil {
		t.Fatal("stock password update failed to invalidate prior token")
	}
	if _, _, err := adapter.Login(context.Background(), otherLookup, otherSecret); err != nil {
		t.Fatal("updated SDK credential refused")
	}
}
