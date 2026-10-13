package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"possums-intake/internal/ledger"
)

func TestCLIProcess(t *testing.T) {
	if os.Getenv("INTAKE_SYNTHETIC_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{os.Args[0]}, os.Args[i+1:]...)
			break
		}
	}
	main()
	os.Exit(0)
}
func runCLI(t *testing.T, args ...string) (string, string, bool) {
	t.Helper()
	binary, e := os.Executable()
	if e != nil {
		t.Fatal("test binary unavailable")
	}
	argv := append([]string{"-test.run=^TestCLIProcess$", "--"}, args...)
	command := exec.Command(binary, argv...)
	command.Env = helperEnv(syntheticAdminKey)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	e = command.Run()
	return stdout.String(), stderr.String(), e == nil
}
func checkFailure(t *testing.T, stdout, stderr string, success bool, code ledger.Code) {
	t.Helper()
	if success || stdout != "" {
		t.Fatal("failure emitted success output")
	}
	var d ledger.Diagnostic
	if e := json.Unmarshal([]byte(stderr), &d); e != nil || d.Code != code || d.Stage == "" || d.Constraint == "" || d.Outcome == "" || d.NextStep == "" {
		t.Fatal("actual startup stderr was not one useful closed diagnostic")
	}
	if strings.Contains(stderr, "SYNTHETIC_PRIVATE_SENTINEL") || strings.Contains(stderr, "panic:") || strings.Contains(stderr, "goroutine ") {
		t.Fatal("startup leaked hostile values or a stack")
	}
}
func TestCLIExplicitInitExistingServeAndPrivateErrors(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "SYNTHETIC_PRIVATE_SENTINEL")
	out, stderr, success := runCLI(t, "serve", "--state", dir)
	checkFailure(t, out, stderr, success, ledger.CodeMissing)
	if _, e := os.Stat(dir); !os.IsNotExist(e) {
		t.Fatal("default startup created missing state")
	}
	out, stderr, success = runCLI(t, "init", "--state", dir)
	if !success || stderr != "" || out != "{\"status\":\"initialized_empty\"}\n" {
		t.Fatal("actual empty init output or logging contract failed")
	}
	out, stderr, success = runCLI(t, "init", "--state", dir)
	checkFailure(t, out, stderr, success, ledger.CodeExisting)
	// A held loopback port exercises the actual listener failure boundary.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("synthetic listener unavailable")
	}
	defer listener.Close()
	out, stderr, success = runCLI(t, "serve", "--state", dir, "--listen", listener.Addr().String())
	checkFailure(t, out, stderr, success, codeBind)
	for _, args := range [][]string{{}, {"init", "--state", "relative"}, {"init", "--SYNTHETIC_PRIVATE_SENTINEL"}, {"superuser", "SYNTHETIC_PRIVATE_SENTINEL"}, {"serve", "--state"}} {
		out, stderr, success = runCLI(t, args...)
		checkFailure(t, out, stderr, success, ledger.CodePath)
	}
	// A regular nonempty but invalid SQLite file fails the validation read
	// before bootstrap; that is not proof of a schema mismatch. Driver text and
	// the path stay private.
	corrupt := filepath.Join(t.TempDir(), "SYNTHETIC_PRIVATE_SENTINEL")
	if e := os.Mkdir(corrupt, 0700); e != nil {
		t.Fatal("fixture directory failed")
	}
	for _, name := range []string{"data.db", "auxiliary.db"} {
		if e := os.WriteFile(filepath.Join(corrupt, name), []byte("SYNTHETIC_PRIVATE_SENTINEL"), 0600); e != nil {
			t.Fatal("fixture database failed")
		}
	}
	out, stderr, success = runCLI(t, "serve", "--state", corrupt)
	checkFailure(t, out, stderr, success, ledger.CodeBoundary)
	var d ledger.Diagnostic
	if err := json.Unmarshal([]byte(stderr), &d); err != nil || d.Stage != "state_validation" || d.Outcome != "unavailable" || d.Constraint != "authoritative_boundary_evidence" || !strings.Contains(d.NextStep, "cause unknown") {
		t.Fatal("unreadable SQLite was mislabeled as a proven schema mismatch")
	}
}
