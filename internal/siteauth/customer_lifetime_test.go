package siteauth_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/ledger"
	"possums-intake/internal/operator"
	"possums-intake/internal/siteauth"
)

// Approved paired lifetime: native customer auth tokens last 12 hours while the
// independent website session stays at one hour absolute / ten minutes idle.
const customerLifetime = 12 * time.Hour
const originalLifetime = time.Hour
const syntheticKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func tokenExpiry(t *testing.T, token string) time.Time {
	t.Helper()
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		t.Fatal("SYNTHETIC_TOKEN_CLAIMS_UNAVAILABLE")
	}
	claimsExpiry, err := claims.GetExpirationTime()
	if err != nil || claimsExpiry == nil {
		t.Fatal("SYNTHETIC_TOKEN_EXPIRY_UNAVAILABLE")
	}
	return claimsExpiry.Time
}

func assertLifetime(t *testing.T, token string, want time.Duration) {
	t.Helper()
	if delta := tokenExpiry(t, token).Sub(time.Now().Add(want)); delta > time.Minute || delta < -time.Minute {
		t.Fatalf("SYNTHETIC_TOKEN_LIFETIME_MISMATCH want=%s", want)
	}
}

func setNativeDuration(t *testing.T, app core.App, seconds int64) {
	t.Helper()
	c, err := app.FindCollectionByNameOrId(siteauth.CustomerCollection)
	if err != nil {
		t.Fatal("SYNTHETIC_CUSTOMER_COLLECTION_UNAVAILABLE")
	}
	c.AuthToken.Duration = seconds
	if app.Save(c) != nil {
		t.Fatal("SYNTHETIC_DURATION_PRECONDITION_UNAVAILABLE")
	}
}

func createSyntheticCustomer(t *testing.T, f *keyFixture) string {
	t.Helper()
	response := f.request(t, "POST", "/api/collections/"+siteauth.CustomerCollection+"/records", map[string]any{"password": syntheticKey, "passwordConfirm": syntheticKey, "account_id": "synthetic-lifetime-account"})
	var created struct {
		ID string `json:"id"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &created) != nil || created.ID == "" {
		t.Fatal("SYNTHETIC_CUSTOMER_PROVISION_UNAVAILABLE")
	}
	return created.ID
}

func customerAdapter(t *testing.T, app core.App) *siteauth.CustomerKeyAdapter {
	t.Helper()
	a, err := siteauth.NewCustomer(app)
	if err != nil {
		t.Fatal("SYNTHETIC_CUSTOMER_ADAPTER_UNAVAILABLE")
	}
	return a
}

// Qualification of the approved existing-installation update: one stock native
// collection PATCH that changes only authToken.duration, exercised on disposable
// fake state through the real project native HTTP path.
func TestCustomerTokenLifetimeNativeUpdate(t *testing.T) {
	f := newKeyFixture(t)
	app := f.app

	// Fresh explicit initialization selects the approved default.
	fresh, err := app.FindCollectionByNameOrId(siteauth.CustomerCollection)
	if err != nil || fresh.AuthToken.Duration != int64(customerLifetime.Seconds()) {
		t.Fatalf("fresh customer token duration mismatch: %d", fresh.AuthToken.Duration)
	}
	if defaults := core.NewAuthCollection("synthetic_reference_defaults"); fresh.AuthToken.Duration == defaults.AuthToken.Duration ||
		fresh.MFA.Duration != defaults.MFA.Duration || fresh.OTP.Duration != defaults.OTP.Duration || fresh.OTP.Length != defaults.OTP.Length ||
		fresh.PasswordResetToken.Duration != defaults.PasswordResetToken.Duration || fresh.EmailChangeToken.Duration != defaults.EmailChangeToken.Duration ||
		fresh.VerificationToken.Duration != defaults.VerificationToken.Duration || fresh.FileToken.Duration != defaults.FileToken.Duration {
		t.Fatal("customer initialization changed unrelated token settings")
	}

	// Model the pre-existing installation's stored one-hour duration.
	setNativeDuration(t, app, int64(originalLifetime.Seconds()))
	recordID := createSyntheticCustomer(t, f)
	adapter := customerAdapter(t, app)
	oldToken, oldPrincipal, err := adapter.Login(context.Background(), syntheticKey)
	if err != nil || oldPrincipal.AccountID != "synthetic-lifetime-account" {
		t.Fatal("SYNTHETIC_ORIGINAL_LOGIN_UNAVAILABLE")
	}
	assertLifetime(t, oldToken, originalLifetime)
	oldExpiry := tokenExpiry(t, oldToken)

	before, err := app.FindCollectionByNameOrId(siteauth.CustomerCollection)
	if err != nil {
		t.Fatal("SYNTHETIC_CUSTOMER_COLLECTION_UNAVAILABLE")
	}
	record, err := app.FindRecordById(siteauth.CustomerCollection, recordID)
	if err != nil {
		t.Fatal("SYNTHETIC_CUSTOMER_RECORD_UNAVAILABLE")
	}
	secret, tokenKey, passwordHash := before.AuthToken.Secret, record.TokenKey(), record.GetString("password:hash")

	// The stock editor requires SDK superuser authentication; no new gate.
	admin := f.admin
	f.admin = ""
	if guest := f.request(t, "PATCH", "/api/collections/"+siteauth.CustomerCollection, map[string]any{"authToken": map[string]any{"duration": 43200}}); guest.Code != 401 && guest.Code != 403 {
		t.Fatalf("guest collection update not refused: status=%d", guest.Code)
	}
	f.admin = admin

	// A duration below SDK validation stays refused and changes nothing.
	if invalid := f.request(t, "PATCH", "/api/collections/"+siteauth.CustomerCollection, map[string]any{"authToken": map[string]any{"duration": 5}}); invalid.Code != 400 {
		t.Fatalf("unsupported duration not refused: status=%d", invalid.Code)
	}

	update := func() *core.Collection {
		t.Helper()
		response := f.request(t, "PATCH", "/api/collections/"+siteauth.CustomerCollection, map[string]any{"authToken": map[string]any{"duration": int64(customerLifetime.Seconds())}})
		if response.Code != http.StatusOK {
			t.Fatalf("native duration update refused: status=%d", response.Code)
		}
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("native update response released the signing secret")
		}
		current, err := app.FindCollectionByNameOrId(siteauth.CustomerCollection)
		if err != nil {
			t.Fatal("SYNTHETIC_CUSTOMER_COLLECTION_UNAVAILABLE")
		}
		return current
	}
	after := update()
	if after.AuthToken.Duration != int64(customerLifetime.Seconds()) || after.AuthToken.Secret != secret {
		t.Fatal("duration update changed the signing secret or duration")
	}
	// Only the duration moved: identity, rules, bindings and other token settings.
	if after.Name != before.Name || after.Id != before.Id || after.Type != before.Type || len(after.Fields) != len(before.Fields) ||
		castRule(after.AuthRule) != castRule(before.AuthRule) || !reflect.DeepEqual(after.PasswordAuth, before.PasswordAuth) ||
		!reflect.DeepEqual(after.MFA, before.MFA) || !reflect.DeepEqual(after.OTP, before.OTP) || after.AuthAlert.Enabled ||
		after.PasswordResetToken.Duration != before.PasswordResetToken.Duration || after.EmailChangeToken.Duration != before.EmailChangeToken.Duration ||
		after.VerificationToken.Duration != before.VerificationToken.Duration || after.FileToken.Duration != before.FileToken.Duration ||
		after.ListRule != nil || after.ViewRule != nil || after.CreateRule != nil || after.UpdateRule != nil || after.DeleteRule != nil || after.ManageRule != nil {
		t.Fatal("duration update changed unrelated native auth settings")
	}
	record, err = app.FindRecordById(siteauth.CustomerCollection, recordID)
	if err != nil || record.TokenKey() != tokenKey || record.GetString("password:hash") != passwordHash || record.GetString("account_id") != "synthetic-lifetime-account" {
		t.Fatal("duration update changed recorded credential or binding")
	}

	// The already-issued one-hour token keeps its original expiry and validity.
	principal, err := adapter.Validate(context.Background(), oldToken)
	if err != nil || principal.AccountID != "synthetic-lifetime-account" || !tokenExpiry(t, oldToken).Equal(oldExpiry) {
		t.Fatal("duration update altered an existing customer token")
	}
	// New logins receive the approved twelve-hour lifetime.
	newToken, newPrincipal, err := adapter.Login(context.Background(), syntheticKey)
	if err != nil || newPrincipal.AccountID != "synthetic-lifetime-account" || newToken == oldToken {
		t.Fatal("SYNTHETIC_UPDATED_LOGIN_UNAVAILABLE")
	}
	assertLifetime(t, newToken, customerLifetime)

	// Repeating the same supported update is safe and changes nothing.
	repeated := update()
	if repeated.AuthToken.Duration != int64(customerLifetime.Seconds()) || repeated.AuthToken.Secret != secret {
		t.Fatal("repeated duration update was not idempotent")
	}
	if _, err := adapter.Validate(context.Background(), newToken); err != nil {
		t.Fatal("repeated duration update invalidated a current token")
	}

	// Existing MFA stays enabled and the website still fails closed.
	enabled := repeated
	enabled.MFA.Enabled, enabled.OTP.Enabled = true, true
	if app.Save(enabled) != nil {
		t.Fatal("SYNTHETIC_MFA_UPDATE_UNAVAILABLE")
	}
	if _, _, err := adapter.Login(context.Background(), syntheticKey); err == nil {
		t.Fatal("unsupported MFA granted customer auth after the lifetime update")
	}
	if challengeCount(t, app) != 0 {
		t.Fatal("lifetime update created MFA challenges")
	}
	enabled.MFA.Enabled, enabled.OTP.Enabled = false, false
	if app.Save(enabled) != nil {
		t.Fatal("SYNTHETIC_MFA_RESTORE_UNAVAILABLE")
	}

	// Stock revocation still applies to the twelve-hour token.
	record, err = app.FindRecordById(siteauth.CustomerCollection, recordID)
	if err != nil {
		t.Fatal("SYNTHETIC_CUSTOMER_RECORD_UNAVAILABLE")
	}
	record.RefreshTokenKey()
	if app.Save(record) != nil {
		t.Fatal("SYNTHETIC_TOKEN_KEY_ROTATION_UNAVAILABLE")
	}
	if _, err := adapter.Validate(context.Background(), newToken); err == nil {
		t.Fatal("token key rotation left a twelve-hour token valid")
	}

	// Logging and AuthAlert origins stay off through the whole path.
	var logs, origins int
	if app.AuxDB().NewQuery("SELECT count(*) FROM _logs").Row(&logs) != nil || app.DB().NewQuery("SELECT count(*) FROM _authOrigins").Row(&origins) != nil || logs != 0 || origins != 0 {
		t.Fatal("lifetime update retained activity or login origins")
	}
	assertNoKeyBytes(t, f.dir, syntheticKey)
}

// The additive initializer must not silently rewrite an installation's stored
// duration; only the explicit native update above changes it.
func TestCustomerInitializerPreservesStoredDuration(t *testing.T) {
	f := newKeyFixture(t)
	for _, existing := range []int64{int64(originalLifetime.Seconds()), 7200, int64(customerLifetime.Seconds())} {
		setNativeDuration(t, f.app, existing)
		if err := siteauth.InitializeCustomerAuth(f.app); err != nil {
			t.Fatal("compatible repeated initialization unavailable")
		}
		c, err := f.app.FindCollectionByNameOrId(siteauth.CustomerCollection)
		if err != nil || c.AuthToken.Duration != existing {
			t.Fatalf("initialization rewrote an existing duration: got=%d want=%d", c.AuthToken.Duration, existing)
		}
	}
}

// Restart persistence: the approved duration and the tokens issued under it
// survive a real ledger close/reopen of disposable synthetic state.
func TestCustomerStoredDurationRestart(t *testing.T) {
	ctx, dir := context.Background(), t.TempDir()
	const email, password = "customer-lifetime@example.invalid", "synthetic-lifetime-native-password"
	if ledger.InitOperator(ctx, dir, ledger.OperatorCredentials{Email: email, Password: password}) != nil {
		t.Fatal("SYNTHETIC_LIFETIME_INIT_UNAVAILABLE")
	}
	key := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x04}, 32))
	open := func() (*ledger.Service, core.App) {
		t.Helper()
		s, err := ledger.Open(ctx, dir)
		if err != nil {
			t.Fatal("SYNTHETIC_LIFETIME_OPEN_UNAVAILABLE")
		}
		app, err := s.OperatorApp()
		if err != nil {
			t.Fatal("SYNTHETIC_LIFETIME_APP_UNAVAILABLE")
		}
		return s, app
	}
	s, app := open()
	if siteauth.InitializeCustomerAuth(app) != nil {
		t.Fatal("SYNTHETIC_LIFETIME_SCHEMA_UNAVAILABLE")
	}
	// Model an existing installation whose stored duration is still one hour.
	c, err := app.FindCollectionByNameOrId(siteauth.CustomerCollection)
	if err != nil {
		t.Fatal("SYNTHETIC_LIFETIME_COLLECTION_UNAVAILABLE")
	}
	c.AuthToken.Duration = 3600
	if app.Save(c) != nil {
		t.Fatal("SYNTHETIC_LIFETIME_PRECONDITION_UNAVAILABLE")
	}
	lookup, secret := syntheticMaterial(key)
	record := core.NewRecord(c)
	record.Set("account_id", "synthetic-lifetime-account")
	record.Set("key_lookup", lookup)
	record.SetPassword(secret)
	if app.Save(record) != nil {
		t.Fatal("SYNTHETIC_LIFETIME_RECORD_UNAVAILABLE")
	}
	handler, err := operator.NewHandler(http.NotFoundHandler(), app, s)
	if err != nil {
		t.Fatal("SYNTHETIC_LIFETIME_ROUTER_UNAVAILABLE")
	}
	var admin string
	request := func(method, path string, body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal("SYNTHETIC_LIFETIME_ENCODING_UNAVAILABLE")
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", admin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	signIn := request("POST", "/api/collections/_superusers/auth-with-password", map[string]any{"identity": email, "password": password})
	var auth struct {
		Token string `json:"token"`
	}
	if signIn.Code != 200 || json.Unmarshal(signIn.Body.Bytes(), &auth) != nil || auth.Token == "" {
		t.Fatal("SYNTHETIC_LIFETIME_ADMIN_LOGIN_UNAVAILABLE")
	}
	admin = auth.Token
	// One stock native collection PATCH, the approved update design.
	if w := request("PATCH", "/api/collections/"+siteauth.CustomerCollection, map[string]any{"authToken": map[string]any{"duration": int64(customerLifetime.Seconds())}}); w.Code != 200 {
		t.Fatalf("SYNTHETIC_LIFETIME_UPDATE_REFUSED status=%d", w.Code)
	}
	adapter := customerAdapter(t, app)
	token, _, err := adapter.Login(ctx, key)
	if err != nil {
		t.Fatal("SYNTHETIC_LIFETIME_LOGIN_UNAVAILABLE")
	}
	assertLifetime(t, token, customerLifetime)
	if s.Close() != nil {
		t.Fatal("SYNTHETIC_LIFETIME_CLOSE_UNAVAILABLE")
	}

	// Restart: reopen the same synthetic state without re-initializing.
	s, app = open()
	defer func() {
		if s.Close() != nil {
			t.Error("SYNTHETIC_LIFETIME_REOPEN_CLOSE_UNAVAILABLE")
		}
	}()
	c, err = app.FindCollectionByNameOrId(siteauth.CustomerCollection)
	if err != nil || c.AuthToken.Duration != int64(customerLifetime.Seconds()) {
		t.Fatal("SYNTHETIC_LIFETIME_DURATION_DID_NOT_PERSIST")
	}
	adapter = customerAdapter(t, app)
	if p, err := adapter.Validate(ctx, token); err != nil || p.AccountID != "synthetic-lifetime-account" {
		t.Fatal("SYNTHETIC_LIFETIME_TOKEN_DID_NOT_SURVIVE_RESTART")
	}
	restarted, _, err := adapter.Login(ctx, key)
	if err != nil {
		t.Fatal("SYNTHETIC_LIFETIME_RESTART_LOGIN_UNAVAILABLE")
	}
	assertLifetime(t, restarted, customerLifetime)
	// The additive initializer still leaves the stored duration untouched.
	if err := siteauth.InitializeCustomerAuth(app); err != nil {
		t.Fatal("SYNTHETIC_LIFETIME_REINIT_UNAVAILABLE")
	}
	if c, err = app.FindCollectionByNameOrId(siteauth.CustomerCollection); err != nil || c.AuthToken.Duration != int64(customerLifetime.Seconds()) {
		t.Fatal("SYNTHETIC_LIFETIME_REINIT_REWROTE_DURATION")
	}
	var logs, origins int
	if app.AuxDB().NewQuery("SELECT count(*) FROM _logs").Row(&logs) != nil || app.DB().NewQuery("SELECT count(*) FROM _authOrigins").Row(&origins) != nil || logs != 0 || origins != 0 {
		t.Fatal("SYNTHETIC_LIFETIME_RESTART_RETAINED_ACTIVITY")
	}
	assertNoKeyBytes(t, dir, key, secret)
}

func castRule(rule *string) string {
	if rule == nil {
		return "\x00nil"
	}
	return *rule
}

func challengeCount(t *testing.T, app core.App) int {
	t.Helper()
	var count int
	if app.DB().NewQuery("SELECT count(*) FROM _mfas").Row(&count) != nil {
		t.Fatal("SYNTHETIC_MFA_STATE_UNAVAILABLE")
	}
	return count
}
