package operator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
	"possums-intake/internal/httpapi"
	"possums-intake/internal/ledger"
)

const testEmail = "synthetic@example.invalid"
const testPassword = "SYNTHETIC_PASSWORD_ONLY_123"
const testKey = "SYNTHETIC_ACCOUNTING_KEY_ONLY_32_BYTES"
const hostile = "SYNTHETIC_PRIVATE_SENTINEL"

func fixture(t *testing.T) (*ledger.Service, core.App, http.Handler) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	if err := ledger.InitOperator(context.Background(), dir, ledger.OperatorCredentials{Email: testEmail, Password: testPassword}); err != nil {
		d := ledger.Failure(err)
		t.Fatalf("synthetic operator init unavailable: code=%s stage=%s constraint=%s outcome=%s", d.Code, d.Stage, d.Constraint, d.Outcome)
	}
	s, err := ledger.Open(context.Background(), dir)
	if err != nil {
		t.Fatal("open unavailable")
	}
	t.Cleanup(func() {
		if s.Close() != nil {
			t.Error("close unavailable")
		}
	})
	app, err := s.OperatorApp()
	if err != nil {
		t.Fatal("same-app access unavailable")
	}
	custom, err := httpapi.NewHandler(s, s.Ready, testKey, nil)
	if err != nil {
		t.Fatal("accounting handler unavailable")
	}
	handler, err := NewHandler(custom, app, s)
	if err != nil {
		t.Fatal("operator handler unavailable")
	}
	return s, app, handler
}
func request(t *testing.T, h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", hostile)
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
func login(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := request(t, h, "POST", authPrefix+"auth-with-password", `{"identity":"`+testEmail+`","password":"`+testPassword+`"}`, "")
	var result struct{ Token string }
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil || result.Token == "" {
		t.Fatalf("native password login failed: status=%d; body withheld", rec.Code)
	}
	return result.Token
}

// Synthetic OTP provisioning avoids mail/provider calls while exercising the
// real stock password+MFA+OTP HTTP controllers. Nothing is logged or echoed.
func loginWithSyntheticOTP(t *testing.T, h http.Handler, app core.App) string {
	t.Helper()
	rec := request(t, h, "POST", authPrefix+"auth-with-password", `{"identity":"`+testEmail+`","password":"`+testPassword+`"}`, "")
	var auth struct {
		Token string
		MFAID string `json:"mfaId"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &auth) != nil {
		t.Fatal("stock auth response unavailable")
	}
	if rec.Code == 200 && auth.Token != "" {
		return auth.Token
	}
	if rec.Code != 401 || auth.MFAID == "" {
		t.Fatal("stock MFA challenge unavailable")
	}
	r, err := app.FindAuthRecordByEmail("_superusers", testEmail)
	if err != nil {
		t.Fatal("synthetic auth fixture unavailable")
	}
	otp := core.NewOTP(app)
	otp.SetRecordRef(r.Id)
	otp.SetCollectionRef(r.Collection().Id)
	otp.SetPassword("123456")
	if app.Save(otp) != nil {
		t.Fatal("synthetic OTP fixture unavailable")
	}
	body, err := json.Marshal(map[string]string{"otpId": otp.Id, "password": "123456", "mfaId": auth.MFAID})
	if err != nil {
		t.Fatal("synthetic OTP encoding unavailable")
	}
	rec = request(t, h, "POST", authPrefix+"auth-with-otp", string(body), "")
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &auth) != nil || auth.Token == "" {
		t.Fatal("stock OTP completion unavailable")
	}
	return auth.Token
}

func expectClosed(t *testing.T, rec *httptest.ResponseRecorder, status int, stage string) {
	t.Helper()
	var result struct {
		Code, Stage, Constraint, Message, Outcome string
		NextStep                                  string `json:"next_step"`
		Status                                    int
	}
	if rec.Code != status || json.Unmarshal(rec.Body.Bytes(), &result) != nil || result.Code != "OPERATOR_NATIVE_REQUEST_UNAVAILABLE" || result.Stage != stage || result.Status != status || result.Constraint == "" || result.NextStep == "" || result.Outcome != "unknown" {
		t.Fatal("native status/stage/outcome diagnostic mismatch; output withheld")
	}
	if strings.Contains(rec.Body.String(), hostile) || strings.Contains(rec.Body.String(), testPassword) || strings.Contains(rec.Body.String(), testKey) {
		t.Fatal("native diagnostic exposed hostile input")
	}
}

func TestNativeSDKAuthenticationAndAccountingSeparation(t *testing.T) {
	s, app, h := fixture(t)
	token := login(t, h)
	for _, path := range []string{"/api/settings", "/api/collections?page=1&perPage=500&skipTotal=1", "/api/collections/meta/scaffolds", "/api/backups", "/api/logs"} {
		for _, bad := range []string{"", "forged." + hostile, testKey} {
			expectClosed(t, request(t, h, "GET", path, "", bad), 401, "native_request")
		}
		if request(t, h, "GET", path, "", token).Code != 200 {
			t.Fatal("authenticated SDK read refused")
		}
	}
	expectClosed(t, request(t, h, "GET", authPrefix+"records", "", ""), 403, "native_request")
	if request(t, h, "POST", authPrefix+"auth-refresh", `{}`, token).Code != 200 {
		t.Fatal("stock refresh failed")
	}
	expectClosed(t, request(t, h, "POST", authPrefix+"auth-refresh", `{}`, testKey), 401, "native_auth_refresh")
	for _, path := range []string{"/accounts/provision", "/balance"} {
		body := `{"account_id":"synthetic-browser-account"}`
		if request(t, h, "POST", path, body, "Bearer "+token).Code != 401 {
			t.Fatal("native token gained custom authority")
		}
		if request(t, h, "POST", path, body, "Bearer "+testKey).Code != 200 {
			t.Fatal("custom key authority changed")
		}
	}
	if snapshot, err := s.Balance(context.Background(), "synthetic-browser-account"); err != nil || snapshot.Available != "0" {
		t.Fatal("account not zero balance")
	}
	var count int
	if app.DB().NewQuery("SELECT count(*) FROM _authOrigins").Row(&count) != nil || count != 0 || app.AuxDB().NewQuery("SELECT count(*) FROM _logs").Row(&count) != nil || count != 0 {
		t.Fatal("native activity persisted")
	}
}

func TestNativeStockAdminSchemaUsersSettingsFilesBatchAndBackups(t *testing.T) {
	_, app, h := fixture(t)
	token := login(t, h)
	// PocketBase already creates users. Exercise it and separately create a new
	// collection through stock HTTP, rather than hiding all but ledger_accounts.
	if request(t, h, "POST", "/api/collections", `{"name":"synthetic_notes","type":"base"}`, token).Code != 200 {
		t.Fatal("native schema create refused")
	}
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal("stock users collection unavailable")
	}
	users.Fields.Add(&core.TextField{Name: "note"}, &core.NumberField{Name: "counter"})
	body, err := json.Marshal(users)
	if err != nil || request(t, h, "PATCH", "/api/collections/users", string(body), token).Code != 200 {
		t.Fatal("stock users schema update refused")
	}
	if request(t, h, "POST", "/api/collections/users/records", `{"id":"ccccccccccccccc","email":"fake@example.invalid","password":"SYNTHETIC_USER_PASSWORD","passwordConfirm":"SYNTHETIC_USER_PASSWORD","note":"first","counter":1}`, token).Code != 200 {
		t.Fatal("native users create refused")
	}
	if request(t, h, "PATCH", "/api/collections/users/records/ccccccccccccccc", `{"note":"second","counter+":2}`, token).Code != 200 {
		t.Fatal("stock modifier refused")
	}
	if rec := request(t, h, "GET", "/api/collections/users/records?filter=note~'second'&fields=id,note,counter", "", token); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"note":"second"`) || !strings.Contains(rec.Body.String(), `"counter":3`) {
		t.Fatal("stock fields/filter/modifier semantics changed")
	}
	if rec := request(t, h, "GET", "/api/collections/users/records", "", ""); rec.Code != 200 || strings.Contains(rec.Body.String(), "ccccccccccccccc") {
		t.Fatal("stock users owner rule exposed an unauthenticated record")
	}
	users, err = app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal("stock schema lookup unavailable")
	}
	users.Fields.Add(&core.TextField{Name: "extra"})
	body, err = json.Marshal(users)
	if err != nil || request(t, h, "PATCH", "/api/collections/users", string(body), token).Code != 200 {
		t.Fatal("stock schema update refused")
	}
	if request(t, h, "PATCH", "/api/settings", `{"meta":{"appName":"Synthetic admin"},"batch":{"enabled":true}}`, token).Code != 200 {
		t.Fatal("stock settings update refused")
	}
	if request(t, h, "POST", "/api/batch", `{"requests":[{"method":"PATCH","url":"/api/collections/users/records/ccccccccccccccc","body":{"extra":"batch"}}]}`, token).Code != 200 {
		t.Fatal("configured stock batch refused")
	}
	if request(t, h, "DELETE", "/api/collections/users/records/ccccccccccccccc", "", token).Code != 204 || request(t, h, "DELETE", "/api/collections/synthetic_notes", "", token).Code != 204 {
		t.Fatal("stock record/schema delete refused")
	}
	// Multiple native admins and MFA/OTP config must not break startup access.
	if request(t, h, "POST", authPrefix+"records", `{"email":"second@example.invalid","password":"SYNTHETIC_SECOND_PASSWORD","passwordConfirm":"SYNTHETIC_SECOND_PASSWORD"}`, token).Code != 200 {
		t.Fatal("second superuser refused")
	}
	if request(t, h, "PATCH", "/api/collections/_superusers", `{"otp":{"enabled":true},"mfa":{"enabled":true},"authAlert":{"enabled":true}}`, token).Code != 200 {
		t.Fatal("stock auth configuration refused")
	}
	c, err := app.FindCollectionByNameOrId("_superusers")
	if err != nil || !c.OTP.Enabled || !c.MFA.Enabled || c.AuthAlert.Enabled {
		t.Fatal("auth features or logging-specific override drifted")
	}
	challenge := request(t, h, "POST", authPrefix+"auth-with-password", `{"identity":"`+testEmail+`","password":"`+testPassword+`"}`, "")
	var mfa struct {
		MFAID string `json:"mfaId"`
	}
	if challenge.Code != 401 || json.Unmarshal(challenge.Body.Bytes(), &mfa) != nil || mfa.MFAID == "" {
		t.Fatal("stock MFA challenge lost")
	}
	token = loginWithSyntheticOTP(t, h, app)
	// Backup generation is isolated disposable synthetic state only. Tokens and
	// archive bytes are first-party data, never printed or diagnostic artifacts.
	if request(t, h, "POST", "/api/backups", `{"name":"synthetic.zip"}`, token).Code != 204 {
		t.Fatal("synthetic stock backup create refused")
	}
	fileToken := request(t, h, "POST", "/api/files/token", `{}`, token)
	var file struct{ Token string }
	if fileToken.Code != 200 || json.Unmarshal(fileToken.Body.Bytes(), &file) != nil || file.Token == "" {
		t.Fatal("stock file token refused")
	}
	if rec := request(t, h, "GET", "/api/backups/synthetic.zip?token="+file.Token, "", ""); rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "PK") {
		t.Fatal("stock token-only backup download refused")
	}
	expectClosed(t, request(t, h, "GET", "/api/backups/synthetic.zip?token="+hostile, "", ""), 403, "native_request")
	if request(t, h, "DELETE", "/api/backups/synthetic.zip", "", token).Code != 204 {
		t.Fatal("stock backup delete refused")
	}
}

func TestNativeHostileErrorsPanicsAndSDKBodyLimit(t *testing.T) {
	_, app, h := fixture(t)
	expectClosed(t, request(t, h, "POST", authPrefix+"auth-with-password", `{"identity":"`+hostile+`",`, ""), 400, "native_password_auth")
	expectClosed(t, request(t, h, "POST", authPrefix+"auth-with-password", `{"identity":"`+testEmail+`","password":"`+hostile+`"}`, ""), 400, "native_password_auth")
	// Native SDK 32 MiB limit, not the custom accounting 4 KiB cap.
	expectClosed(t, request(t, h, "POST", authPrefix+"auth-with-password", strings.Repeat(" ", int(apis.DefaultMaxBodySize)+1), ""), 413, "native_password_auth")
	if rec := request(t, h, "POST", authPrefix+"auth-with-password", strings.Repeat(" ", 5000)+`{"identity":"`+testEmail+`","password":"`+testPassword+`"}`, ""); rec.Code != 200 {
		t.Fatal("native project body cap remains")
	}
	for _, status := range []int{400, 401, 403, 429, 503} {
		id := app.OnRecordAuthWithPasswordRequest().BindFunc(func(e *core.RecordAuthWithPasswordRequestEvent) error {
			return router.NewApiError(status, hostile, errors.New(hostile))
		})
		expectClosed(t, request(t, h, "POST", authPrefix+"auth-with-password", `{"identity":"`+testEmail+`","password":"`+testPassword+`"}`, ""), status, "native_password_auth")
		app.OnRecordAuthWithPasswordRequest().Unbind(id)
	}
	for _, mode := range []string{"error", "panic"} {
		id := app.OnRecordAuthWithPasswordRequest().BindFunc(func(e *core.RecordAuthWithPasswordRequestEvent) error {
			if mode == "panic" {
				panic(hostile)
			}
			return errors.New(hostile)
		})
		expectClosed(t, request(t, h, "POST", authPrefix+"auth-with-password", `{"identity":"`+testEmail+`","password":"`+testPassword+`"}`, ""), 500, "native_password_auth")
		app.OnRecordAuthWithPasswordRequest().Unbind(id)
	}
}

func TestNativeStaticAssetPrivacyAndSDKQueries(t *testing.T) {
	_, _, h := fixture(t)
	for _, path := range []string{"/_/", "/_/extensions.js"} {
		rec := request(t, h, "GET", path, "", "")
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("Set-Cookie") != "" {
			t.Fatal("asset privacy contract failed")
		}
	}
	var manifest map[string]string
	raw, err := os.ReadFile("ui/assets.json")
	if err != nil || json.Unmarshal(raw, &manifest) != nil {
		t.Fatal("asset manifest unavailable")
	}
	for name, want := range manifest {
		body, err := nativeAssets.ReadFile("ui/dist/" + name)
		sum := sha256.Sum256(body)
		if err != nil || hex.EncodeToString(sum[:]) != want {
			t.Fatal("asset hash mismatch")
		}
		if strings.HasSuffix(name, ".js") && strings.Contains(string(body), "console.") {
			t.Fatal("asset console fallback")
		}
		rec := request(t, h, "GET", "/_/"+name+"?v=synthetic-font-version", "", "")
		if name == "index.html" {
			if rec.Code != 301 {
				t.Fatal("SDK index redirect changed")
			}
			continue
		}
		if rec.Code != 200 {
			t.Fatal("SDK asset query refused")
		}
	}
	server := httptest.NewServer(h)
	defer server.Close()
	response, err := http.Post(server.URL+authPrefix+"auth-with-password", "application/json", strings.NewReader(`{"identity":"`+hostile+`",`))
	if err != nil {
		t.Fatal("actual HTTP unavailable")
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 400 || strings.Contains(string(body), hostile) {
		t.Fatal("HTTP parser leaked")
	}
}
