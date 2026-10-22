package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/httpapi"
	"possums-intake/internal/ledger"
	"possums-intake/internal/operator"
	"possums-intake/internal/siteauth"
	"possums-intake/internal/testfixture"
)

func accountingRuntimeKey(n int) string {
	return fmt.Sprintf("v1.%013d.%032x", time.Now().UnixMilli(), n)
}

func TestWebsiteDefaultExplicitSetupPreservesFinancialStateAndServeNeverSeeds(t *testing.T) {
	ctx, dir := context.Background(), filepath.Join(t.TempDir(), "state")
	if ledger.Init(ctx, dir) != nil {
		t.Fatal("SITE_RUNTIME_TEST_INIT")
	}
	s, err := ledger.Open(ctx, dir)
	if err != nil {
		t.Fatal("SITE_RUNTIME_TEST_OPEN")
	}
	account := "websitesetup000"
	if _, err := testfixture.Create(ctx, s, account); err != nil {
		t.Fatal("SITE_RUNTIME_TEST_ACCOUNT")
	}
	if _, err := s.Credit(ctx, ledger.CreditInput{AccountID: account, Amount: "100", IdempotencyKey: accountingRuntimeKey(1)}); err != nil {
		t.Fatal("SITE_RUNTIME_TEST_CREDIT")
	}
	if _, err := s.Reserve(ctx, ledger.ReserveInput{AccountID: account, Maximum: "7", TimeoutSeconds: 600, IdempotencyKey: accountingRuntimeKey(2)}); err != nil {
		t.Fatal("SITE_RUNTIME_TEST_HOLD")
	}
	before, err := s.Balance(ctx, account)
	if err != nil || s.Close() != nil {
		t.Fatal("SITE_RUNTIME_TEST_INITIAL_RECEIPT")
	}
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("SITE_RUNTIME_TEST_HELD_LISTENER")
	}
	defer held.Close()
	out, stderr, success := runCLI(t, "serve", "--state", dir, "--listen", held.Addr().String())
	checkFailure(t, out, stderr, success, codeBind)
	for i := 0; i < 2; i++ {
		out, stderr, success = runCLI(t, "website-init", "--state", dir)
		if !success || stderr != "" || out != "{\"status\":\"website_initialized\"}\n" {
			t.Fatal("SITE_RUNTIME_EXPLICIT_SETUP")
		}
	}
	out, stderr, success = runCLI(t, "serve", "--state", dir, "--listen", held.Addr().String())
	checkFailure(t, out, stderr, success, codeBind)
	s, err = ledger.Open(ctx, dir)
	if err != nil {
		t.Fatal("SITE_RUNTIME_TEST_REOPEN")
	}
	defer s.Close()
	after, err := s.Balance(ctx, account)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("SITE_RUNTIME_SETUP_CHANGED_FINANCIAL_STATE")
	}
	app, err := s.CustomerAuthApp()
	if err != nil {
		t.Fatal("SITE_RUNTIME_TEST_APP")
	}
	for _, name := range []string{siteauth.CustomerCollection, "_superusers"} {
		n, err := app.CountRecords(name)
		want := int64(0)
		if name == siteauth.CustomerCollection {
			want = 1
		}
		if err != nil || n != want {
			t.Fatal("SITE_RUNTIME_SEEDED_CREDENTIALS")
		}
	}
	for _, args := range [][]string{{"website-init", "--state", dir, "--listen", "127.0.0.1:0"}, {"website-init", "--state", dir, "--operator"}, {"website-init", "--state", "relative"}, {"website-init", "--state", dir, "--SYNTHETIC_PRIVATE_SENTINEL"}} {
		out, stderr, success := runCLI(t, args...)
		checkFailure(t, out, stderr, success, "SITE_INITIALIZATION_OPTIONS_INVALID")
	}
}

func TestWebsiteDefaultColdFailureKeepsNativeAndAccountingIndependent(t *testing.T) {
	ctx, dir := context.Background(), initProcessState(t)
	s, err := ledger.Open(ctx, dir)
	if err != nil {
		t.Fatal("SITE_RUNTIME_TEST_OPEN")
	}
	defer s.Close()
	app, err := s.OperatorApp()
	if err != nil {
		t.Fatal("SITE_RUNTIME_TEST_NATIVE_APP")
	}
	selection, err := app.FindRecordById("website_selection", "websitebundle01")
	if err != nil {
		t.Fatal("SITE_RUNTIME_TEST_SELECTION")
	}
	selection.Set("active", json.RawMessage(`{"host":999}`))
	if app.Save(selection) != nil {
		t.Fatal("SITE_RUNTIME_TEST_CORRUPT_CODE")
	}
	accounting, err := httpapi.NewHandler(s, s.Ready, syntheticAdminKey, nil)
	if err != nil {
		t.Fatal("SITE_RUNTIME_TEST_ACCOUNTING")
	}
	native, err := operator.NewHandler(accounting, app, s)
	if err != nil {
		t.Fatal("SITE_RUNTIME_TEST_NATIVE")
	}
	o := &runtimeObserver{}
	host, err := newWebsiteHandler(s, native, o)
	if err != nil {
		t.Fatal("SITE_RUNTIME_NATIVE_UNAVAILABLE_AFTER_CODE_FAILURE")
	}
	o.want(t, failuretype.NativeReadUnknown)
	for _, tc := range []struct {
		path   string
		status int
	}{{"/", 503}, {"/healthz", 200}, {"/_/", 200}} {
		w := httptest.NewRecorder()
		host.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://synthetic.invalid"+tc.path, nil))
		if w.Code != tc.status {
			t.Fatal("SITE_RUNTIME_RESERVED_AVAILABILITY")
		}
		if tc.path == "/" && !strings.Contains(w.Body.String(), "SITE_PUBLICATION_UNAVAILABLE") {
			t.Fatal("SITE_RUNTIME_NO_DEMO_FALLBACK")
		}
	}
	preserved, err := app.FindRecordById("website_selection", "websitebundle01")
	if err != nil || preserved.GetString("active") != selection.GetString("active") {
		t.Fatal("SITE_RUNTIME_RESET_SELECTED_CODE")
	}
}
