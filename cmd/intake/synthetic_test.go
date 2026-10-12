package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"possums-intake/internal/ledger"
)

// Build the real entry point, not the test-helper executable, for this packet.
func TestSyntheticCompiledCLI(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "intake")
	build := exec.Command("go", "build", "-o", binary, ".")
	if build.Run() != nil {
		t.Fatal("SYNTHETIC_BUILD_UNAVAILABLE stage=build")
	}
	env := func(tmp, email, password, key string) []string {
		var result []string
		for _, entry := range os.Environ() {
			if strings.HasPrefix(entry, "TMPDIR=") || strings.HasPrefix(entry, "LEDGER_") || strings.HasPrefix(entry, "OTEL_") || strings.HasPrefix(entry, "INTAKE_METRICS_") {
				continue
			}
			result = append(result, entry)
		}
		return append(result, "TMPDIR="+tmp, "LEDGER_OPERATOR_EMAIL="+email, "LEDGER_OPERATOR_PASSWORD="+password, "LEDGER_ADMIN_KEY="+key, "OTEL_SDK_DISABLED=false", "INTAKE_METRICS_ENDPOINT=https://api.honeycomb.io/v1/metrics", "INTAKE_METRICS_HONEYCOMB_API_KEY=SYNTHETIC_PRIVATE_SENTINEL", "INTAKE_METRICS_ENVIRONMENT=development", "INTAKE_METRICS_SLOT=intake-01")
	}
	t.Run("ValidationBeforeFilesystem", func(t *testing.T) {
		cases := []struct {
			args                 []string
			email, password, key string
			code                 string
		}{
			{[]string{"synthetic-serve", "--state="}, "synthetic@example.invalid", "synthetic-password", syntheticAdminKey, string(codeSyntheticOptions)},
			{[]string{"synthetic-serve", "--state", "/SYNTHETIC_PRIVATE_SENTINEL"}, "synthetic@example.invalid", "synthetic-password", syntheticAdminKey, string(codeSyntheticOptions)},
			{[]string{"synthetic-serve", "--operator=false"}, "synthetic@example.invalid", "synthetic-password", syntheticAdminKey, string(codeSyntheticOptions)},
			{[]string{"synthetic-serve", "--password=SYNTHETIC_PRIVATE_SENTINEL"}, "synthetic@example.invalid", "synthetic-password", syntheticAdminKey, string(codeSyntheticOptions)},
			{[]string{"synthetic-serve"}, "", "", syntheticAdminKey, "LEDGER_OPERATOR_SETUP_INVALID"},
			{[]string{"synthetic-serve"}, "SYNTHETIC_PRIVATE_SENTINEL", "synthetic-password", syntheticAdminKey, "LEDGER_OPERATOR_SETUP_INVALID"},
			{[]string{"synthetic-serve"}, "synthetic@example.invalid", "short", syntheticAdminKey, "LEDGER_OPERATOR_SETUP_INVALID"},
			{[]string{"synthetic-serve"}, "synthetic@example.invalid", "synthetic-password", "short", string(codeAdminKey)},
			{[]string{"synthetic-serve", "--listen=localhost:8090"}, "synthetic@example.invalid", "synthetic-password", syntheticAdminKey, string(codeListen)},
			{[]string{"synthetic-serve", "--listen=192.0.2.1:8090"}, "synthetic@example.invalid", "synthetic-password", syntheticAdminKey, string(codeListen)},
			{[]string{"synthetic-serve", "--listen=0.0.0.0:08090"}, "synthetic@example.invalid", "synthetic-password", syntheticAdminKey, string(codeListen)},
		}
		for _, c := range cases {
			tmp := t.TempDir()
			cmd := exec.Command(binary, c.args...)
			cmd.Env = env(tmp, c.email, c.password, c.key)
			var out, stderr bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &stderr
			err := cmd.Run()
			var d struct{ Code, Outcome string }
			if err == nil || out.Len() != 0 || json.Unmarshal(stderr.Bytes(), &d) != nil || d.Code != c.code || d.Outcome != "no_effect" || bytes.Contains(stderr.Bytes(), []byte("SYNTHETIC_PRIVATE_SENTINEL")) {
				t.Fatal("SYNTHETIC_VALIDATION_FAILED stage=startup")
			}
			entries, err := os.ReadDir(tmp)
			if err != nil || len(entries) != 0 {
				t.Fatal("SYNTHETIC_VALIDATION_EFFECT stage=scratch_create")
			}
		}
	})
	t.Run("OrdinaryServeRefusals", func(t *testing.T) {
		tmp := t.TempDir()
		state := filepath.Join(tmp, "missing")
		for _, address := range []string{"127.0.0.1:8090", "0.0.0.0:8090"} {
			cmd := exec.Command(binary, "serve", "--state", state, "--listen", address)
			cmd.Env = env(tmp, "synthetic@example.invalid", "synthetic-password", syntheticAdminKey)
			var out, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &stderr
			err := cmd.Run()
			code := ledger.CodeMissing
			if address == "0.0.0.0:8090" {
				code = codeListen
			}
			checkFailure(t, out.String(), stderr.String(), err == nil, code)
		}
		entries, err := os.ReadDir(tmp)
		if err != nil || len(entries) != 0 {
			t.Fatal("SYNTHETIC_ORDINARY_EFFECT stage=startup")
		}
	})
	t.Run("DefaultWildcardBindFailureDisposesState", func(t *testing.T) {
		listener, err := net.Listen("tcp", "0.0.0.0:8090")
		if err != nil {
			t.Skip("SYNTHETIC_DEFAULT_PORT_UNAVAILABLE stage=fixture")
		}
		defer listener.Close()
		tmp := t.TempDir()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "synthetic-serve")
		cmd.Env = env(tmp, "synthetic@example.invalid", "synthetic-password", syntheticAdminKey)
		var out, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &stderr
		err = cmd.Run()
		checkFailure(t, out.String(), stderr.String(), err == nil, codeBind)
		entries, err := os.ReadDir(tmp)
		if err != nil || len(entries) != 0 {
			t.Fatal("SYNTHETIC_DISPOSAL_FAILED stage=scratch_cleanup")
		}
	})
	t.Run("TempCreationFailure", func(t *testing.T) {
		cmd := exec.Command(binary, "synthetic-serve")
		cmd.Env = env(filepath.Join(t.TempDir(), "missing"), "synthetic@example.invalid", "synthetic-password", syntheticAdminKey)
		var out, stderr bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &stderr
		err := cmd.Run()
		checkFailure(t, out.String(), stderr.String(), err == nil, codeScratch)
	})
	for _, config := range []string{"valid_inherited", "hostile_inherited"} {
		t.Run("WildcardNativeDashboardAndDisposal/"+config, func(t *testing.T) {
			tmp := t.TempDir()
			address := syntheticAddress(t)
			_, port, _ := net.SplitHostPort(address)
			var exports atomic.Int32
			tripwire := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { exports.Add(1); w.WriteHeader(500) }))
			defer tripwire.Close()
			p := &process{done: make(chan error, 1), base: "http://" + address}
			p.cmd = exec.Command(binary, "synthetic-serve", "--listen", "0.0.0.0:"+port)
			p.cmd.Env = env(tmp, "synthetic@example.invalid", "synthetic-password", syntheticAdminKey)
			if config == "hostile_inherited" {
				p.cmd.Env = append(p.cmd.Env, "OTEL_EXPORTER_OTLP_ENDPOINT="+tripwire.URL, "OTEL_EXPORTER_OTLP_HEADERS=SYNTHETIC_PRIVATE_SENTINEL", "OTEL_RESOURCE_ATTRIBUTES=SYNTHETIC_PRIVATE_SENTINEL")
			}
			p.cmd.Stdout = &p.out
			p.cmd.Stderr = &p.stderr
			if p.cmd.Start() != nil {
				t.Fatal("SYNTHETIC_START_FAILED stage=startup")
			}
			go func() { p.done <- p.cmd.Wait() }()
			t.Cleanup(func() { stopProcess(t, p) })
			client := &http.Client{Timeout: time.Second}
			ready := false
			for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); {
				resp, err := client.Get(p.base + "/healthz")
				if err == nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					if resp.StatusCode == 200 {
						ready = true
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !ready {
				t.Fatal("SYNTHETIC_READINESS_FAILED stage=listener")
			}
			entries, err := os.ReadDir(tmp)
			if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "possums-intake-synthetic-") {
				t.Fatal("SYNTHETIC_SCRATCH_FAILED stage=scratch_create")
			}
			scratch := filepath.Join(tmp, entries[0].Name())
			status, fields := rpc(t, p, "/api/collections/_superusers/auth-with-password", `{"identity":"synthetic@example.invalid","password":"synthetic-password"}`, "", "")
			var token string
			if status != 200 || json.Unmarshal(fields["token"], &token) != nil || token == "" {
				t.Fatal("SYNTHETIC_LOGIN_FAILED stage=native_auth")
			}
			if status, _ = rpc(t, p, "/balance", `{"account_id":"synthetic-native"}`, token, ""); status != 401 {
				t.Fatal("SYNTHETIC_AUTH_SEPARATION_FAILED stage=accounting_auth")
			}
			if status, _ = rpc(t, p, "/api/collections/ledger_accounts/records", `{"account_id":"synthetic-native","balance":"0","enabled":true}`, syntheticAdminKey, ""); status != 401 && status != 403 {
				t.Fatal("SYNTHETIC_AUTH_SEPARATION_FAILED stage=native_auth")
			}
			if status, _ = rpc(t, p, "/api/collections/ledger_accounts/records", `{"account_id":"synthetic-native","balance":"0","enabled":true}`, token, ""); status != 200 {
				t.Fatal("SYNTHETIC_ACCOUNT_CREATE_FAILED stage=native_mutation")
			}
			if accountTotal(t, successRPC(t, p, "/balance", `{"account_id":"synthetic-native"}`, "")) != "0" {
				t.Fatal("SYNTHETIC_SEED_CREDIT_FAILED stage=balance")
			}
			response, err := client.Get(p.base + "/_/")
			if err != nil {
				t.Fatal("SYNTHETIC_DASHBOARD_FAILED stage=native_ui")
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatal("SYNTHETIC_DASHBOARD_FAILED stage=native_ui")
			}
			// Inspect actual auxiliary storage while alive (read only); no request logs.
			db, err := sql.Open("sqlite", filepath.Join(scratch, "auxiliary.db")+"?mode=ro")
			if err != nil {
				t.Fatal("SYNTHETIC_LOG_INSPECTION_FAILED stage=privacy")
			}
			var count int
			err = db.QueryRow("SELECT count(*) FROM _logs").Scan(&count)
			db.Close()
			if err != nil || count != 0 || exports.Load() != 0 {
				t.Fatal("SYNTHETIC_PRIVACY_FAILED stage=privacy")
			}
			stopProcess(t, p)
			entries, err = os.ReadDir(tmp)
			if err != nil || len(entries) != 0 {
				t.Fatal("SYNTHETIC_DISPOSAL_FAILED stage=scratch_cleanup")
			}
		})
	}
}

func TestSyntheticListenerAndConservativeCleanup(t *testing.T) {
	for _, address := range []string{"0.0.0.0:8090", "[::]:8090", "127.0.0.1:0", "[::1]:8090"} {
		if validateModeListen(address, true) != nil {
			t.Fatal("synthetic numeric listener rejected")
		}
	}
	for _, address := range []string{"localhost:8090", ":8090", "[::%lo0]:8090", "192.0.2.1:8090", "0.0.0.0:http"} {
		if validateModeListen(address, true) == nil {
			t.Fatal("invalid synthetic listener accepted")
		}
	}
	dir := filepath.Join(t.TempDir(), "scratch")
	if os.Mkdir(dir, 0700) != nil {
		t.Fatal("cleanup fixture unavailable")
	}
	cleanupErr := cleanupScratch(dir, false)
	_, statErr := os.Stat(dir)
	if cleanupErr == nil || statErr != nil {
		t.Fatal("uncertain teardown state removed")
	}
	if cleanupScratch(dir, true) != nil {
		t.Fatal("confirmed teardown cleanup failed")
	}
	// A regular file cannot have children: deterministic removal failure.
	file := filepath.Join(t.TempDir(), "file")
	if os.WriteFile(file, []byte("synthetic"), 0600) != nil {
		t.Fatal("cleanup fixture unavailable")
	}
	if cleanupScratch(filepath.Join(file, "child"), true) == nil {
		t.Fatal("cleanup failure not classified")
	}
}
