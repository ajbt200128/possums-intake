package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

func TestWebsiteInitRefusesManagedOptionsBeforeVolumeOrStateWork(t *testing.T) {
	for _, option := range []string{"--tinfoil-volume=ledger", "--tinfoil-volume=", "--initialize-volume", "--initialize-volume=false", "--operator=false", "--listen=127.0.0.1:0"} {
		t.Run(option, func(t *testing.T) {
			t.Setenv(volumeKeyEnv, startupSentinel)
			t.Setenv("LEDGER_MANAGED_STARTUP_DIAGNOSTICS", "true")
			dir := filepath.Join(t.TempDir(), "ledger")
			args := []string{"website-init", "--state", dir, option}
			if startupRecorderForArgs(args) != nil {
				t.Fatal("SITE_SETUP_MANAGED_DIAGNOSTICS_SELECTED")
			}
			o := &runtimeObserver{}
			err := executeObserved(context.Background(), args, o)
			if err == nil || ledger.Failure(err).Code != "SITE_INITIALIZATION_OPTIONS_INVALID" {
				t.Fatal("SITE_SETUP_MANAGED_OPTION_ACCEPTED")
			}
			o.want(t, failuretype.RequestInvalid)
			if key, present := os.LookupEnv(volumeKeyEnv); !present || key != startupSentinel {
				t.Fatal("SITE_SETUP_CONSUMED_VOLUME_KEY")
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("SITE_SETUP_CREATED_STATE_BEFORE_OPTION_REFUSAL")
			}
		})
	}
}

func TestWebsiteInitUnselectedVolumeKeyRefusedWithoutConsumingIt(t *testing.T) {
	t.Setenv(volumeKeyEnv, startupSentinel)
	dir := filepath.Join(t.TempDir(), "ledger")
	o := &runtimeObserver{}
	err := executeObserved(context.Background(), []string{"website-init", "--state", dir}, o)
	if err == nil || ledger.Failure(err).Code != codeVolumeOptions {
		t.Fatal("SITE_SETUP_UNSELECTED_VOLUME_KEY_ACCEPTED")
	}
	o.want(t, failuretype.RequestInvalid)
	if key, present := os.LookupEnv(volumeKeyEnv); !present || key != startupSentinel {
		t.Fatal("SITE_SETUP_CONSUMED_UNSELECTED_VOLUME_KEY")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("SITE_SETUP_CREATED_STATE_BEFORE_VOLUME_REFUSAL")
	}
}
