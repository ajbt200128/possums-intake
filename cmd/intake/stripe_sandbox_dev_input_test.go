package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSandboxDevLiteralInput(t *testing.T) {
	for _, body := range []string{
		"STRIPE_API_KEY=" + sandboxLocalTestKey,
		"# comment\nexport STRIPE_API_KEY = '" + sandboxLocalTestKey + "' # comment\n",
		"STRIPE_API_KEY=\"" + sandboxLocalTestKey + "\"\r\n",
		"UNRELATED=$(never-run-this)\nSTRIPE_API_KEY=" + sandboxLocalTestKey + " # comment",
	} {
		key, err := parseSandboxDevKey(body)
		if err != nil || key != sandboxLocalTestKey {
			t.Fatal("DEV_TEST_VALID_LITERAL_REJECTED")
		}
	}
	for _, body := range []string{
		"", "# no assignment", "export STRIPE_API_KEY", "STRIPE_API_KEY", "STRIPE_API_KEY=", "STRIPE_API_KEY_EXTRA=" + sandboxLocalTestKey,
		"STRIPE_API_KEY=sk_live_Synthetic123", "STRIPE_API_KEY=rk_live_Synthetic123", "STRIPE_API_KEY=sk_test_offline_fixture_only",
		"STRIPE_API_KEY=sk_test_possums_offline_fixture", "STRIPE_API_KEY=${PRIVATE}", "STRIPE_API_KEY=$(echo private)",
		"STRIPE_API_KEY=`private`", "STRIPE_API_KEY='" + sandboxLocalTestKey, "STRIPE_API_KEY=\"" + sandboxLocalTestKey + "\\n\"",
		"STRIPE_API_KEY=" + sandboxLocalTestKey + ";echo bad", "STRIPE_API_KEY=" + sandboxLocalTestKey + " extra",
		"STRIPE_API_KEY=" + sandboxLocalTestKey + "#no-space", "STRIPE_API_KEY='" + sandboxLocalTestKey + "' extra",
		"STRIPE_API_KEY=" + sandboxLocalTestKey + "\nexport STRIPE_API_KEY=" + sandboxLocalTestKey,
		"STRIPE_API_KEY=" + sandboxLocalTestKey + "\nSTRIPE_API_KEY=bad",
		"STRIPE_API_KEY=" + sandboxLocalTestKey + "\x00", "STRIPE_API_KEY=sk_test_é", "echo STRIPE_API_KEY=" + sandboxLocalTestKey,
		strings.Repeat("#", sandboxDevInputLimit+1),
	} {
		key, err := parseSandboxDevKey(body)
		if err == nil || key != "" {
			t.Fatal("DEV_TEST_HOSTILE_INPUT_ACCEPTED")
		}
		assertClosedRuntime(t, err)
	}
}

func TestSandboxDevBoundedRegularInputOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "authored.env")
	if os.WriteFile(path, []byte("STRIPE_API_KEY="+sandboxLocalTestKey), 0600) != nil {
		t.Fatal("DEV_TEST_INPUT_FIXTURE")
	}
	key, err := readSandboxDevKey(path)
	if err != nil || key != sandboxLocalTestKey {
		t.Fatal("DEV_TEST_INPUT_READ")
	}
	link := filepath.Join(dir, "link")
	if os.Symlink(path, link) != nil {
		t.Fatal("DEV_TEST_SYMLINK_FIXTURE")
	}
	fifo := filepath.Join(dir, "fifo")
	if unix.Mkfifo(fifo, 0600) != nil {
		t.Fatal("DEV_TEST_FIFO_FIXTURE")
	}
	for _, candidate := range []string{dir, link, fifo, filepath.Join(dir, "missing")} {
		if key, err := readSandboxDevKey(candidate); key != "" || err == nil {
			t.Fatal("DEV_TEST_NONREGULAR_INPUT_ACCEPTED")
		}
	}
	if os.WriteFile(path, []byte(strings.Repeat("x", sandboxDevInputLimit+1)), 0600) != nil {
		t.Fatal("DEV_TEST_OVERSIZE_FIXTURE")
	}
	if key, err := readSandboxDevKey(path); key != "" || err == nil {
		t.Fatal("DEV_TEST_OVERSIZE_INPUT_ACCEPTED")
	}
}

func sandboxDevTestReady() string {
	return "Ready! You are using Stripe API Version [2026-09-30.endive]. Your webhook signing secret is " + sandboxLocalTestSecret + " (^C to quit)\n"
}

func TestSandboxDevReviewedReadinessGrammar(t *testing.T) {
	for _, text := range []string{sandboxDevTestReady(), "> " + sandboxDevTestReady(), "Getting ready...\n" + sandboxDevTestReady(), "> Getting ready...\n> " + sandboxDevTestReady()} {
		secret, err := readSandboxDevReady(bufio.NewReaderSize(strings.NewReader(text), sandboxDevCLILimit))
		if err != nil || secret != sandboxLocalTestSecret {
			t.Fatal("DEV_TEST_REVIEWED_READINESS_REJECTED")
		}
	}
	for _, text := range []string{
		"", "Getting ready...\n", "Getting ready...\nGetting ready...\n" + sandboxDevTestReady(),
		"SYNTHETIC_PRIVATE_SENTINEL\n" + sandboxDevTestReady(),
		strings.ReplaceAll(sandboxDevTestReady(), "2026-09-30.endive", "2025-01-01.wrong"),
		strings.ReplaceAll(sandboxDevTestReady(), sandboxLocalTestSecret, "whsec_offline_fixture_only"),
		strings.ReplaceAll(sandboxDevTestReady(), sandboxLocalTestSecret, "whsec_"),
		strings.ReplaceAll(sandboxDevTestReady(), sandboxLocalTestSecret, "whsec_"+strings.Repeat("a", 257)),
		"\x1b[32m" + sandboxDevTestReady(), strings.TrimSuffix(sandboxDevTestReady(), "\n"),
		strings.Repeat("x", sandboxDevCLILimit+1) + "\n", "\n" + sandboxDevTestReady(),
	} {
		secret, err := readSandboxDevReady(bufio.NewReaderSize(strings.NewReader(text), sandboxDevCLILimit))
		if err == nil || secret != "" {
			t.Fatal("DEV_TEST_HOSTILE_READINESS_ACCEPTED")
		}
		assertClosedRuntime(t, err)
	}
}

func TestSandboxDevCaptureBound(t *testing.T) {
	var capture sandboxDevCapture
	for range 100 {
		if n, err := capture.Write([]byte(strings.Repeat("x", 4096))); n != 4096 || err != nil {
			t.Fatal("DEV_TEST_CAPTURE_DRAIN")
		}
	}
	if len(capture.bytes) != sandboxDevCLILimit || !capture.over {
		t.Fatal("DEV_TEST_CAPTURE_UNBOUNDED")
	}
}
