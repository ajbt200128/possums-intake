package main

import (
	"bytes"
	"context"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"possums-intake/internal/httpapi"
	"possums-intake/internal/ledger"
)

func operatorEnv(email, password string) []string {
	var result []string
	for _, entry := range helperEnv(syntheticAdminKey) {
		if !strings.HasPrefix(entry, "LEDGER_OPERATOR_") {
			result = append(result, entry)
		}
	}
	return append(result, "LEDGER_OPERATOR_EMAIL="+email, "LEDGER_OPERATOR_PASSWORD="+password)
}
func runOperatorCLI(t *testing.T, email, password string, args ...string) (string, string, bool) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal("test binary unavailable")
	}
	command := exec.Command(binary, append([]string{"-test.run=^TestCLIProcess$", "--"}, args...)...)
	command.Env = operatorEnv(email, password)
	var out, stderr bytes.Buffer
	command.Stdout, command.Stderr = &out, &stderr
	err = command.Run()
	return out.String(), stderr.String(), err == nil
}

func TestOperatorInitCredentialValidationBeforeSideEffects(t *testing.T) {
	for _, credentials := range [][2]string{{"", ""}, {"synthetic@example.invalid", ""}, {"", "SYNTHETIC_PRIVATE_SENTINEL"}, {"SYNTHETIC_PRIVATE_SENTINEL", "valid-password"}, {"synthetic@example.invalid", "short"}, {"synthetic@example.invalid", strings.Repeat("x", 72)}, {"synthetic@example.invalid", strings.Repeat("猫", 30)}} {
		dir := filepath.Join(t.TempDir(), "SYNTHETIC_PRIVATE_SENTINEL")
		out, stderr, success := runOperatorCLI(t, credentials[0], credentials[1], "init", "--operator", "--state", dir)
		checkFailure(t, out, stderr, success, "LEDGER_OPERATOR_SETUP_INVALID")
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("invalid credentials caused init filesystem side effects")
		}
	}
	// Credentials in argv are always rejected by the ordinary closed flag parser.
	dir := filepath.Join(t.TempDir(), "state")
	out, stderr, success := runOperatorCLI(t, "synthetic@example.invalid", "valid-password", "init", "--operator", "--state", dir, "--password", "SYNTHETIC_PRIVATE_SENTINEL")
	checkFailure(t, out, stderr, success, ledger.CodePath)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("credential argv caused initialization")
	}
}

func TestOperatorDefaultExclusionExplicitInitAndNoServeReset(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	out, stderr, success := runOperatorCLI(t, "synthetic@example.invalid", "synthetic-password-original", "init", "--operator", "--state", dir)
	if !success || stderr != "" || out != "{\"status\":\"initialized_empty\"}\n" {
		t.Fatal("explicit operator init failed; output withheld")
	}
	s, err := ledger.Open(context.Background(), dir)
	if err != nil {
		t.Fatal("open failed")
	}
	app, err := s.OperatorApp()
	if err != nil {
		t.Fatal("operator app failed")
	}
	records, err := app.FindAllRecords("_superusers")
	if err != nil || len(records) != 1 || !records[0].ValidatePassword("synthetic-password-original") {
		t.Fatal("explicit native password bootstrap failed")
	}
	collection := records[0].Collection()
	if collection.AuthAlert.Enabled || collection.OTP.Enabled || collection.MFA.Enabled || collection.OAuth2.Enabled {
		t.Fatal("native fingerprint/extra auth features enabled")
	}
	custom, err := httpapi.NewHandler(s, s.Ready, syntheticAdminKey, nil)
	if err != nil {
		t.Fatal("handler unavailable")
	}
	for _, path := range []string{"/_/", "/operator/", "/api/settings", "/api/collections/_superusers/auth-methods"} {
		rec := httptest.NewRecorder()
		custom.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 404 {
			t.Fatal("default handler included operator surface")
		}
	}
	if s.Close() != nil {
		t.Fatal("close failed")
	}
	// A held loopback port reaches actual operator handler construction, then
	// fails without serving. Neither present nor absent init credentials reset it.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("loopback fixture unavailable")
	}
	defer listener.Close()
	for _, credentials := range [][2]string{{"other@example.invalid", "replacement-password-never-used"}, {"", ""}} {
		out, stderr, success = runOperatorCLI(t, credentials[0], credentials[1], "serve", "--operator", "--state", dir, "--listen", listener.Addr().String())
		checkFailure(t, out, stderr, success, codeBind)
	}
	out, stderr, success = runOperatorCLI(t, "other@example.invalid", "replacement-password-never-used", "init", "--operator", "--state", dir)
	checkFailure(t, out, stderr, success, ledger.CodeExisting)
	s, err = ledger.Open(context.Background(), dir)
	if err != nil {
		t.Fatal("reopen failed")
	}
	defer s.Close()
	app, err = s.OperatorApp()
	if err != nil {
		t.Fatal("serve/reset qualification changed")
	}
	records, err = app.FindAllRecords("_superusers")
	if err != nil || len(records) != 1 || records[0].Email() != "synthetic@example.invalid" || !records[0].ValidatePassword("synthetic-password-original") || records[0].ValidatePassword("replacement-password-never-used") {
		t.Fatal("existing native identity reset")
	}
	empty := initProcessState(t)
	// No obsolete one-superuser startup gate: the held port bounds this serve
	// attempt without waiting on a listener that is now correctly allowed.
	out, stderr, success = runOperatorCLI(t, "synthetic@example.invalid", "some-password", "serve", "--operator", "--state", empty, "--listen", listener.Addr().String())
	checkFailure(t, out, stderr, success, codeBind)
	emptyService, err := ledger.Open(context.Background(), empty)
	if err != nil {
		t.Fatal("empty synthetic state reopen unavailable")
	}
	defer emptyService.Close()
	emptyApp, err := emptyService.OperatorApp()
	if err != nil {
		t.Fatal("zero-admin stock app access refused")
	}
	count, err := emptyApp.CountRecords("_superusers")
	if err != nil || count != 0 {
		t.Fatal("serve created native credentials")
	}
}
