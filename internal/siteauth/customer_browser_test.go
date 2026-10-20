package siteauth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"possums-intake/internal/siteauth"
)

// Explicit opt-in: installed Chromium is not a dependency of ordinary Go tests.
// The browser only sees a fresh synthetic fixture, never an existing preview.
func TestCustomerNativeEditorBrowser(t *testing.T) {
	if os.Getenv("INTAKE_CUSTOMER_NATIVE_BROWSER") != "synthetic-only" {
		t.Skip("manual synthetic browser qualification not requested")
	}
	f := newKeyFixture(t)
	server := httptest.NewTLSServer(f.handler)
	defer server.Close()
	spki := sha256.Sum256(server.Certificate().RawSubjectPublicKeyInfo)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "customer-native-browser-test.mjs")
	// Do not inherit runtime credentials, browser profiles, proxy or exporter
	// settings. URL/SPKI are public properties of this ephemeral TLS fixture.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "OTEL_SDK_DISABLED=true", "CUSTOMER_KEY_FIXTURE_URL=" + server.URL, "CUSTOMER_KEY_FIXTURE_SPKI=" + base64.StdEncoding.EncodeToString(spki[:])}
	output, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "CUSTOMER_NATIVE_EDITOR_OK" {
		// Child output is not a diagnostic transport, even if a runtime/tool
		// fails before the script can replace raw exceptions with closed codes.
		t.Fatalf("CUSTOMER_NATIVE_BROWSER_UNAVAILABLE stage=%s constraint=synthetic_script_completion_cause_unknown; inspect the isolated script privately; no retry occurred", customerBrowserStage(output))
	}
	lookup, secret := syntheticMaterial(strings.Repeat("A", 128))
	record, err := f.app.FindFirstRecordByData(siteauth.CustomerCollection, "key_lookup", lookup)
	if err != nil || record.GetString("account_id") != "synthetic-browser-account" || !record.ValidatePassword(secret) || record.GetString("password") != "" {
		t.Fatal("CUSTOMER_NATIVE_BROWSER_STORAGE_MISMATCH")
	}
	assertNoKeyBytes(t, f.dir, strings.Repeat("A", 128), secret)
	var count, logs, origins int
	if f.app.DB().NewQuery("SELECT count(*) FROM customer_keys").Row(&count) != nil || count != 1 || f.app.AuxDB().NewQuery("SELECT count(*) FROM _logs").Row(&logs) != nil || logs != 0 || f.app.DB().NewQuery("SELECT count(*) FROM _authOrigins").Row(&origins) != nil || origins != 0 {
		t.Fatal("CUSTOMER_NATIVE_BROWSER_PERSISTENCE_MISMATCH")
	}
}
