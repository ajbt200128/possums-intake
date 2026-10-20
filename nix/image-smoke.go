// Test-only exec probe: never included in the production image. All state and
// credentials are disposable synthetic fixtures; no production network path.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"possums-intake/internal/website"
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
	case "website":
		return websiteSmoke(false)
	case "website-restart":
		return websiteSmoke(true)
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

// siteClient deliberately transports HTTPS-origin synthetic requests over the
// private HTTP backend. Secure cookies are handled explicitly, never downgraded
// by a cookie jar or by changing application/forwarded-header policy.
type siteClient struct {
	cookies map[string]*http.Cookie
}

func (c *siteClient) do(method, path string, form url.Values, status int, location string) ([]byte, error) {
	req, err := http.NewRequest(method, "http://127.0.0.1:8090"+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("website request unavailable")
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "https://"+req.URL.Host)
	}
	for _, cookie := range c.cookies {
		req.AddCookie(cookie)
	}
	client := http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("website transport unconfirmed")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 || resp.StatusCode != status || resp.Header.Get("Location") != location {
		return nil, fmt.Errorf("website response unconfirmed")
	}
	if !strings.HasPrefix(path, "/_site/") && resp.Header.Get("Cache-Control") != "no-store" {
		return nil, fmt.Errorf("website cache policy unconfirmed")
	}
	for _, cookie := range resp.Cookies() {
		if (cookie.Name != "__Host-possums" && cookie.Name != "__Host-possums-csrf") || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Domain != "" || cookie.Path != "/" || cookie.MaxAge > 0 || (!cookie.Expires.IsZero() && cookie.MaxAge >= 0) {
			return nil, fmt.Errorf("website cookie policy unconfirmed")
		}
		if c.cookies == nil {
			c.cookies = make(map[string]*http.Cookie)
		}
		if cookie.MaxAge < 0 {
			delete(c.cookies, cookie.Name)
		} else {
			c.cookies[cookie.Name] = cookie
		}
	}
	return raw, nil
}

func siteForm(raw []byte) (url.Values, error) {
	form := url.Values{}
	for _, name := range []string{"csrf_token", "revision"} {
		pattern := regexp.MustCompile(`name="` + name + `" value="([^"]+)"`)
		matches := pattern.FindAllSubmatch(raw, -1)
		if len(matches) != 1 {
			return nil, fmt.Errorf("website form evidence unconfirmed")
		}
		form.Set(name, html.UnescapeString(string(matches[0][1])))
	}
	return form, nil
}

func websiteKey(index int) string {
	// Valid 96-character fake keys exercise the native pre-form adapter beyond
	// the SDK's raw 71-character password bound. Never print these values.
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(81 + index)}, 72))
}

func siteLogin(c *siteClient, key string) error {
	raw, err := c.do("GET", "/login", nil, 200, "")
	if err != nil {
		return err
	}
	form, err := siteForm(raw)
	if err != nil {
		return err
	}
	form.Set("key", key)
	_, err = c.do("POST", "/login", form, 303, "/home")
	if err != nil || c.cookies["__Host-possums"] == nil {
		return fmt.Errorf("website login unconfirmed")
	}
	return nil
}

func siteSnapshot(c *siteClient, account, other string) error {
	body, _ := json.Marshal(map[string]string{"account_id": account})
	raw, err := request("/balance", string(body), 200, os.Getenv("LEDGER_ADMIN_KEY"))
	var snapshot struct {
		Available string `json:"available"`
		Accounts  []struct {
			Account string `json:"account_id"`
			Balance string `json:"balance"`
			Enabled bool   `json:"enabled"`
		} `json:"account_balance"`
		Pending []struct {
			ID       string  `json:"reservation_id"`
			Key      string  `json:"idempotency_key"`
			Account  string  `json:"account_id"`
			Maximum  string  `json:"maximum"`
			Timeout  int64   `json:"timeout_seconds"`
			Expires  string  `json:"expires_at"`
			State    string  `json:"state"`
			Charged  *string `json:"charged_amount"`
			Terminal *string `json:"terminal_at"`
			Retain   *string `json:"retain_until"`
		} `json:"pending_reservations"`
	}
	if err != nil || json.Unmarshal(raw, &snapshot) != nil || len(snapshot.Accounts) != 1 || snapshot.Accounts[0].Account != account || len(snapshot.Pending) != 1 {
		return &siteProbeFailure{"IMAGE_WEBSITE_BALANCE_API_UNCONFIRMED"}
	}
	a := snapshot.Accounts[0]
	p := snapshot.Pending[0]
	expiry, parseErr := time.Parse("2006-01-02T15:04:05.000Z", p.Expires)
	expires := expiry.UnixMilli()
	if parseErr != nil || p.Account != account || p.State != "active" || p.Charged != nil || p.Terminal != nil || p.Retain != nil || p.Timeout != 600 || expires <= time.Now().UnixMilli() || p.ID == "" || p.Key == "" {
		return &siteProbeFailure{"IMAGE_WEBSITE_PENDING_UNCONFIRMED"}
	}
	want := fmt.Sprintf("Account: %s\nBalance (microunits): %s\nAvailable (microunits): %s\nEnabled: %t\nPending holds (amounts in microunits; timestamps in Unix milliseconds):\n- reservation_id=%s idempotency_key=%s account_id=%s maximum=%s timeout_seconds=%d expires_at=%d state=%s charged_amount=null terminal_at=null retain_until=null\n", strconv.QuoteToASCII(account), a.Balance, snapshot.Available, a.Enabled, strconv.QuoteToASCII(p.ID), strconv.QuoteToASCII(p.Key), strconv.QuoteToASCII(p.Account), p.Maximum, p.Timeout, expires, strconv.QuoteToASCII(p.State))
	// Account-selector query strings are refused, never used as authority.
	if _, err := c.do("GET", "/home?account_id="+url.QueryEscape(other), nil, 400, ""); err != nil {
		return &siteProbeFailure{"IMAGE_WEBSITE_ISOLATION_UNCONFIRMED"}
	}
	page, err := c.do("GET", "/home", nil, 200, "")
	// Strip only host markup, then unescape: this also verifies the complete
	// contiguous text representation rather than merely matching field names.
	text := html.UnescapeString(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(string(page), ""))
	if err != nil {
		return &siteProbeFailure{"IMAGE_WEBSITE_ACCOUNT_PAGE_UNCONFIRMED"}
	}
	if !strings.Contains(text, want) {
		return &siteProbeFailure{"IMAGE_WEBSITE_COMPLETE_TEXT_UNCONFIRMED"}
	}
	if strings.Count(text, "reservation_id=") != 1 || strings.Contains(text, other) || bytes.Contains(page, []byte(os.Getenv("LEDGER_ADMIN_KEY"))) {
		return &siteProbeFailure{"IMAGE_WEBSITE_ISOLATION_UNCONFIRMED"}
	}
	return nil
}

const sitePublishedLabel = "Synthetic image publication selected"

func siteAssets(c *siteClient, revision *website.Revision) error {
	assets := map[string]string{
		"/_site/fonts/iosevka-term-regular.woff2": "2921da08b32396422927bb76e3618938e13ede0b3dfb1785d5ca9d88df863752",
		"/_site/fonts/iosevka-term-bold.woff2":    "3f9db216e17c47e03fd0c392b17e3a2f7cbd568c1bc4a8dfb165a106df273753",
		"/_site/fonts/OFL.txt":                    "4ba53c7c1cb39279aae5f8d7d22054c485c71169920e5a36ed098b115e2e3c5d",
	}
	// Compile expected CSS from the probe's frozen public source, never from a
	// digest claimed by the running service or mutable publication record.
	assets[revision.CSSPath()] = fmt.Sprintf("%x", sha256.Sum256([]byte(revision.CSS())))
	for path, digest := range assets {
		raw, err := c.do("GET", path, nil, 200, "")
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != digest {
			return fmt.Errorf("website frozen asset binding mismatch")
		}
	}
	return nil
}

type siteProbeFailure struct{ code string }

func (e *siteProbeFailure) Error() string { return "website qualification unconfirmed" }

func websiteSmoke(restarted bool) (result error) {
	stage := "IMAGE_WEBSITE_NATIVE_AUTH_UNCONFIRMED"
	defer func() {
		if _, classified := result.(*siteProbeFailure); result != nil && !classified {
			result = &siteProbeFailure{stage}
		}
	}()
	key := os.Getenv("LEDGER_ADMIN_KEY")
	login, _ := json.Marshal(map[string]string{"identity": os.Getenv("LEDGER_OPERATOR_EMAIL"), "password": os.Getenv("LEDGER_OPERATOR_PASSWORD")})
	raw, err := request("/api/collections/_superusers/auth-with-password", string(login), 200, "")
	var auth struct {
		Token string `json:"token"`
	}
	if err != nil || json.Unmarshal(raw, &auth) != nil || auth.Token == "" || auth.Token == key {
		return fmt.Errorf("website native authentication unconfirmed")
	}
	token := auth.Token
	stage = "IMAGE_WEBSITE_PROVISION_UNCONFIRMED"
	accounts := []string{"website-image-one", "website-image-two"}
	if !restarted {
		for index, account := range accounts {
			body, _ := json.Marshal(map[string]any{"account_id": account, "balance": strconv.Itoa(101 + index), "enabled": true})
			if _, err := request("/api/collections/ledger_accounts/records", string(body), 200, token); err != nil {
				return err
			}
			body, _ = json.Marshal(map[string]string{"account_id": account, "password": websiteKey(index), "passwordConfirm": websiteKey(index)})
			if _, err := request("/api/collections/customer_keys/records", string(body), 200, token); err != nil {
				return err
			}
			body, _ = json.Marshal(map[string]any{"account_id": account, "maximum": strconv.Itoa(11 + index), "timeout_seconds": 600})
			if _, err := request("/reservations", string(body), 200, key); err != nil {
				return err
			}
		}
	}
	stage = "IMAGE_WEBSITE_SOURCE_UNCONFIRMED"
	files, err := website.Bundled()
	if err != nil {
		return fmt.Errorf("website frozen source unavailable")
	}
	if restarted {
		files["pages.el"] = strings.ReplaceAll(files["pages.el"], "Private inference, with clear boundaries.", sitePublishedLabel)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	revision, err := website.Prepare(ctx, files)
	if err != nil {
		return fmt.Errorf("website frozen compilation unconfirmed")
	}
	stage = "IMAGE_WEBSITE_PUBLIC_UNCONFIRMED"
	guest := &siteClient{}
	page, err := guest.do("GET", "/", nil, 200, "")
	label := "Private inference, with clear boundaries."
	if restarted {
		label = sitePublishedLabel
	}
	if err != nil || !bytes.Contains(page, []byte(label)) || !bytes.Contains(page, []byte(revision.CSSPath())) {
		return fmt.Errorf("website homepage unconfirmed")
	}
	stage = "IMAGE_WEBSITE_ASSETS_UNCONFIRMED"
	if err := siteAssets(guest, revision); err != nil {
		return err
	}
	if _, err := guest.do("GET", "/home", nil, 303, "/login"); err != nil {
		return err
	}
	stage = "IMAGE_WEBSITE_CUSTOMER_UNCONFIRMED"
	// A well-formed but unprovisioned key must not grant a session. Native and
	// accounting bearer authority must not act as website session authority.
	page, err = guest.do("GET", "/login", nil, 200, "")
	if err != nil {
		return err
	}
	refusedForm, err := siteForm(page)
	if err != nil {
		return err
	}
	refusedForm.Set("key", websiteKey(2))
	if _, err := guest.do("POST", "/login", refusedForm, 401, ""); err != nil {
		return err
	}
	if guest.cookies["__Host-possums"] != nil {
		return fmt.Errorf("website refused login session mismatch")
	}
	for _, credential := range []string{key, token} {
		if _, err := exchange("GET", "/home", "", 303, credential, 8192); err != nil {
			return err
		}
	}
	clients := []*siteClient{{}, {}}
	for index, client := range clients {
		stage = "IMAGE_WEBSITE_CUSTOMER_UNCONFIRMED"
		if err := siteLogin(client, websiteKey(index)); err != nil {
			return err
		}
		stage = "IMAGE_WEBSITE_SNAPSHOT_UNCONFIRMED"
		if err := siteSnapshot(client, accounts[index], accounts[1-index]); err != nil {
			return err
		}
		for _, credential := range []string{"", client.cookies["__Host-possums"].Value} {
			if _, err := request("/balance", `{"account_id":"website-image-one"}`, 401, credential); err != nil {
				return err
			}
		}
	}
	if clients[0].cookies["__Host-possums"].Value == clients[1].cookies["__Host-possums"].Value {
		return fmt.Errorf("website session isolation unconfirmed")
	}
	if !restarted {
		stage = "IMAGE_WEBSITE_PUBLICATION_UNCONFIRMED"
		const path = "/api/collections/website_bundles/records/websitebundle01"
		raw, err = request(path, "", 200, token)
		var record struct {
			Draft struct {
				Host  int               `json:"host"`
				Files map[string]string `json:"files"`
			} `json:"draft"`
		}
		if err != nil || json.Unmarshal(raw, &record) != nil || record.Draft.Host != 1 || !reflect.DeepEqual(record.Draft.Files, map[string]string(files)) {
			return fmt.Errorf("website native draft source unconfirmed")
		}
		record.Draft.Files["pages.el"] = strings.ReplaceAll(record.Draft.Files["pages.el"], label, sitePublishedLabel)
		body, _ := json.Marshal(map[string]any{"draft": record.Draft})
		if _, err := exchange("PATCH", path, string(body), 200, token, 65536); err != nil {
			return err
		}
		page, err = guest.do("GET", "/", nil, 200, "")
		if err != nil || !bytes.Contains(page, []byte(label)) || bytes.Contains(page, []byte(sitePublishedLabel)) {
			return fmt.Errorf("website draft isolation unconfirmed")
		}
		if _, err := exchange("PATCH", path, `{"publish":true}`, 200, token, 65536); err != nil {
			return err
		}
		page, err = guest.do("GET", "/", nil, 200, "")
		if err != nil || !bytes.Contains(page, []byte(sitePublishedLabel)) || bytes.Contains(page, []byte(label)) {
			return fmt.Errorf("website activation unconfirmed")
		}
		for index, client := range clients {
			if err := siteSnapshot(client, accounts[index], accounts[1-index]); err != nil {
				return err
			}
		}
	}
	stage = "IMAGE_WEBSITE_LOGOUT_UNCONFIRMED"
	for _, client := range clients {
		page, err = client.do("GET", "/home", nil, 200, "")
		if err != nil {
			return err
		}
		form, err := siteForm(page)
		if err != nil {
			return err
		}
		stale := &siteClient{cookies: map[string]*http.Cookie{"__Host-possums": client.cookies["__Host-possums"]}}
		if _, err := client.do("POST", "/logout", form, 303, "/"); err != nil {
			return err
		}
		if client.cookies["__Host-possums"] != nil {
			return fmt.Errorf("website logout cookie unconfirmed")
		}
		if _, err := client.do("GET", "/home", nil, 303, "/login"); err != nil {
			return err
		}
		if _, err := stale.do("GET", "/home", nil, 303, "/login"); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	var err error
	if len(os.Args) == 2 {
		err = run(os.Args[1])
	}
	if len(os.Args) != 2 || err != nil {
		code := "IMAGE_PROBE_UNCONFIRMED"
		if failure, ok := err.(*siteProbeFailure); ok {
			code = failure.code
		}
		diagnostic := map[string]string{"code": code, "stage": "synthetic_probe", "constraint": "expected_local_fixture_evidence", "outcome": "unknown", "next_step": "Cause unknown. Inspect the isolated image fixture privately; never initialize production state."}
		_ = json.NewEncoder(os.Stderr).Encode(diagnostic)
		os.Exit(1)
	}
	_, _ = io.WriteString(os.Stdout, "{\"passed\":true,\"stage\":\"synthetic_probe\"}\n")
}
