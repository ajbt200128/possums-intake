package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

func TestLedgerUpgradeCLIExplicitExistingOnlyAndRepeatedCurrent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	if err := ledger.Init(context.Background(), dir); err != nil {
		t.Fatal("synthetic upgrade fixture unavailable")
	}
	s, err := ledger.Open(context.Background(), dir)
	if err != nil {
		t.Fatal("synthetic upgrade open unavailable")
	}
	if _, err := s.Provision(context.Background(), "upgrade-account"); err != nil {
		t.Fatal("synthetic account unavailable")
	}
	if _, err := s.Credit(context.Background(), ledger.CreditInput{AccountID: "upgrade-account", Amount: "100", IdempotencyKey: accountingRuntimeKey(1)}); err != nil {
		t.Fatal("synthetic credit unavailable")
	}
	if _, err := s.Reserve(context.Background(), ledger.ReserveInput{AccountID: "upgrade-account", Maximum: "7", TimeoutSeconds: 1800, IdempotencyKey: accountingRuntimeKey(2)}); err != nil {
		t.Fatal("synthetic reserve unavailable")
	}
	before, err := s.Balance(context.Background(), "upgrade-account")
	if err != nil || s.Close() != nil {
		t.Fatal("synthetic fixture closure unavailable")
	}
	for range 2 {
		out, stderr, success := runCLI(t, "ledger-upgrade", "--state", dir)
		if !success || stderr != "" || out != "{\"status\":\"ledger_schema_current\"}\n" {
			t.Fatal("current schema maintenance status unavailable")
		}
	}
	s, err = ledger.Open(context.Background(), dir)
	if err != nil {
		t.Fatal("upgrade reopen unavailable")
	}
	defer s.Close()
	after, err := s.Balance(context.Background(), "upgrade-account")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("upgrade changed accounting state")
	}
}

func TestLedgerUpgradeCLIRejectsOptionsAndNeverInitializes(t *testing.T) {
	for _, args := range [][]string{
		{"ledger-upgrade"},
		{"ledger-upgrade", "--state", "relative"},
		{"ledger-upgrade", "--state", "/"},
		{"ledger-upgrade", "--state", "/synthetic", "--listen", "127.0.0.1:0"},
		{"ledger-upgrade", "--state", "/synthetic", "--initialize-volume"},
		{"ledger-upgrade", "--state", "/synthetic", "--initialize-volume=false"},
		{"ledger-upgrade", "--state", "/synthetic", "--operator"},
		{"ledger-upgrade", "--state", "/synthetic", "--operator=false"},
		{"ledger-upgrade", "--state", "/synthetic", "--tinfoil-volume", "ledger"},
		{"ledger-upgrade", "--state", "/synthetic/ledger", "--tinfoil-volume="},
		{"ledger-upgrade", "--state", "/synthetic/ledger", "--tinfoil-volume=../SYNTHETIC_PRIVATE_SENTINEL"},
		{"ledger-upgrade", "--state", "/synthetic/ledger", "SYNTHETIC_PRIVATE_SENTINEL"},
		{"ledger-upgrade", "--SYNTHETIC_PRIVATE_SENTINEL"},
	} {
		out, stderr, success := runCLI(t, args...)
		checkFailure(t, out, stderr, success, "LEDGER_UPGRADE_OPTIONS_INVALID")
	}
	missing := filepath.Join(t.TempDir(), "absent")
	out, stderr, success := runCLI(t, "ledger-upgrade", "--state", missing)
	checkFailure(t, out, stderr, success, ledger.CodeMissing)
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("maintenance created missing state")
	}
}

func TestLedgerUpgradeManagedOptionsRefusedBeforeVolumeOrStateWork(t *testing.T) {
	for _, option := range []string{
		"--tinfoil-volume=", "--tinfoil-volume=../HOSTILE", "--tinfoil-volume=Ledger",
		"--state=relative", "--state=/", "--state=/state/..", "--state=/state/not-ledger",
		"--state=/state/ledger/", "--state=/a/../state/ledger", "--state=/HOSTILE\n/ledger",
		"--initialize-volume", "--initialize-volume=false", "--operator", "--operator=false",
		"--listen=", "--listen=127.0.0.1:0", "--operator-password=HOSTILE", "--HOSTILE", "HOSTILE",
		"--LEDGER_MANAGED_STARTUP_DIAGNOSTICS=true",
	} {
		t.Run(option, func(t *testing.T) {
			t.Setenv(volumeKeyEnv, startupSentinel)
			dir := filepath.Join(t.TempDir(), "ledger")
			args := []string{"--state", dir, "--tinfoil-volume=ledger", option}
			o := &runtimeObserver{}
			err := executeLedgerUpgradeUsing(context.Background(), args, o,
				func(context.Context, string, string, bool) error {
					t.Fatal("invalid options started volume preparation")
					return nil
				})
			d := requireLedgerUpgradeFailure(t, err, "LEDGER_UPGRADE_OPTIONS_INVALID", failuretype.RequestInvalid)
			if d.Outcome != "no_effect" {
				t.Fatal("option refusal lost no-effect outcome")
			}
			o.want(t, failuretype.RequestInvalid)
			if key, present := os.LookupEnv(volumeKeyEnv); !present || key != startupSentinel {
				t.Fatal("invalid options consumed volume key")
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("invalid options created state")
			}
		})
	}
}

func TestLedgerUpgradeUnnamedVolumeKeyRefusedWithoutConsumption(t *testing.T) {
	for _, key := range []string{"", startupSentinel, syntheticVolumeKey()} {
		t.Setenv(volumeKeyEnv, key)
		dir := filepath.Join(t.TempDir(), "ledger")
		o := &runtimeObserver{}
		err := executeObserved(context.Background(), []string{"ledger-upgrade", "--state", dir}, o)
		requireLedgerUpgradeFailure(t, err, codeVolumeOptions, failuretype.RequestInvalid)
		o.want(t, failuretype.RequestInvalid)
		if retained, present := os.LookupEnv(volumeKeyEnv); !present || retained != key {
			t.Fatal("unnamed volume key consumed")
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("unnamed key refusal created state")
		}
	}
}

func TestLedgerUpgradeManagedPreparationOnceExistingOnlyBeforeUpgrade(t *testing.T) {
	t.Setenv(volumeKeyEnv, syntheticVolumeKey())
	root := t.TempDir()
	staged, dir := filepath.Join(root, "staged"), filepath.Join(root, "ledger")
	if err := ledger.Init(context.Background(), staged); err != nil {
		t.Fatal("synthetic staged fixture unavailable")
	}
	calls := 0
	o := &runtimeObserver{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := executeLedgerUpgradeUsing(ctx, []string{"--state", dir, "--tinfoil-volume=ledger"}, o,
		func(gotCtx context.Context, name, state string, initialize bool) error {
			calls++
			if gotCtx != ctx || name != "ledger" || state != dir || initialize {
				t.Fatal("maintenance changed existing-only preparation contract")
			}
			return prepareManagedVolumeUsing(gotCtx, name, state, initialize,
				func(_ context.Context, _, _ string, packet []byte, _ *volumeProgress) error {
					if len(packet) != 34 || packet[0] != 2 || packet[1] != 'u' {
						t.Fatal("maintenance requested initialization")
					}
					// Model a mount exposing pre-existing state, not initialization.
					if _, err := os.Stat(dir); !os.IsNotExist(err) {
						t.Fatal("ledger work started before preparation")
					}
					return os.Rename(staged, dir)
				})
		})
	if err != nil || calls != 1 {
		t.Fatal("prepared existing state was not upgraded exactly once")
	}
	o.want(t)
	if _, present := os.LookupEnv(volumeKeyEnv); present {
		t.Fatal("selected synthetic key retained")
	}
}

func TestLedgerUpgradeManagedUnlockFailurePreservesStateAndOwnsOnce(t *testing.T) {
	for _, code := range []ledger.Code{codeVolumeRejected, codeVolumeMount, codeVolumeCleanup, codeVolumeUnknown} {
		t.Run(string(code), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "ledger")
			if err := ledger.Init(context.Background(), dir); err != nil {
				t.Fatal("synthetic original state unavailable")
			}
			before := ledgerUpgradeStateFiles(t, dir)
			primary := volumeFailure(code, "volume_prepare", "cause_unknown", &volumeProgress{sent: true})
			o := &runtimeObserver{}
			calls := 0
			err := executeLedgerUpgradeUsing(context.Background(), []string{"--state", dir, "--tinfoil-volume=ledger"}, o,
				func(context.Context, string, string, bool) error { calls++; return primary })
			if err != primary || calls != 1 {
				t.Fatal("volume failure replaced or preparation repeated")
			}
			requireVolumeFailure(t, err, code)
			o.want(t, primary.Kind())
			if !reflect.DeepEqual(before, ledgerUpgradeStateFiles(t, dir)) {
				t.Fatal("unlock failure changed original database files")
			}
		})
	}
}

func TestLedgerUpgradeManagedKeyValidationAndCancellationWithoutRecorder(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		for _, existing := range []bool{false, true} {
			t.Run(map[bool]string{false: "invalid_key", true: "cancelled"}[cancelled]+map[bool]string{false: "_absent", true: "_existing"}[existing], func(t *testing.T) {
				t.Setenv(volumeKeyEnv, startupSentinel)
				t.Setenv("LEDGER_MANAGED_STARTUP_DIAGNOSTICS", "true")
				t.Setenv("LEDGER_SYNTHETIC_STARTUP_DIAGNOSTICS", "true")
				dir := filepath.Join(t.TempDir(), "ledger")
				if existing {
					if os.Mkdir(dir, 0700) != nil || os.WriteFile(filepath.Join(dir, "data.db"), []byte(startupSentinel), 0600) != nil {
						t.Fatal("synthetic original bytes unavailable")
					}
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				code := codeVolumeKey
				kind := failuretype.RequestInvalid
				if cancelled {
					t.Setenv(volumeKeyEnv, syntheticVolumeKey())
					cancel()
					code, kind = codeVolumeCancelled, failuretype.Cancelled
				}
				args := []string{"ledger-upgrade", "--state", dir, "--tinfoil-volume=ledger"}
				if startupRecorderForArgs(args) != nil {
					t.Fatal("maintenance activated startup recorder")
				}
				o := &runtimeObserver{}
				err := executeObserved(ctx, args, o)
				d := requireVolumeFailure(t, err, code)
				if d.Outcome != "no_volume_request_sent" {
					t.Fatal("validation or cancellation sent volume request")
				}
				o.want(t, kind)
				if _, present := os.LookupEnv(volumeKeyEnv); present {
					t.Fatal("selected synthetic key not consumed")
				}
				if !existing {
					if _, err := os.Stat(dir); !os.IsNotExist(err) {
						t.Fatal("failed preparation created missing state")
					}
					return
				}
				files := ledgerUpgradeStateFiles(t, dir)
				if len(files) != 1 || string(files["data.db"]) != startupSentinel {
					t.Fatal("failed preparation opened or changed original state")
				}
			})
		}
	}
}

func TestLedgerUpgradePreparedMissingStateStillRefusedAndStorageOwnsFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	calls := 0
	o := &runtimeObserver{}
	err := executeLedgerUpgradeUsing(context.Background(), []string{"--state", dir, "--tinfoil-volume=ledger"}, o,
		func(context.Context, string, string, bool) error { calls++; return nil })
	requireLedgerUpgradeFailure(t, err, ledger.CodeMissing, failuretype.StorageOpenUnknown)
	o.want(t, failuretype.StorageOpenUnknown)
	if calls != 1 {
		t.Fatal("missing state repeated volume preparation")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("successful unlock initialized missing state")
	}
}

func TestLedgerUpgradePreparationPanicIsClosedAndOwnedOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	o := &runtimeObserver{}
	err := executeLedgerUpgradeUsing(context.Background(), []string{"--state", dir, "--tinfoil-volume=ledger"}, o,
		func(context.Context, string, string, bool) error { panic(startupSentinel) })
	d := requireLedgerUpgradeFailure(t, err, "LEDGER_UPGRADE_UNAVAILABLE", failuretype.StorageValidationUnknown)
	if d.Stage != "ledger_upgrade" || d.Outcome != "unknown" || !strings.Contains(d.Constraint, "cause_unknown") {
		t.Fatal("panic lost stage or unknown outcome")
	}
	o.want(t, failuretype.StorageValidationUnknown)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("preparation panic created state")
	}
}

func TestLedgerUpgradeDeliveryFailurePreservesConfirmedSchema(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	if err := ledger.Init(context.Background(), dir); err != nil {
		t.Fatal("synthetic upgrade fixture unavailable")
	}
	closed, err := os.CreateTemp(t.TempDir(), "closed-output")
	if err != nil || closed.Close() != nil {
		t.Fatal("synthetic closed output unavailable")
	}
	stdout := os.Stdout
	os.Stdout = closed
	defer func() { os.Stdout = stdout }()
	o := &runtimeObserver{}
	err = executeLedgerUpgradeUsing(context.Background(), []string{"--state", dir}, o,
		func(context.Context, string, string, bool) error {
			t.Fatal("unmanaged maintenance prepared volume")
			return nil
		})
	d := requireLedgerUpgradeFailure(t, err, "LEDGER_UPGRADE_RESPONSE_UNAVAILABLE", failuretype.ResponseDeliveryUnknown)
	if d.Outcome != "schema_current_delivery_unconfirmed" {
		t.Fatal("delivery failure erased confirmed schema result")
	}
	o.want(t, failuretype.ResponseDeliveryUnknown)
}

func requireLedgerUpgradeFailure(t *testing.T, err error, code ledger.Code, kind failuretype.Type) *ledger.Diagnostic {
	t.Helper()
	if err == nil {
		t.Fatal("expected typed upgrade refusal")
	}
	d := ledger.Failure(err)
	if d.Code != code || ledger.FailureKind(err, failuretype.RuntimeSetupUnknown) != kind || d.Stage == "" || d.Constraint == "" || d.Outcome == "" || d.NextStep == "" {
		t.Fatal("upgrade failure lost closed classification")
	}
	wire, marshalErr := json.Marshal(d)
	if marshalErr != nil {
		t.Fatal("upgrade diagnostic serialization unavailable")
	}
	for _, hostile := range []string{startupSentinel, syntheticVolumeKey(), "HOSTILE", "panic:", "goroutine "} {
		if strings.Contains(string(wire), hostile) || strings.Contains(err.Error(), hostile) {
			t.Fatal("upgrade diagnostic disclosed hostile data")
		}
	}
	return d
}

func ledgerUpgradeStateFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files[name] = data
		return nil
	})
	if err != nil {
		t.Fatal("synthetic original state snapshot unavailable")
	}
	return files
}
