package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"possums-intake/internal/ledger"
)

func TestManagedVolumeOptionsFailBeforeStorage(t *testing.T) {
	t.Setenv("LEDGER_OPERATOR_EMAIL", "")
	t.Setenv("LEDGER_OPERATOR_PASSWORD", "")
	for _, tc := range []struct {
		name string
		args []string
		code ledger.Code
	}{
		{"serve_cannot_initialize_disk", []string{"serve", "--tinfoil-volume=ledger", "--initialize-volume"}, codeVolumeOptions},
		{"disk_init_requires_named_volume", []string{"init", "--initialize-volume"}, codeVolumeOptions},
		{"init_cannot_listen", []string{"init", "--tinfoil-volume=ledger", "--initialize-volume", "--listen=127.0.0.1:0"}, codeListen},
		{"init_validates_credentials_first", []string{"init", "--tinfoil-volume=ledger", "--initialize-volume", "--operator"}, "LEDGER_OPERATOR_SETUP_INVALID"},
		{"platform_mode_rejects_remote_interface", []string{"serve", "--tinfoil-volume=ledger", "--listen=192.0.2.1:8090"}, codeListen},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := filepath.Join(t.TempDir(), "SYNTHETIC_PRIVATE_SENTINEL")
			args := append(append([]string(nil), tc.args...), "--state", state)
			out, stderr, success := runCLI(t, args...)
			checkFailure(t, out, stderr, success, tc.code)
			if _, err := os.Stat(state); !os.IsNotExist(err) {
				t.Fatal("invalid volume options touched ledger state")
			}
		})
	}
}

func TestSyntheticServingRejectsVolumeFlags(t *testing.T) {
	for _, option := range []string{"--tinfoil-volume=ledger", "--tinfoil-volume=", "--initialize-volume", "--initialize-volume=false"} {
		out, stderr, success := runCLI(t, "synthetic-serve", option)
		checkFailure(t, out, stderr, success, codeSyntheticOptions)
	}
}

func TestVolumeSecretCannotSilentlySelectOrdinaryStorage(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal("test executable unavailable")
	}
	state := filepath.Join(t.TempDir(), "missing")
	cmd := exec.Command(binary, "-test.run=^TestCLIProcess$", "--", "serve", "--state", state)
	cmd.Env = append(helperEnv(syntheticAdminKey), "LEDGER_VOLUME_KEY_B64=SYNTHETIC_PRIVATE_SENTINEL")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	checkFailure(t, stdout.String(), stderr.String(), runErr == nil, codeVolumeOptions)
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("unselected volume secret touched ledger state")
	}
}
