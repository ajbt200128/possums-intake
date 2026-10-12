// Test-only exec probe: never included in the production image. All state and
// credentials are disposable synthetic fixtures; no production network path.
package main

import (
	"bytes"
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
	req, err := http.NewRequest(method, "http://127.0.0.1:8090"+path, bytes.NewBufferString(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if path == "/credits" {
		req.Header.Set("Idempotency-Key", "v1."+strconv.FormatInt(time.Now().UnixMilli(), 10)+".11111111111111111111111111111111")
	}
	client := http.Client{Timeout: 3 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8193))
	if err != nil || len(raw) > 8192 || resp.StatusCode != status {
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
