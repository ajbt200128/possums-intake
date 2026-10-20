package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/httpapi"
	"possums-intake/internal/ledger"
	"possums-intake/internal/operator"
	"possums-intake/internal/siteauth"
	"possums-intake/internal/website"
)

// PUBLIC synthetic-only credentials. Never use this harness with existing state.
const activationFixtureEmail = "activation-demo@example.invalid"
const activationFixturePassword = "local-activation-demo-password"
const activationFixtureURL = "https://[::1]:8444"

// Only the caller's freshly created private scratch directory is initialized.
func initializeActivationFixture(t *testing.T, ctx context.Context, dir string) {
	t.Helper()
	if ledger.InitOperator(ctx, dir, ledger.OperatorCredentials{Email: activationFixtureEmail, Password: activationFixturePassword}) != nil {
		t.Fatal("ACTIVATION_FIXTURE_INIT")
	}
	service, err := ledger.Open(ctx, dir)
	if err != nil {
		t.Fatal("ACTIVATION_FIXTURE_OPEN")
	}
	defer func() {
		if service.Close() != nil {
			t.Error("ACTIVATION_FIXTURE_CLOSE")
		}
	}()
	app, err := service.CustomerAuthApp()
	if err != nil || seedWebsiteCustomer(ctx, app) != nil || website.InitializePublication(app) != nil {
		t.Fatal("ACTIVATION_FIXTURE_SCHEMA")
	}
	bundle, err := website.Bundled()
	if err != nil {
		t.Fatal("ACTIVATION_FIXTURE_BUNDLE")
	}
	encoded, err := json.Marshal(struct {
		Host  int            `json:"host"`
		Files website.Bundle `json:"files"`
	}{1, bundle})
	if err != nil {
		t.Fatal("ACTIVATION_FIXTURE_ENCODE")
	}
	// Initial code is explicitly seeded only in this new synthetic fixture. Later
	// edits/publication go through the unchanged stock editor and request hook.
	for _, name := range []string{"website_bundles", "website_selection"} {
		record, err := app.FindRecordById(name, "websitebundle01")
		if err != nil {
			t.Fatal("ACTIVATION_FIXTURE_RECORD")
		}
		field := "draft"
		if name == "website_selection" {
			field = "active"
		}
		record.Set(field, json.RawMessage(encoded))
		if app.SaveWithContext(ctx, record) != nil {
			t.Fatal("ACTIVATION_FIXTURE_SEED")
		}
	}
}

func openActivationFixture(t *testing.T, ctx context.Context, dir string) (*ledger.Service, *website.Host, core.App) {
	t.Helper()
	service, err := ledger.Open(ctx, dir)
	if err != nil {
		t.Fatal("ACTIVATION_FIXTURE_REOPEN")
	}
	succeeded := false
	defer func() {
		if !succeeded {
			if service.Close() != nil {
				t.Error("ACTIVATION_FIXTURE_SETUP_CLOSE")
			}
		}
	}()
	// Native trusted administration is deliberate in this isolated harness only.
	app, err := service.OperatorApp()
	if err != nil {
		t.Fatal("ACTIVATION_FIXTURE_NATIVE_APP")
	}
	adapter, err := siteauth.New(app, localWebsiteCollection)
	if err != nil {
		t.Fatal("ACTIVATION_FIXTURE_AUTH")
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		t.Fatal("ACTIVATION_FIXTURE_RANDOM")
	}
	accounting, err := httpapi.NewHandler(service, func() bool { return ctx.Err() == nil && service.Ready() }, hex.EncodeToString(key), nil)
	if err != nil {
		t.Fatal("ACTIVATION_FIXTURE_ACCOUNTING")
	}
	native, err := operator.NewHandler(accounting, app, service)
	if err != nil {
		t.Fatal("ACTIVATION_FIXTURE_NATIVE")
	}
	bundle, err := website.Bundled()
	if err != nil {
		t.Fatal("ACTIVATION_FIXTURE_SOURCE")
	}
	revision, err := website.Prepare(ctx, bundle)
	if err != nil {
		t.Fatal("ACTIVATION_FIXTURE_PREPARE")
	}
	host, err := website.NewFixture(revision, siteauth.FixtureKeyAdapter{Adapter: adapter, Identity: localWebsiteIdentity}, native, website.Config{Lifetime: time.Hour, IdleTimeout: 10 * time.Minute, ConcurrentRequests: 16})
	if err != nil || host.BindPublication(app) != nil {
		t.Fatal("ACTIVATION_FIXTURE_BIND")
	}
	succeeded = true
	return service, host, app
}

func TestActivationBrowserFixtureNativeAndRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initializeActivationFixture(t, ctx, dir)
	service, host, app := openActivationFixture(t, ctx, dir)
	closed := false
	defer func() {
		if !closed {
			if service.Close() != nil {
				t.Error("ACTIVATION_FIXTURE_TEST_CLOSE")
			}
		}
	}()
	server := httptest.NewUnstartedServer(host)
	config, err := localWebsiteTLS()
	if err != nil {
		t.Fatal("ACTIVATION_FIXTURE_TEST_TLS")
	}
	server.TLS = config
	server.StartTLS()
	defer server.Close()
	// Explicitly pin the public fixture certificate, never skip TLS verification.
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	request := func(method, path, body string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal("ACTIVATION_FIXTURE_TEST_REQUEST")
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal("ACTIVATION_FIXTURE_TEST_HTTP")
		}
		return response
	}
	auth := request("POST", "/api/collections/_superusers/auth-with-password", `{"identity":"`+activationFixtureEmail+`","password":"`+activationFixturePassword+`"}`)
	if auth.StatusCode != 200 {
		auth.Body.Close()
		t.Fatal("ACTIVATION_FIXTURE_NATIVE_LOGIN")
	}
	auth.Body.Close()
	response := request("GET", "/_/", "")
	if response.StatusCode != 200 {
		response.Body.Close()
		t.Fatal("ACTIVATION_FIXTURE_NATIVE_UI")
	}
	response.Body.Close()
	response = request("GET", "/", "")
	if response.StatusCode != 200 {
		response.Body.Close()
		t.Fatal("ACTIVATION_FIXTURE_PUBLIC")
	}
	response.Body.Close()
	if app.Settings().Logs.MaxDays != 0 {
		t.Fatal("ACTIVATION_FIXTURE_LOG_SETTING")
	}
	var logs, origins int
	if app.AuxDB().NewQuery("SELECT count(*) FROM _logs").Row(&logs) != nil || app.DB().NewQuery("SELECT count(*) FROM _authOrigins").Row(&origins) != nil || logs != 0 || origins != 0 {
		t.Fatal("ACTIVATION_FIXTURE_ACTIVITY_RETAINED")
	}
	server.Close()
	if service.Close() != nil {
		t.Fatal("ACTIVATION_FIXTURE_TEST_FIRST_CLOSE")
	}
	closed = true
	service, host, _ = openActivationFixture(t, ctx, dir)
	closed = false
	writer := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.TLS = &tls.ConnectionState{}
	host.ServeHTTP(writer, req)
	if writer.Code != 200 {
		t.Fatal("ACTIVATION_FIXTURE_RESTART_PAGE")
	}
}

// Explicit manual-only entrypoint of the test artifact, not a product command.
// No state argument, real credentials, inherited exporter, installer or CA file.
func TestWebsiteActivationManualPreview(t *testing.T) {
	if os.Getenv("INTAKE_MANUAL_ACTIVATION_PREVIEW") != "synthetic-only" {
		t.Skip("manual synthetic preview not requested")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()
	scratch, err := os.MkdirTemp("", "possums-intake-activation-browser-")
	if err != nil {
		t.Fatal("ACTIVATION_PREVIEW_SCRATCH")
	}
	remove := false
	defer func() {
		if cleanupScratch(scratch, remove) != nil {
			t.Error("ACTIVATION_PREVIEW_SCRATCH_CLOSE")
		}
	}()
	initializeActivationFixture(t, ctx, scratch)
	service, host, _ := openActivationFixture(t, ctx, scratch)
	drained := false
	defer func() {
		if service.Close() == nil && drained {
			remove = true
		} else {
			t.Error("ACTIVATION_PREVIEW_TEARDOWN_UNCONFIRMED")
		}
	}()
	config, err := localWebsiteTLS()
	if err != nil {
		t.Fatal("ACTIVATION_PREVIEW_TLS")
	}
	listener, err := net.Listen("tcp6", "[::1]:8444")
	if err != nil {
		t.Fatal("ACTIVATION_PREVIEW_BIND")
	}
	work, workCancel := context.WithCancel(ctx)
	defer workCancel()
	server := localServer(work, host)
	server.TLSConfig = config
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	result := coordinateServeObserved(ctx, work, workCancel, service, server, tls.NewListener(listener, config), ticker.C, time.Now(), nil)
	if result != nil {
		t.Fatal("ACTIVATION_PREVIEW_SERVE_UNCONFIRMED")
	}
	drained = true
}
