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
		t.Fatal("operator init unavailable")
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
	var result struct {
		Token  string
		Record struct {
			ID string `json:"id"`
		}
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil || result.Token == "" {
		t.Fatal("native password login failed; output withheld")
	}
	return result.Token
}
func expectClosed(t *testing.T, rec *httptest.ResponseRecorder, status int, stage string) {
	t.Helper()
	var result struct {
		Code, Stage, Constraint, Message string
		NextStep                         string `json:"next_step"`
		Status                           int
	}
	if rec.Code != status || json.Unmarshal(rec.Body.Bytes(), &result) != nil || result.Code != "OPERATOR_NATIVE_REQUEST_UNAVAILABLE" || result.Stage != stage || result.Status != status || result.Constraint == "" || result.NextStep == "" {
		t.Fatal("native status/stage diagnostic mismatch; output withheld")
	}
	if strings.Contains(rec.Body.String(), hostile) || strings.Contains(rec.Body.String(), testPassword) || strings.Contains(rec.Body.String(), testKey) {
		t.Fatal("native diagnostic exposed hostile input")
	}
}

func TestNativeAuthenticationReadGateAndAccountingSeparation(t *testing.T) {
	s, app, h := fixture(t)
	if rec := request(t, h, "GET", authPrefix+"auth-methods", "", ""); rec.Code != 200 || strings.Contains(rec.Body.String(), `"enabled":true,"duration"`) {
		t.Fatal("password-only auth methods unavailable")
	}
	token := login(t, h)
	reads := []string{"/api/settings", "/api/collections?page=1&perPage=500&skipTotal=1", "/api/collections/meta/scaffolds", "/api/collections/meta/oauth2-providers", authPrefix + "records?page=1&perPage=1&fields=id&filter="}
	for _, path := range reads {
		for _, bad := range []string{"", "forged." + hostile, testKey} {
			expectClosed(t, request(t, h, "GET", path, "", bad), 401, "native_read")
		}
		if rec := request(t, h, "GET", path, "", token); rec.Code != 200 {
			t.Fatal("approved authenticated read failed; output withheld")
		}
	}
	if rec := request(t, h, "POST", authPrefix+"auth-refresh", `{}`, token); rec.Code != 200 {
		t.Fatal("native refresh failed")
	}
	expectClosed(t, request(t, h, "POST", authPrefix+"auth-refresh", `{}`, testKey), 401, "native_auth_refresh")
	records, err := app.FindAllRecords(core.CollectionNameSuperusers)
	if err != nil || len(records) != 1 {
		t.Fatal("native superuser count changed")
	}
	if rec := request(t, h, "GET", authPrefix+"records/"+records[0].Id, "", token); rec.Code != 200 {
		t.Fatal("approved record detail failed")
	}
	for _, path := range []string{"/accounts/provision", "/balance"} {
		body := `{"account_id":"synthetic-browser-account"}`
		if rec := request(t, h, "POST", path, body, "Bearer "+token); rec.Code != 401 {
			t.Fatal("native token gained accounting authority")
		}
		if rec := request(t, h, "POST", path, body, "Bearer "+testKey); rec.Code != 200 {
			t.Fatal("existing accounting key authority changed")
		}
	}
	snapshot, err := s.Balance(context.Background(), "synthetic-browser-account")
	if err != nil || snapshot.Available != "0" {
		t.Fatal("fake account was not zero-balance")
	}
	var count int
	if err := app.DB().NewQuery("SELECT count(*) FROM _authOrigins").Row(&count); err != nil || count != 0 {
		t.Fatal("native auth persisted origin fingerprints")
	}
	if err := app.AuxDB().NewQuery("SELECT count(*) FROM _logs").Row(&count); err != nil || count != 0 {
		t.Fatal("native activity logger persisted records")
	}
	if app.Settings().Logs.MaxDays != 0 {
		t.Fatal("framework log retention enabled")
	}
}

func TestNativeMethodPathAliasQueryAndMutationDenials(t *testing.T) {
	_, app, h := fixture(t)
	token := login(t, h)
	collection, _ := app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
	paths := []string{
		"/api/backups", "/api/backups/fixture", "/api/backups/fixture/download?token=" + hostile,
		"/api/logs", "/api/logs?superuserRequests=0", "/api/logs/stats", "/api/logs/stats?superuserRequests=0", "/api/files/token", "/api/files/fixture/file", "/api/sql", "/api/batch", "/api/realtime", "/api/crons", "/api/health", "/api/oauth2-redirect",
		authPrefix + "request-otp", authPrefix + "auth-with-otp", authPrefix + "auth-with-oauth2", authPrefix + "request-password-reset", authPrefix + "confirm-password-reset", authPrefix + "request-verification", authPrefix + "confirm-email-change", authPrefix + "impersonate/aaaaaaaaaaaaaaa",
		"/api/collections/_authOrigins/records", "/api/collections/_mfas/records", "/api/collections/users/records",
		"/api/collections/" + collection.Id + "/auth-with-password", "/api/collections/" + collection.Id + "/records",
		"/api/settings/", "/api//settings", "/api/collections/../settings", "/api/settings?token=" + hostile, "/api/collections?expand=anything", "/api/collections?page=1&page=2", "/api/collections?filter=id%3D%27" + hostile + "%27", "/api/collections?fields=id%2C%40request.headers", "/api/collections?sort=%40collection._authOrigins.id",
		"/_/index.html", "/_/pbinstall/fixture", "/_/request-password-reset", "/_/../api/settings",
	}
	for _, path := range paths {
		for _, method := range []string{"GET", "POST", "PATCH", "PUT", "DELETE", "HEAD", "OPTIONS"} {
			rec := request(t, h, method, path, `{"private":"`+hostile+`"}`, token)
			if rec.Code != 403 || rec.Header().Get("Location") != "" || strings.Contains(rec.Body.String(), hostile) {
				t.Fatal("denied native path reached mux, redirected or leaked; output withheld")
			}
		}
	}
	for _, path := range []string{"/api/settings", "/api/collections", authPrefix + "records", authPrefix + "records/aaaaaaaaaaaaaaa"} {
		for _, method := range []string{"POST", "PATCH", "PUT", "DELETE", "HEAD", "OPTIONS"} {
			expectClosed(t, request(t, h, method, path, `{}`, token), 403, "native_gate")
		}
	}
	for _, path := range []string{"/%61pi/settings", "/api/%73ettings", "/_/%69ndex.html"} {
		expectClosed(t, request(t, h, "GET", path, "", token), 400, "native_gate")
	}
	expectClosed(t, request(t, h, "GET", "/api/settings?", "", token), 400, "native_gate")
}

func TestNativeHostileErrorsPanicsAndParserBoundary(t *testing.T) {
	_, app, h := fixture(t)
	expectClosed(t, request(t, h, "POST", authPrefix+"auth-with-password", `{"identity":"`+hostile+`",`, ""), 400, "native_password_auth")
	expectClosed(t, request(t, h, "POST", authPrefix+"auth-with-password", `{"identity":"`+testEmail+`","password":"`+hostile+`"}`, ""), 400, "native_password_auth")
	expectClosed(t, request(t, h, "POST", authPrefix+"auth-with-password", strings.Repeat(hostile, 300), ""), 413, "native_password_auth")
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

func TestNativeStaticAssetPrivacy(t *testing.T) {
	_, _, h := fixture(t)
	for _, path := range []string{"/_/", "/_/extensions.js"} {
		rec := request(t, h, "GET", path, "", "")
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("Set-Cookie") != "" {
			t.Fatal("static operator header/privacy contract failed")
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
			t.Fatal("embedded asset hash mismatch")
		}
		if strings.HasSuffix(name, ".js") && strings.Contains(string(body), "console.") {
			t.Fatal("compiled UI retained browser console calls")
		}
		rec := request(t, h, "GET", "/_/"+name, "", "")
		if name == "index.html" {
			if rec.Code != 403 {
				t.Fatal("index alias allowed")
			}
			continue
		}
		if rec.Code != 200 {
			t.Fatal("manifest asset not served")
		}
	}
	// Test native hooks over a real HTTP server, not only recorder formatting.
	server := httptest.NewServer(h)
	defer server.Close()
	response, err := http.Post(server.URL+authPrefix+"auth-with-password", "application/json", strings.NewReader(`{"identity":"`+hostile+`",`))
	if err != nil {
		t.Fatal("actual native HTTP unavailable")
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 400 || strings.Contains(string(body), hostile) {
		t.Fatal("actual native HTTP parser leaked")
	}
}
