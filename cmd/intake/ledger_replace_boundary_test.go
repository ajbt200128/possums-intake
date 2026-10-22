package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

func TestLedgerReplaceMountEvidenceRefusalStopsBeforeDiscard(t *testing.T) {
	// Controlled parser evidence ONLY. Native mount/socket checks are separate
	// Linux tests; these strings do not establish a real mounted encrypted disk.
	base := "40 1 253:0 / /state rw - ext4 /dev/dm-0 rw\n"
	for _, evidence := range []string{
		"", strings.ReplaceAll(base, "rw", "ro"),
		strings.Replace(base, "253:0", "0:0", 1),
		strings.Replace(base, "ext4 /dev/dm-0", "tmpfs none", 1),
		base + "41 40 253:0 / /state/ledger rw - ext4 /dev/dm-0 rw\n",
		base + "41 40 253:0 / /state/ledger/storage rw - ext4 /dev/dm-0 rw\n",
	} {
		err := executeLedgerReplaceUsing(context.Background(), replaceArgs(), nil, replaceCredentials,
			func(context.Context, string, string, bool) error {
				if hasVolumeMount(context.Background(), strings.NewReader(evidence), "/state", 40, 253, 0) {
					t.Fatal("unsafe controlled mount evidence admitted")
				}
				return volumeFailure(codeVolumeMount, "volume_mount", "exact_dedicated_block_mount_rw_without_child_mount_required", &volumeProgress{sent: true, ok: true})
			}, func(context.Context, string, ledger.OperatorCredentials, failureowner.Observer) error {
				t.Fatal("unsafe mount reached discard")
				return nil
			})
		requireVolumeFailure(t, err, codeVolumeMount)
	}
}

func TestLedgerReplaceChangedInventoryRefusesBeforeUnlink(t *testing.T) {
	for _, mode := range []string{"added", "replaced", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			dir := replaceV2Fixture(t, nil)
			tree, err := openReplaceTree(context.Background(), dir)
			if err != nil {
				t.Fatal("fixture tree unavailable")
			}
			defer tree.root.Close()
			switch mode {
			case "added":
				err = os.WriteFile(filepath.Join(dir, "unexpected"), []byte("synthetic"), 0600)
			case "replaced":
				err = os.Rename(filepath.Join(dir, "data.db"), filepath.Join(dir, "replaced"))
				if err == nil {
					err = os.WriteFile(filepath.Join(dir, "data.db"), []byte("synthetic"), 0600)
				}
			case "symlink":
				err = os.Symlink("../outside", filepath.Join(dir, "unexpected"))
			}
			if err != nil {
				t.Fatal("inventory mutation fixture unavailable")
			}
			p := &replaceProgress{}
			err = discardReplaceTreeUsing(context.Background(), tree, p, func(string) error { t.Fatal("changed inventory reached unlink"); return nil })
			if err == nil || p.started {
				t.Fatal("changed inventory admitted")
			}
		})
	}
}

func TestLedgerReplaceActualInitializerCancellationPreservesPartialState(t *testing.T) {
	dir := replaceV2Fixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := &runtimeObserver{}
	calls := 0
	err := replaceLedgerStateUsing(ctx, dir, replaceCredentials(), o, inspectReplaceV2, discardReplaceTree,
		func(ctx context.Context, dir string, credentials ledger.OperatorCredentials, observer failureowner.Observer) error {
			calls++
			cancel()
			return initializeReplacement(ctx, dir, credentials, observer)
		})
	assertReplaceFailure(t, err, "LEDGER_REPLACE_INIT_UNCONFIRMED", "old_data_discarded_new_initialization_unconfirmed")
	if calls != 1 || len(o.kinds) != 1 || o.kinds[0] != failuretype.Cancelled {
		t.Fatal("SDK initialization cancellation ownership differs")
	}
	before := replaceSnapshot(t, dir)
	if len(before) == 0 {
		t.Fatal("actual partial SDK state was cleaned")
	}
	if err := replaceLedgerState(context.Background(), dir, replaceCredentials(), nil); err == nil {
		t.Fatal("partial SDK bootstrap admitted another discard")
	}
	if !reflect.DeepEqual(before, replaceSnapshot(t, dir)) {
		t.Fatal("refusal changed partial SDK files")
	}
}

func TestLedgerReplaceUnexpectedInputPanicAndCancellationBeforeUnlock(t *testing.T) {
	for _, mode := range []string{"panic", "cancel"} {
		ctx, cancel := context.WithCancel(context.Background())
		if mode == "cancel" {
			cancel()
		}
		o := &runtimeObserver{}
		err := executeLedgerReplaceUsing(ctx, replaceArgs(), o, func() ledger.OperatorCredentials {
			if mode == "panic" {
				panic("HOSTILE_PRIVATE_SENTINEL")
			}
			return replaceCredentials()
		}, func(context.Context, string, string, bool) error { t.Fatal("failed inputs reached unlock"); return nil },
			func(context.Context, string, ledger.OperatorCredentials, failureowner.Observer) error {
				t.Fatal("failed inputs reached discard")
				return nil
			})
		cancel()
		assertReplaceFailure(t, err, ledger.Failure(err).Code, "old_state_not_discarded")
		if len(o.kinds) != 1 || mode == "cancel" && o.kinds[0] != failuretype.Cancelled {
			t.Fatal("input failure ownership differs")
		}
	}
}

func TestLedgerReplaceDirectoryAndRootCloseFailuresKeepSeparateOwnership(t *testing.T) {
	dir := replaceV2Fixture(t, nil)
	p := &replaceProgress{stage: "replacement_admission"}
	owned := false
	o := &runtimeObserver{}
	closes := 0
	tree, err := openReplaceTreeUsing(context.Background(), dir, func(close func() error, result *error) {
		closes++
		if close() != nil {
			t.Fatal("synthetic real closure unavailable")
		}
		closeReplaceResource(func() error { return errors.New("HOSTILE_PRIVATE_SENTINEL") }, p, o, result, &owned)
	})
	if tree != nil || closes != 2 || !owned {
		t.Fatal("directory failure did not close root exactly once")
	}
	assertReplaceFailure(t, err, "LEDGER_REPLACE_CLOSE_UNCONFIRMED", "old_state_not_discarded")
	if !strings.Contains(ledger.Failure(err).NextStep, "closure is also unconfirmed") {
		t.Fatal("secondary root close uncertainty lost")
	}
	o.want(t, failuretype.StorageCloseUnknown, failuretype.StorageCloseUnknown)
}

func TestLedgerReplaceClosePanicAndRawCancellationKeepEvidence(t *testing.T) {
	p := &replaceProgress{stage: "replacement_admission"}
	result := error(context.Canceled)
	owned := false
	o := &runtimeObserver{}
	closeReplaceResource(func() error { panic("HOSTILE_PRIVATE_SENTINEL") }, p, o, &result, &owned)
	if ledger.Failure(result).Kind() != failuretype.Cancelled || owned || !strings.Contains(ledger.Failure(result).NextStep, "closure is also unconfirmed") {
		t.Fatal("cleanup lost primary cancellation")
	}
	o.want(t, failuretype.StorageCloseUnknown)
	p.initialized, p.discarded, p.started = true, true, true
	result = nil
	closeReplaceResource(func() error { return errors.New("HOSTILE_PRIVATE_SENTINEL") }, p, nil, &result, &owned)
	assertReplaceFailure(t, result, "LEDGER_REPLACE_CLOSE_UNCONFIRMED", "replacement_initialized_cleanup_unconfirmed")
}
