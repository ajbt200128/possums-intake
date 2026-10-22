package main

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"possums-intake/internal/ledger"
)

func TestRetiredLedgerUpgradeAndUnknownCommandNeverTouchState(t *testing.T) {
	dir := initProcessState(t)
	before := map[string][32]byte{}
	for _, name := range []string{"data.db", "auxiliary.db"} {
		contents, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal("synthetic state inspection unavailable")
		}
		before[name] = sha256.Sum256(contents)
	}
	for _, command := range []string{"ledger-upgrade", "SYNTHETIC_PRIVATE_SENTINEL"} {
		out, stderr, success := runCLI(t, command, "--state", dir)
		checkFailure(t, out, stderr, success, ledger.CodePath)
		for name, want := range before {
			contents, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil || sha256.Sum256(contents) != want {
				t.Fatal("unknown command modified synthetic state")
			}
		}
	}
}
