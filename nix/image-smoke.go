// Test-only exec probe: never included in the production image. All state and
// credentials are disposable synthetic fixtures; no production network path.
package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

func request(path, body string, status int, key string) ([]byte, error) {
	method := http.MethodPost
	if path == "/healthz" || body == "" {
		method = http.MethodGet
	}
	return exchange(method, path, body, status, key, 8192)
}

func exchange(method, path, body string, status int, key string, limit int64) ([]byte, error) {
	req, err := http.NewRequest(method, "http://127.0.0.1:8090"+path, bytes.NewBufferString(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if path == "/credits" || path == "/reservations" {
		req.Header.Set("Idempotency-Key", "v1."+strconv.FormatInt(time.Now().UnixMilli(), 10)+".11111111111111111111111111111111")
	}
	client := http.Client{Timeout: 3 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(raw)) > limit || resp.StatusCode != status {
		return nil, fmt.Errorf("unexpected synthetic response")
	}
	return raw, nil
}

func database(mode string) error {
	name := "data.db"
	if mode == "logs" {
		name = "auxiliary.db"
	}
	db, err := sql.Open("sqlite", "file:/state/"+name+"?mode=rw")
	if err != nil {
		return err
	}
	defer db.Close()
	switch mode {
	case "logs":
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM _logs").Scan(&count); err != nil || count != 0 {
			return fmt.Errorf("synthetic activity log must be empty")
		}
	case "unsupported-schema":
		// Structurally valid future metadata, disposable state, server stopped.
		// This is fixture construction, never a production recovery operation.
		_, err = db.Exec("DROP TABLE ledger_meta; CREATE TABLE ledger_meta (singleton INTEGER PRIMARY KEY CHECK(singleton=1), schema_version INTEGER NOT NULL CHECK(schema_version=999), clock_high_water_ms INTEGER NOT NULL); INSERT INTO ledger_meta VALUES(1,999,0)")
	case "schema-drift":
		_, err = db.Exec("DROP INDEX ledger_reservations_expiry")
	case "migration-rollback":
		// Inspect after the image's real init exits under a file-size limit.
		// Bootstrap must have completed, but no v1 DDL may have committed.
		var bootstrap, ledgerObjects int
		if err := db.QueryRow("SELECT COUNT(*) FROM _migrations").Scan(&bootstrap); err != nil || bootstrap == 0 {
			return fmt.Errorf("bootstrap evidence missing")
		}
		if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_schema WHERE name GLOB 'ledger_*'").Scan(&ledgerObjects); err != nil || ledgerObjects != 0 {
			return fmt.Errorf("partial migration committed")
		}
	default:
		return fmt.Errorf("unsupported fixture")
	}
	return err
}

// Actual stock API fields, not an alternative account writer or schema.
func operator() error {
	key := os.Getenv("LEDGER_ADMIN_KEY")
	login, _ := json.Marshal(map[string]string{"identity": os.Getenv("LEDGER_OPERATOR_EMAIL"), "password": os.Getenv("LEDGER_OPERATOR_PASSWORD")})
	raw, err := request("/api/collections/_superusers/auth-with-password", string(login), 200, "")
	if err != nil {
		return err
	}
	var auth struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &auth) != nil || auth.Token == "" || auth.Token == key {
		return fmt.Errorf("native authentication unconfirmed")
	}
	token := auth.Token
	for _, credential := range []string{"", token} {
		if _, err := request("/balance", `{"account_id":"native-image-fixture"}`, 401, credential); err != nil {
			return err
		}
	}
	path := "/api/collections/ledger_accounts/records"
	if _, err := request(path, "", 401, key); err != nil {
		return err
	}
	raw, err = request(path, `{"id":"","collectionId":"ledgeraccounts1","collectionName":"ledger_accounts","account_id":"native-image-fixture","balance":"100","enabled":true}`, 200, token)
	if err != nil {
		return err
	}
	var account struct {
		ID             string `json:"id"`
		AccountID      string `json:"account_id"`
		Balance        string `json:"balance"`
		Enabled        bool   `json:"enabled"`
		CollectionID   string `json:"collectionId"`
		CollectionName string `json:"collectionName"`
	}
	if json.Unmarshal(raw, &account) != nil || len(account.ID) != 15 || account.AccountID != "native-image-fixture" || account.Balance != "100" || !account.Enabled || account.CollectionID != "ledgeraccounts1" || account.CollectionName != "ledger_accounts" {
		return fmt.Errorf("native account evidence mismatch")
	}
	path += "/" + account.ID
	if _, err := exchange(http.MethodPatch, path, `{"balance":"90"}`, 200, token, 8192); err != nil {
		return err
	}
	if _, err := request("/reservations", `{"account_id":"native-image-fixture","maximum":"20","timeout_seconds":600}`, 200, key); err != nil {
		return err
	}
	raw, err = exchange(http.MethodPatch, path, `{"balance":"89"}`, 409, token, 8192)
	var diagnostic struct {
		Code string `json:"code"`
	}
	if err != nil || json.Unmarshal(raw, &diagnostic) != nil || diagnostic.Code != "LEDGER_NATIVE_BALANCE_HELD" || bytes.Contains(raw, []byte(key)) || bytes.Contains(raw, []byte(token)) {
		return fmt.Errorf("held balance refusal unconfirmed")
	}
	if _, err := exchange(http.MethodPatch, path, `{"enabled":false}`, 200, token, 8192); err != nil {
		return err
	}
	raw, err = request("/balance", `{"account_id":"native-image-fixture"}`, 200, key)
	var balance struct {
		Available string `json:"available"`
		Accounts  []struct {
			Balance string `json:"balance"`
			Enabled bool   `json:"enabled"`
		} `json:"account_balance"`
		Pending []json.RawMessage `json:"pending_reservations"`
	}
	if err != nil || json.Unmarshal(raw, &balance) != nil || balance.Available != "70" || len(balance.Accounts) != 1 || balance.Accounts[0].Balance != "90" || balance.Accounts[0].Enabled || len(balance.Pending) != 1 || bytes.Contains(raw, []byte(key)) || bytes.Contains(raw, []byte(token)) {
		return fmt.Errorf("custom balance evidence mismatch")
	}
	for _, denied := range []string{"/api/logs?superuserRequests=0", "/api/logs/stats"} {
		if _, err := request(denied, "", 403, token); err != nil {
			return err
		}
	}
	if _, err := exchange(http.MethodPatch, "/api/settings", `{"logs":{"maxDays":5}}`, 403, token, 8192); err != nil {
		return err
	}
	// Every frozen generated asset, including license texts, must be identical
	// through the Linux operator HTTP surface. This is not browser execution.
	manifest, err := os.ReadFile("/asset-manifest.json")
	var assets map[string]string
	if err != nil || json.Unmarshal(manifest, &assets) != nil || len(assets) == 0 {
		return fmt.Errorf("frozen asset manifest unavailable")
	}
	for name, digest := range assets {
		assetPath := "/_/" + name
		if name == "index.html" {
			assetPath = "/_/"
		}
		raw, err := exchange(http.MethodGet, assetPath, "", 200, "", 4<<20)
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != digest || bytes.Contains(raw, []byte(key)) || bytes.Contains(raw, []byte(token)) {
			return fmt.Errorf("native asset binding mismatch")
		}
	}
	return nil
}

func authOrigins() error {
	db, err := sql.Open("sqlite", "file:/state/data.db?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM _authOrigins").Scan(&count); err != nil || count != 0 {
		return fmt.Errorf("native auth origins must be empty")
	}
	return nil
}

func run(mode string) error {
	key := os.Getenv("LEDGER_ADMIN_KEY")
	switch mode {
	case "health":
		raw, err := request("/healthz", "", 200, "")
		if err != nil {
			return err
		}
		view := make(map[string]string)
		if json.Unmarshal(raw, &view) != nil || len(view) != 1 || view["status"] != "ok" {
			return fmt.Errorf("not ready")
		}
	case "seed":
		if _, err := request("/accounts/provision", `{"account_id":"image-fixture"}`, 200, key); err != nil {
			return err
		}
		_, err := request("/credits", `{"account_id":"image-fixture","amount":"17"}`, 200, key)
		return err
	case "balance":
		raw, err := request("/balance", `{"account_id":"image-fixture"}`, 200, key)
		if err != nil {
			return err
		}
		var view struct {
			Available string `json:"available"`
			Accounts  []struct {
				Balance string `json:"balance"`
			} `json:"account_balance"`
		}
		if json.Unmarshal(raw, &view) != nil || view.Available != "17" || len(view.Accounts) != 1 || view.Accounts[0].Balance != "17" {
			return fmt.Errorf("persistence evidence mismatch")
		}
	case "privacy":
		for _, path := range []string{"/_/", "/api/collections", "/api/settings", "/api/backups", "/api/realtime"} {
			if _, err := request(path, "", 404, ""); err != nil {
				return err
			}
		}
		if _, err := request("/balance", `{"account_id":"image-fixture"}`, 401, ""); err != nil {
			return err
		}
		raw, err := request("/balance", `{"account_id":"hostile-fixture","unknown":"secret-fixture"}`, 400, key)
		if err != nil || bytes.Contains(raw, []byte("hostile-fixture")) || bytes.Contains(raw, []byte("secret-fixture")) || bytes.Contains(raw, []byte(key)) {
			return fmt.Errorf("unsafe diagnostic")
		}
	case "operator":
		return operator()
	case "operator-privacy":
		if err := database("logs"); err != nil {
			return err
		}
		return authOrigins()
	case "logs", "unsupported-schema", "schema-drift", "migration-rollback":
		return database(mode)
	default:
		return fmt.Errorf("unsupported probe")
	}
	return nil
}

func main() {
	if len(os.Args) != 2 || run(os.Args[1]) != nil {
		_, _ = io.WriteString(os.Stderr, "{\"code\":\"IMAGE_PROBE_UNCONFIRMED\",\"stage\":\"synthetic_probe\",\"constraint\":\"expected_local_fixture_evidence\",\"outcome\":\"unknown\",\"next_step\":\"Inspect the isolated image fixture privately; never initialize production state.\"}\n")
		os.Exit(1)
	}
	_, _ = io.WriteString(os.Stdout, "{\"passed\":true,\"stage\":\"synthetic_probe\"}\n")
}
