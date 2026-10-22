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
	"github.com/pocketbase/pocketbase/mails"
	"possums-intake/internal/httpapi"
	"possums-intake/internal/ledger"
	"possums-intake/internal/testfixture"
)

func TestLoggingOffDefaultAndNativeActualHTTP(t *testing.T) {
	for _, native := range []bool{false, true} {
		name := "default"
		if native {
			name = "operator"
		}
		t.Run(name, func(t *testing.T) {
			var service *ledger.Service
			var app core.App
			var h http.Handler
			var dir string
			if native {
				service, app, h = fixture(t)
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
				service = s
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
			if _, err := testfixture.Create(context.Background(), service, "logging00000000"); err != nil {
				t.Fatal("synthetic auth account unavailable")
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
				status, _ = send("POST", accountRecords, nativeAccountBody(t, `{"balance":"1","enabled":true}`), token)
				if status != 200 {
					t.Fatal("native account admin unavailable")
				}
			}
			want := 404
			if native {
				want = 200
			}
			for _, path := range []string{"/api/logs", "/api/logs?superuserRequests=0", "/api/logs/stats", "/api/logs/stats?superuserRequests=0"} {
				if status, _ := send("GET", path, "", token); status != want {
					t.Fatal("logs/filter SDK route status changed")
				}
			}
			if status, _ := send("PATCH", "/api/settings", `{"logs":{"maxDays":5}}`, token); status != want {
				t.Fatal("native settings SDK status changed")
			}
			if native && app.Settings().Logs.MaxDays != 0 {
				t.Fatal("native settings write enabled logging")
			}
			if status, _ := send("POST", "/balance", `{"account_id":"logging00000000"}`, "Bearer "+testKey); status != 200 {
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

func TestNativeSQLCannotReenableSDKAuthActivity(t *testing.T) {
	_, app, h := fixture(t)
	token := login(t, h)
	if request(t, h, "POST", "/api/sql", `{"query":"UPDATE _collections SET options=json_set(options, '$.authAlert.enabled', json('true')) WHERE name='_superusers'"}`, token).Code != 200 || app.ReloadCachedCollections() != nil {
		t.Fatal("trusted SDK SQL or collection reload unavailable")
	}
	collection, err := app.FindCachedCollectionByNameOrId("_superusers")
	if err != nil || !collection.AuthAlert.Enabled {
		t.Fatal("trusted manual SQL was intercepted")
	}
	login(t, h)
	if request(t, h, "POST", "/api/sql", `{"query":"UPDATE _params SET value=json_set(value, '$.logs.maxDays', 10) WHERE id='settings'"}`, token).Code != 200 || app.ReloadSettings() != nil || app.Settings().Logs.MaxDays != 0 {
		t.Fatal("manual settings enabled runtime SDK logger")
	}
	app.Logger().Error("SYNTHETIC_PRIVATE_SENTINEL")
	var count int
	if app.AuxDB().NewQuery("SELECT count(*) FROM _logs").Row(&count) != nil || count != 0 {
		t.Fatal("manual settings enabled SDK error recording")
	}
	if app.DB().NewQuery("SELECT count(*) FROM _authOrigins").Row(&count) != nil || count != 0 {
		t.Fatal("manual metadata enabled SDK origin recording")
	}
	// A supported SDK alert-mail call must also stop before transport. The
	// second hook is a no-network sentinel even if the privacy hook regresses.
	mailCalls := 0
	app.OnMailerRecordAuthAlertSend().BindFunc(func(*core.MailerRecordEvent) error { mailCalls++; return nil })
	record, err := app.FindAuthRecordByEmail(collection, testEmail)
	if err != nil || mails.SendRecordAuthAlert(app, record, "SYNTHETIC_PRIVATE_SENTINEL") != nil || mailCalls != 0 {
		t.Fatal("SDK auth alert crossed disabled activity boundary")
	}
	response := request(t, h, "POST", "/api/sql", `{"query":"SELECT * FROM SYNTHETIC_PRIVATE_SENTINEL"}`, token)
	if response.Code != 400 || strings.Contains(response.Body.String(), "SYNTHETIC_PRIVATE_SENTINEL") || !strings.Contains(response.Body.String(), "OPERATOR_NATIVE_REQUEST_UNAVAILABLE") {
		t.Fatal("stock SQL error escaped closed diagnostics")
	}
}

func TestNativeLoggingSettingsPersistOffAcrossRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if ledger.InitOperator(context.Background(), dir, ledger.OperatorCredentials{Email: testEmail, Password: testPassword}) != nil {
		t.Fatal("synthetic init unavailable")
	}
	for pass := 0; pass < 2; pass++ {
		s, err := ledger.Open(context.Background(), dir)
		if err != nil {
			t.Fatal("synthetic reopen unavailable")
		}
		app, err := s.OperatorApp()
		if err != nil {
			s.Close()
			t.Fatal("operator access refused after native auth changes")
		}
		custom, err := httpapi.NewHandler(s, s.Ready, testKey, nil)
		if err != nil {
			s.Close()
			t.Fatal("custom handler unavailable")
		}
		h, err := NewHandler(custom, app, s)
		if err != nil {
			s.Close()
			t.Fatal("native handler unavailable")
		}
		token := loginWithSyntheticOTP(t, h, app)
		if request(t, h, "PATCH", "/api/settings", `{"logs":{"maxDays":10,"logIP":true,"logAuthId":true}}`, token).Code != 200 || app.Settings().Logs.MaxDays != 0 {
			s.Close()
			t.Fatal("native logging enablement not overridden")
		}
		if request(t, h, "PATCH", "/api/collections/_superusers", `{"authAlert":{"enabled":true},"otp":{"enabled":true}}`, token).Code != 200 {
			s.Close()
			t.Fatal("auth configuration unavailable")
		}
		// A login after attempted AuthAlert enablement must not record IP/UA.
		loginWithSyntheticOTP(t, h, app)
		app.Logger().Error("SYNTHETIC_PRIVATE_SENTINEL")
		if app.ReloadSettings() != nil || app.Settings().Logs.MaxDays != 0 {
			s.Close()
			t.Fatal("settings reload enabled SDK logger")
		}
		var raw string
		var saved struct {
			Logs struct {
				MaxDays int `json:"maxDays"`
			} `json:"logs"`
		}
		if app.DB().NewQuery("SELECT value FROM _params WHERE id='settings'").Row(&raw) != nil || json.Unmarshal([]byte(raw), &saved) != nil || saved.Logs.MaxDays != 0 {
			s.Close()
			t.Fatal("persisted SDK settings advertise logging enabled")
		}
		c, err := app.FindCollectionByNameOrId("_superusers")
		var count int
		if err != nil || c.AuthAlert.Enabled || !c.OTP.Enabled || app.DB().NewQuery("SELECT count(*) FROM _authOrigins").Row(&count) != nil || count != 0 || app.AuxDB().NewQuery("SELECT count(*) FROM _logs").Row(&count) != nil || count != 0 {
			s.Close()
			t.Fatal("native login activity persisted")
		}
		if s.Close() != nil {
			t.Fatal("joined close unavailable")
		}
	}
}
