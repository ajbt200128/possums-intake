package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

func TestWebsiteInitRefusesUnsafeOptionsBeforeVolumeOrStateWork(t *testing.T) {
	for _, option := range []string{"--tinfoil-volume=", "--tinfoil-volume=../ledger", "--tinfoil-volume=Ledger", "--state=relative", "--state=/state/not-ledger", "--initialize-volume", "--initialize-volume=false", "--operator", "--operator=false", "--listen=", "--listen=127.0.0.1:0"} {
		t.Run(option, func(t *testing.T) {
			t.Setenv(volumeKeyEnv, startupSentinel)
			t.Setenv("LEDGER_MANAGED_STARTUP_DIAGNOSTICS", "true")
			dir := filepath.Join(t.TempDir(), "ledger")
			args := []string{"website-init", "--state", dir, "--tinfoil-volume=ledger", option}
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

func TestWebsiteInitManagedInputFailureOwnsOnceAndDoesNotOpenState(t *testing.T) {
	t.Setenv(volumeKeyEnv, startupSentinel)
	t.Setenv("LEDGER_MANAGED_STARTUP_DIAGNOSTICS", "true")
	dir := filepath.Join(t.TempDir(), "ledger")
	args := []string{"website-init", "--state", dir, "--tinfoil-volume=ledger"}
	if startupRecorderForArgs(args) != nil {
		t.Fatal("SITE_SETUP_MANAGED_DIAGNOSTICS_SELECTED")
	}
	o := &runtimeObserver{}
	err := executeObserved(context.Background(), args, o)
	if err == nil || ledger.Failure(err).Code != codeVolumeKey || ledger.Failure(err).Outcome != "no_volume_request_sent" {
		t.Fatal("SITE_SETUP_MANAGED_KEY_FAILURE_CONTRACT")
	}
	o.want(t, failuretype.RequestInvalid)
	if _, present := os.LookupEnv(volumeKeyEnv); present {
		t.Fatal("SITE_SETUP_RETAINED_SELECTED_VOLUME_KEY")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("SITE_SETUP_CREATED_STATE_AFTER_VOLUME_REFUSAL")
	}
}

func TestWebsiteInitManagedCancellationPreservesOriginalState(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "existing"}[existing], func(t *testing.T) {
			t.Setenv(volumeKeyEnv, syntheticVolumeKey())
			dir := filepath.Join(t.TempDir(), "ledger")
			const original = "synthetic-original-state"
			if existing {
				if os.Mkdir(dir, 0700) != nil || os.WriteFile(filepath.Join(dir, "data.db"), []byte(original), 0600) != nil {
					t.Fatal("SITE_SETUP_ORIGINAL_FIXTURE_UNAVAILABLE")
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			o := &runtimeObserver{}
			err := executeObserved(ctx, []string{"website-init", "--state", dir, "--tinfoil-volume=ledger"}, o)
			if err == nil || ledger.Failure(err).Code != codeVolumeCancelled || ledger.Failure(err).Outcome != "no_volume_request_sent" {
				t.Fatal("SITE_SETUP_MANAGED_CANCELLATION_CONTRACT")
			}
			o.want(t, failuretype.Cancelled)
			if _, present := os.LookupEnv(volumeKeyEnv); present {
				t.Fatal("SITE_SETUP_RETAINED_SELECTED_VOLUME_KEY")
			}
			if !existing {
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatal("SITE_SETUP_CREATED_STATE_AFTER_CANCELLATION")
				}
				return
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 || entries[0].Name() != "data.db" {
				t.Fatal("SITE_SETUP_CHANGED_ORIGINAL_ENTRIES")
			}
			data, err := os.ReadFile(filepath.Join(dir, "data.db"))
			if err != nil || string(data) != original {
				t.Fatal("SITE_SETUP_CHANGED_ORIGINAL_STATE")
			}
		})
	}
}
