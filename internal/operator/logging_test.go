package operator

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/httpapi"
	"possums-intake/internal/ledger"
)

func TestLoggingOffDefaultAndNativeActualHTTP(t *testing.T) {
	for _, native := range []bool{false, true} {
		name := "default"
		if native {
			name = "operator"
		}
		t.Run(name, func(t *testing.T) {
			var app core.App
			var h http.Handler
			var dir string
			if native {
				_, app, h = fixture(t)
				dir = app.DataDir()
				if app.Settings().Logs.MaxDays != 0 {
					t.Fatal("initial native log retention enabled")
				}
				// A stored logging configuration cannot re-enable retention on reload.
				app.Settings().Logs.MaxDays = 5
				if app.Save(app.Settings()) != nil || app.Settings().Logs.MaxDays != 0 {
					t.Fatal("reload logging override failed")
				}
			} else {
				dir = filepath.Join(t.TempDir(), "state")
				if ledger.Init(context.Background(), dir) != nil {
					t.Fatal("default init failed")
				}
				s, err := ledger.Open(context.Background(), dir)
				if err != nil {
					t.Fatal("default open failed")
				}
				t.Cleanup(func() {
					if s.Close() != nil {
						t.Error("default close failed")
					}
				})
				h, err = httpapi.NewHandler(s, s.Ready, testKey, nil)
				if err != nil {
					t.Fatal("default handler failed")
				}
			}
			server := httptest.NewServer(h)
			defer server.Close()
			client := &http.Client{Timeout: 5 * time.Second}
			send := func(method, path, body, token string) (int, []byte) {
				req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
				if err != nil {
					t.Fatal("synthetic request construction failed")
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", token)
				req.Header.Set("User-Agent", hostile)
				response, err := client.Do(req)
				if err != nil {
					t.Fatal("actual HTTP unavailable")
				}
				defer response.Body.Close()
				raw, err := io.ReadAll(io.LimitReader(response.Body, 8192))
				if err != nil {
					t.Fatal("actual HTTP read unavailable")
				}
				if response.StatusCode >= 400 && strings.Contains(string(raw), hostile) {
					t.Fatal("hostile value escaped closed diagnostic")
				}
				return response.StatusCode, raw
			}
			token := ""
			if native {
				status, raw := send("POST", authPrefix+"auth-with-password", `{"identity":"`+testEmail+`","password":"`+testPassword+`"}`, "")
				var auth struct{ Token string }
				if status != 200 || json.Unmarshal(raw, &auth) != nil || auth.Token == "" {
					t.Fatal("actual native login failed")
				}
				token = auth.Token
				id := app.OnRecordAuthWithPasswordRequest().BindFunc(func(e *core.RecordAuthWithPasswordRequestEvent) error { return errors.New(hostile) })
				status, _ = send("POST", authPrefix+"auth-with-password", `{"identity":"`+testEmail+`","password":"`+testPassword+`"}`, "")
				app.OnRecordAuthWithPasswordRequest().Unbind(id)
				if status != 500 {
					t.Fatal("hostile native failure not classified")
				}
				status, _ = send("POST", accountRecords, `{"account_id":"logging-synthetic","balance":"1","enabled":true}`, token)
				if status != 200 {
					t.Fatal("native account admin unavailable")
				}
			}
			want := 404
			if native {
				want = 403
			}
			for _, path := range []string{"/api/logs", "/api/logs?superuserRequests=0", "/api/logs/stats", "/api/logs/stats?superuserRequests=0"} {
				if status, _ := send("GET", path, "", token); status != want {
					t.Fatal("logs/filter route exposed")
				}
			}
			if status, _ := send("PATCH", "/api/settings", `{"logs":{"maxDays":5}}`, token); status != want {
				t.Fatal("authenticated settings write enabled")
			}
			if status, _ := send("POST", "/accounts/provision", `{"account_id":"logging-custom"}`, "Bearer "+testKey); status != 200 {
				t.Fatal("default/custom account provisioning unavailable")
			}
			if status, _ := send("POST", "/balance", `{"account_id":"`+hostile+`"}`, "Bearer "+hostile); status != 401 {
				t.Fatal("hostile custom auth failure not classified")
			}
			for _, fixture := range []struct{ file, table string }{{"auxiliary.db", "_logs"}, {"data.db", "_authOrigins"}} {
				uri := (&url.URL{Scheme: "file", Path: filepath.Join(dir, fixture.file)}).String()
				db, err := dbx.Open("sqlite", uri+"?mode=ro")
				if err != nil {
					t.Fatal("synthetic privacy inspection unavailable")
				}
				var count int
				err = db.NewQuery("SELECT count(*) FROM " + fixture.table).Row(&count)
				closed := db.Close()
				if err != nil || closed != nil || count != 0 {
					t.Fatal("HTTP/native failures persisted activity or origins")
				}
			}
		})
	}
}
