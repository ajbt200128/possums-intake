package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/httpapi"
	"possums-intake/internal/ledger"
	"possums-intake/internal/operator"
	"possums-intake/internal/siteauth"
)

const customerSiteEmail = "customer-site@example.invalid"
const customerSitePassword = "synthetic-customer-site-admin-password"

func customerSiteStage(output []byte) string {
	value := strings.TrimSpace(string(output))
	for _, stage := range []string{"tooling", "launch", "admin_login", "provision", "customer_login", "snapshot", "draft", "publish", "stale", "invalid", "invalid_code", "restore", "privacy", "privacy_storage", "privacy_cookies", "privacy_logging", "privacy_network", "privacy_console", "privacy_page_error", "privacy_request_failure", "privacy_schema_read_failure", "privacy_native_list_failure", "privacy_native_record_failure", "privacy_settings_read_failure", "privacy_realtime_failure", "privacy_navigation_failure", "privacy_font_failure", "privacy_stylesheet_failure", "privacy_console_warning", "privacy_console_source", "privacy_favicon_404", "privacy_http_response", "privacy_external", "cleanup"} {
		if value == "CUSTOMER_SITE_FAIL "+stage {
			return stage
		}
	}
	return "completion_unknown"
}

// This harness accepts no existing state path. Every record/key is synthetic.
// It uses default runtime setup/hosting through a synthetic HTTPS frontend and
// HTTP backend; this tests offload policy, not Tinfoil's deployed boundary.
func TestCustomerSiteCombinedBrowser(t *testing.T) {
	if os.Getenv("INTAKE_CUSTOMER_SITE_BROWSER") != "synthetic-only" {
		t.Skip("explicit synthetic combined browser qualification not requested")
	}
	ctx, dir := context.Background(), t.TempDir()
	if ledger.InitOperator(ctx, dir, ledger.OperatorCredentials{Email: customerSiteEmail, Password: customerSitePassword}) != nil {
		t.Fatal("CUSTOMER_SITE_INIT_UNAVAILABLE stage=fixture_init constraint=fresh_synthetic_state; inspect fixture setup privately")
	}
	service, err := ledger.Open(ctx, dir)
	if err != nil {
		t.Fatal("CUSTOMER_SITE_OPEN_UNAVAILABLE stage=fixture_open constraint=initialized_synthetic_state; inspect fixture setup privately")
	}
	t.Cleanup(func() {
		if service.Close() != nil {
			t.Error("CUSTOMER_SITE_CLOSE_UNCONFIRMED stage=fixture_close constraint=sdk_completion_cause_unknown; preserve synthetic partial state privately")
		}
	})
	app, err := service.OperatorApp()
	if err != nil || initializeWebsite(ctx, app) != nil {
		t.Fatal("CUSTOMER_SITE_SCHEMA_UNAVAILABLE stage=fixture_schema constraint=explicit_supported_initialization; inspect fixture setup privately")
	}
	key := func(n int) string { return fmt.Sprintf("v1.%013d.%032x", time.Now().UnixMilli(), n) }
	lookupA := sha256.Sum256([]byte("possums-intake/customer-key/lookup/v1\x00" + strings.Repeat("A", 128)))
	// Fund only the actual record created by the browser's native SDK form.
	// This explicit test hook is not runtime credential creation or aliasing.
	app.OnRecordAfterCreateSuccess(ledger.AccountCollectionName).BindFunc(func(e *core.RecordEvent) error {
		if e.Record.GetString("key_lookup") == "v1_"+hex.EncodeToString(lookupA[:]) {
			if _, err := service.Credit(ctx, ledger.CreditInput{AccountID: e.Record.Id, Amount: "100", IdempotencyKey: key(1)}); err != nil {
				return err
			}
			for i, maximum := range []ledger.Money{"7", "3"} {
				if _, err := service.Reserve(ctx, ledger.ReserveInput{AccountID: e.Record.Id, Maximum: maximum, TimeoutSeconds: 600, IdempotencyKey: key(i + 2)}); err != nil {
					return err
				}
			}
		}
		return e.Next()
	})
	shared := make([]byte, 32)
	if _, err := rand.Read(shared); err != nil {
		t.Fatal("CUSTOMER_SITE_KEY_UNAVAILABLE stage=fixture_key constraint=local_random_generation; inspect local runtime privately")
	}
	accounting, err := httpapi.NewHandler(service, service.Ready, hex.EncodeToString(shared), nil)
	if err != nil {
		t.Fatal("CUSTOMER_SITE_ACCOUNTING_UNAVAILABLE stage=fixture_accounting constraint=shared_key_handler; inspect fixture privately")
	}
	native, err := operator.NewHandler(accounting, app, service)
	if err != nil {
		t.Fatal("CUSTOMER_SITE_NATIVE_UNAVAILABLE stage=fixture_native constraint=supported_sdk_handler; inspect fixture privately")
	}
	host, err := newWebsiteHandler(service, native, nil)
	if err != nil {
		t.Fatal("CUSTOMER_SITE_BIND_UNAVAILABLE stage=fixture_host constraint=explicit_publication_and_snapshot; inspect fixture privately")
	}
	backend := httptest.NewServer(host)
	t.Cleanup(backend.Close)
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal("CUSTOMER_SITE_PROXY_UNAVAILABLE stage=fixture_proxy constraint=synthetic_backend; inspect fixture privately")
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorLog = log.New(io.Discard, "", 0)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "CUSTOMER_SITE_PROXY_UNAVAILABLE stage=fixture_proxy constraint=upstream_completion_cause_unknown; inspect synthetic fixture privately", http.StatusBadGateway)
	}
	server := httptest.NewUnstartedServer(proxy)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(server.Close)
	spki := sha256.Sum256(server.Certificate().RawSubjectPublicKeyInfo)
	childCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(childCtx, "node", "../../internal/website/customer-site-browser-test.mjs")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "OTEL_SDK_DISABLED=true", "CUSTOMER_SITE_FIXTURE_URL=" + server.URL, "CUSTOMER_SITE_FIXTURE_SPKI=" + base64.StdEncoding.EncodeToString(spki[:])}
	output, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "CUSTOMER_SITE_OK" {
		t.Fatalf("CUSTOMER_SITE_BROWSER_UNAVAILABLE stage=%s constraint=synthetic_script_completion_cause_unknown outcome=unconfirmed; inspect the isolated fixture privately; no operation was replayed", customerSiteStage(output))
	}
	var accountA string
	for index, fixture := range []struct{ key string }{{strings.Repeat("A", 128)}, {strings.Repeat("C", 64)}} {
		lookup := sha256.Sum256([]byte("possums-intake/customer-key/lookup/v1\x00" + fixture.key))
		password := sha256.Sum256([]byte("possums-intake/customer-key/password/v1\x00" + fixture.key))
		secret := base64.RawURLEncoding.EncodeToString(password[:])
		record, err := app.FindFirstRecordByData(siteauth.CustomerCollection, "key_lookup", "v1_"+hex.EncodeToString(lookup[:]))
		if err != nil || len(record.Id) != 15 || record.Collection().Fields.GetByName("account_id") != nil || !record.ValidatePassword(secret) {
			t.Fatal("CUSTOMER_SITE_VERIFIER_UNAVAILABLE stage=fixture_receipt constraint=exact_binding_and_sdk_verifier; inspect synthetic state privately")
		}
		if index == 0 {
			accountA = record.Id
		}
		if filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			value, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(value), fixture.key) || strings.Contains(string(value), secret) {
				return fmt.Errorf("closed synthetic persistence refusal")
			}
			return nil
		}) != nil {
			t.Fatal("CUSTOMER_SITE_PERSISTENCE_REFUSED stage=fixture_files constraint=no_raw_or_derived_key_bytes; inspect fixture privately")
		}
	}
	snapshot, err := service.Balance(ctx, accountA)
	if err != nil || snapshot.AccountBalance.Balance != "100" || snapshot.Available != "90" || len(snapshot.PendingReservations) != 2 {
		t.Fatal("CUSTOMER_SITE_FINANCIAL_RECEIPT_UNAVAILABLE stage=fixture_receipt constraint=unchanged_authoritative_snapshot; do not infer rollback or replay; inspect synthetic state privately")
	}
	var logs, origins, customers int
	if app.Settings().Logs.MaxDays != 0 || app.AuxDB().NewQuery("SELECT count(*) FROM _logs").Row(&logs) != nil || logs != 0 || app.DB().NewQuery("SELECT count(*) FROM _authOrigins").Row(&origins) != nil || origins != 0 || app.DB().NewQuery("SELECT count(*) FROM accounts").Row(&customers) != nil || customers != 2 {
		t.Fatal("CUSTOMER_SITE_ACTIVITY_REFUSED stage=fixture_receipt constraint=logging_off_and_only_explicit_customers; inspect synthetic state privately")
	}
}

func TestCustomerSiteBrowserProtocolIsClosed(t *testing.T) {
	if customerSiteStage([]byte("CUSTOMER_SITE_FAIL snapshot\n")) != "snapshot" {
		t.Fatal("CUSTOMER_SITE_PROTOCOL_KNOWN_STAGE")
	}
	for _, value := range []string{"RAW_PRIVATE_EXCEPTION", "CUSTOMER_SITE_FAIL snapshot RAW_PRIVATE_EXCEPTION", "CUSTOMER_SITE_FAIL snapshot\nRAW_PRIVATE_EXCEPTION"} {
		if customerSiteStage([]byte(value)) != "completion_unknown" {
			t.Fatal("CUSTOMER_SITE_PROTOCOL_PRIVATE_OUTPUT")
		}
	}
}
