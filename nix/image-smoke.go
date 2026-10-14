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
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
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
	return exchangeHost("127.0.0.1:8090", method, path, body, status, key, limit)
}

func exchangeHost(host, method, path, body string, status int, key string, limit int64) ([]byte, error) {
	req, err := http.NewRequest(method, "http://"+host+path, bytes.NewBufferString(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if path == "/balance" {
		req.Header.Set("Content-Type", "APPLICATION/JSON; charset=UTF-8")
	}
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
		settingsDB, err := sql.Open("sqlite", "file:/state/data.db?mode=ro")
		if err != nil {
			return fmt.Errorf("persisted logging settings unavailable")
		}
		defer settingsDB.Close()
		var maxDays int
		if err := settingsDB.QueryRow("SELECT json_extract(value, '$.logs.maxDays') FROM _params WHERE id='settings'").Scan(&maxDays); err != nil || maxDays != 0 {
			return fmt.Errorf("persisted SDK logging must be disabled")
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
	if _, err := request(path, "", 403, key); err != nil {
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
	// Trusted stock writes bypass the former held-balance guard. This fake
	// absolute edit is not a funding receipt or a safe concurrent accounting edit.
	raw, err = exchange(http.MethodPatch, path, `{"balance":"89"}`, 200, token, 8192)
	if err != nil || json.Unmarshal(raw, &account) != nil || account.Balance != "89" || bytes.Contains(raw, []byte(key)) || bytes.Contains(raw, []byte(token)) {
		return fmt.Errorf("stock held balance edit unconfirmed")
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
	if err != nil || json.Unmarshal(raw, &balance) != nil || balance.Available != "69" || len(balance.Accounts) != 1 || balance.Accounts[0].Balance != "89" || balance.Accounts[0].Enabled || len(balance.Pending) != 1 || bytes.Contains(raw, []byte(key)) || bytes.Contains(raw, []byte(token)) {
		return fmt.Errorf("custom balance evidence mismatch")
	}
	raw, err = request("/api/logs?superuserRequests=0", "", 200, token)
	var logs struct {
		TotalItems *int              `json:"totalItems"`
		Items      []json.RawMessage `json:"items"`
	}
	if err != nil || json.Unmarshal(raw, &logs) != nil || logs.TotalItems == nil || *logs.TotalItems != 0 || logs.Items == nil || len(logs.Items) != 0 {
		return fmt.Errorf("empty native logs unconfirmed")
	}
	raw, err = request("/api/logs/stats", "", 200, token)
	var stats []json.RawMessage
	if err != nil || json.Unmarshal(raw, &stats) != nil || stats == nil || len(stats) != 0 {
		return fmt.Errorf("empty native log stats unconfirmed")
	}
	var settings struct {
		Logs struct {
			MaxDays *int `json:"maxDays"`
		} `json:"logs"`
	}
	for _, method := range []string{http.MethodPatch, http.MethodGet} {
		body := ""
		if method == http.MethodPatch {
			body = `{"logs":{"maxDays":5}}`
		}
		raw, err = exchange(method, "/api/settings", body, 200, token, 65536)
		settings.Logs.MaxDays = nil
		if err != nil || json.Unmarshal(raw, &settings) != nil || settings.Logs.MaxDays == nil || *settings.Logs.MaxDays != 0 {
			return fmt.Errorf("runtime SDK logging disabled evidence mismatch")
		}
	}
	// Every frozen generated asset, including license texts, must be identical
	// through the Linux operator HTTP surface. This is not browser execution.
	manifest, err := os.ReadFile("/asset-manifest.json")
	var assets map[string]string
	if err != nil || json.Unmarshal(manifest, &assets) != nil || len(assets) == 0 {
		return fmt.Errorf("frozen asset manifest unavailable")
	}
	const iconFont = "fonts/remixicon/remixicon.woff2"
	if _, ok := assets[iconFont]; !ok {
		return fmt.Errorf("frozen icon font unavailable")
	}
	for name, digest := range assets {
		assetPath := "/_/" + name
		if name == "index.html" {
			assetPath = "/_/"
		}
		paths := []string{assetPath}
		if name == iconFont {
			// A locally authored static query must not change SDK asset routing.
			paths = append(paths, assetPath+"?source=image-fixture")
		}
		for _, path := range paths {
			raw, err := exchange(http.MethodGet, path, "", 200, "", 4<<20)
			if err != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != digest || bytes.Contains(raw, []byte(key)) || bytes.Contains(raw, []byte(token)) {
				return fmt.Errorf("native asset binding mismatch")
			}
		}
	}
	return nil
}

// syntheticWildcard proves the actual container listener is reachable on an
// address other than 127.0.0.1. It never prints the address and never opens an
// external network path.
func syntheticWildcard() error {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return fmt.Errorf("synthetic interface evidence unavailable")
	}
	target := ""
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err != nil || ip.IsLoopback() || ip.To4() == nil {
			continue
		}
		target = ip.String()
		break
	}
	if target == "" {
		// A --network none namespace exposes only 127.0.0.1/8. A secondary
		// loopback address still distinguishes a wildcard bind from a
		// loopback-only bind without any external network.
		target = "127.0.0.2"
	}
	raw, err := exchangeHost(target+":8090", http.MethodGet, "/healthz", "", 200, "", 8192)
	if err != nil {
		return err
	}
	view := make(map[string]string)
	if json.Unmarshal(raw, &view) != nil || len(view) != 1 || view["status"] != "ok" {
		return fmt.Errorf("synthetic wildcard listener evidence mismatch")
	}
	return nil
}

// syntheticFresh runs after a restart with no state initialization. A missing
// account then a successful native create prove the state was not persisted.
func syntheticFresh() error {
	key := os.Getenv("LEDGER_ADMIN_KEY")
	raw, err := request("/balance", `{"account_id":"native-image-fixture"}`, 404, key)
	if err != nil {
		return err
	}
	var missing struct {
		Code    string `json:"code"`
		Outcome string `json:"outcome"`
	}
	if json.Unmarshal(raw, &missing) != nil || missing.Code != "LEDGER_ACCOUNT_UNKNOWN" || missing.Outcome != "no_effect" || bytes.Contains(raw, []byte(key)) {
		return fmt.Errorf("synthetic fresh account evidence mismatch")
	}
	login, _ := json.Marshal(map[string]string{"identity": os.Getenv("LEDGER_OPERATOR_EMAIL"), "password": os.Getenv("LEDGER_OPERATOR_PASSWORD")})
	raw, err = request("/api/collections/_superusers/auth-with-password", string(login), 200, "")
	if err != nil {
		return err
	}
	var auth struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &auth) != nil || auth.Token == "" || auth.Token == key {
		return fmt.Errorf("synthetic fresh authentication unconfirmed")
	}
	_, err = request("/api/collections/ledger_accounts/records", `{"id":"","collectionId":"ledgeraccounts1","collectionName":"ledger_accounts","account_id":"native-image-fixture","balance":"100","enabled":true}`, 200, auth.Token)
	return err
}

// syntheticPrivacy inspects only the single disposable scratch directory and
// reads the auxiliary stores read-only while the server is live.
func syntheticPrivacy() error {
	entries, err := os.ReadDir("/tmp")
	if err != nil {
		return fmt.Errorf("synthetic scratch evidence unavailable")
	}
	scratch := ""
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "possums-intake-synthetic-") {
			continue
		}
		if scratch != "" {
			return fmt.Errorf("multiple synthetic scratch directories")
		}
		scratch = entry.Name()
	}
	if scratch == "" {
		return fmt.Errorf("synthetic scratch evidence unavailable")
	}
	for _, check := range []struct{ name, query string }{
		{"data.db", "SELECT COUNT(*) FROM _authOrigins"},
		{"auxiliary.db", "SELECT COUNT(*) FROM _logs"},
	} {
		db, err := sql.Open("sqlite", "file:/tmp/"+scratch+"/"+check.name+"?mode=ro")
		if err != nil {
			return fmt.Errorf("synthetic privacy evidence unavailable")
		}
		var count int
		queryErr := db.QueryRow(check.query).Scan(&count)
		db.Close()
		if queryErr != nil || count != 0 {
			return fmt.Errorf("synthetic privacy evidence mismatch")
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
		raw, err := request("/balance", `{"account_id":"image-fixture","future":{"nested":[true,null],"ignored":"synthetic-extension-fixture"}}`, 200, key)
		if err != nil {
			return err
		}
		var view struct {
			Available string `json:"available"`
			Accounts  []struct {
				Balance string `json:"balance"`
			} `json:"account_balance"`
		}
		if json.Unmarshal(raw, &view) != nil || view.Available != "17" || len(view.Accounts) != 1 || view.Accounts[0].Balance != "17" || bytes.Contains(raw, []byte("synthetic-extension-fixture")) {
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
		raw, err := request("/balance", `{"account_id":{"private":"hostile-fixture"},"unknown":"secret-fixture"}`, 400, key)
		if err != nil || bytes.Contains(raw, []byte("hostile-fixture")) || bytes.Contains(raw, []byte("secret-fixture")) || bytes.Contains(raw, []byte(key)) {
			return fmt.Errorf("unsafe diagnostic")
		}
	case "operator":
		return operator()
	case "synthetic-wildcard":
		return syntheticWildcard()
	case "synthetic-fresh":
		return syntheticFresh()
	case "synthetic-privacy":
		return syntheticPrivacy()
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
