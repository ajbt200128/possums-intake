package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"

	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

// This stopped-service maintenance entrypoint optionally unlocks existing managed
// storage, but starts no listener, initialization, credentials, website or telemetry.
func executeLedgerUpgrade(ctx context.Context, args []string, observer failureowner.Observer) error {
	return executeLedgerUpgradeUsing(ctx, args, observer, prepareManagedVolume)
}

func executeLedgerUpgradeUsing(ctx context.Context, args []string, observer failureowner.Observer, prepare func(context.Context, string, string, bool) error) (result error) {
	schemaCurrent := false
	defer func() {
		if recover() != nil {
			kind := failuretype.StorageValidationUnknown
			outcome := "unknown"
			next := "Upgrade outcome unknown; cause unknown. Preserve original state and inspect privately; do not reset, restore or automatically repeat the operation."
			if schemaCurrent {
				kind = failuretype.ResponseDeliveryUnknown
				outcome = "schema_current_delivery_unconfirmed"
				next = "The schema was confirmed current but status delivery is unconfirmed; cause unknown. Preserve that result. Do not reset or restore state."
			}
			fault := ledgerUpgradeFailure("LEDGER_UPGRADE_UNAVAILABLE", "ledger_upgrade", "explicit_upgrade_completion_cause_unknown", outcome, next, kind)
			if result == nil {
				result = fault
			}
			failureowner.Observe(observer, kind)
		}
	}()
	flags := flag.NewFlagSet("ledger-upgrade", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dir := flags.String("state", "", "Absolute existing stopped ledger directory")
	volumeName := flags.String("tinfoil-volume", "", "Optional existing managed volume mount name")
	parseErr := flags.Parse(args)
	invalidOption := false
	flags.Visit(func(f *flag.Flag) {
		invalidOption = invalidOption || (f.Name == "tinfoil-volume" && *volumeName == "")
	})
	if parseErr != nil || flags.NArg() != 0 || !filepath.IsAbs(*dir) || filepath.Clean(*dir) == string(filepath.Separator) || invalidOption || (*volumeName != "" && (!validVolumeName(*volumeName) || !validVolumeStatePath(*dir))) {
		failureowner.Observe(observer, failuretype.RequestInvalid)
		return ledgerUpgradeFailure("LEDGER_UPGRADE_OPTIONS_INVALID", "ledger_upgrade", "explicit_existing_stopped_state_and_optional_named_volume", "no_effect", "Stop the service, then supply ledger-upgrade --state with its absolute existing directory and optional --tinfoil-volume name. Managed state must be a canonical absolute ledger child. No initialization, listener or credential options are accepted.", failuretype.RequestInvalid)
	}
	if _, supplied := os.LookupEnv(volumeKeyEnv); supplied && *volumeName == "" {
		failureowner.Observe(observer, failuretype.RequestInvalid)
		return runtimeFailure(codeVolumeOptions, "volume_options", "volume_key_requires_named_volume", "no_effect", "Select --tinfoil-volume when supplying a volume key. No volume or ledger work was started.")
	}
	if *volumeName != "" {
		if err := prepare(ctx, *volumeName, *dir, false); err != nil {
			failureowner.Observe(observer, ledger.FailureKind(err, failuretype.RuntimeSetupUnknown))
			return err
		}
	}
	if err := ledger.UpgradeObserved(ctx, *dir, observer); err != nil {
		return err // the storage boundary owns its primary and teardown failures
	}
	schemaCurrent = true
	if err := json.NewEncoder(os.Stdout).Encode(struct {
		Status string `json:"status"`
	}{"ledger_schema_current"}); err != nil {
		failureowner.Observe(observer, failuretype.ResponseDeliveryUnknown)
		return ledgerUpgradeFailure("LEDGER_UPGRADE_RESPONSE_UNAVAILABLE", "ledger_upgrade_response", "status_delivery_cause_unknown", "schema_current_delivery_unconfirmed", "The schema was confirmed current but status delivery failed; cause unknown. Preserve that result. Do not reset or restore state.", failuretype.ResponseDeliveryUnknown)
	}
	return nil
}

func ledgerUpgradeFailure(code ledger.Code, stage, constraint, outcome, next string, kind failuretype.Type) *ledger.Diagnostic {
	d := ledger.CanonicalFailure(&ledger.Diagnostic{}, kind)
	d.Code, d.Stage, d.Constraint, d.Outcome, d.NextStep = code, stage, constraint, outcome, next
	return d
}
