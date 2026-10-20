package siteauth_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// Inspect only this test's isolated fresh fixture, including SQLite WAL/sidecars.
// This is evidence for the exercised path, not secure erasure or native-admin
// inability to write arbitrary sensitive data through other stock operations.
func assertNoKeyBytes(t *testing.T, dir string, values ...string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			t.Fatal("synthetic storage inspection unavailable")
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Size() > 32<<20 {
			t.Fatal("synthetic storage inspection bound unavailable")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("synthetic storage read unavailable")
		}
		for _, value := range values {
			if value == "" || bytes.Contains(data, []byte(value)) {
				t.Fatal("synthetic key material persisted outside verifier")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal("synthetic storage walk unavailable")
	}
}
