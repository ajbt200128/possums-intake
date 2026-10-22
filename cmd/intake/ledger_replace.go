package main

import (
	"context"
	"errors"
	"os"

	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

const replaceState = "/state/ledger"

// No implicit selection, aliases, boolean coercions, duplicate flags, positional
// arguments or general-purpose purge target. Only this stopped-service command
// has discard authority. In particular, it never enters the startup recorder.
func validReplaceArgs(args []string) bool {
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		name := args[i]
		if seen[name] {
			return false
		}
		seen[name] = true
		switch name {
		case "--state", "--tinfoil-volume":
			want := replaceState
			if name == "--tinfoil-volume" {
				want = "ledger"
			}
			i++
			if i >= len(args) || args[i] != want {
				return false
			}
		case "--operator", "--discard-existing-ledger":
		default:
			return false
		}
	}
	return len(seen) == 4
}

type replaceProgress struct {
	stage       string
	started     bool
	discarded   bool
	initialized bool
}

func replaceFailure(err error, p *replaceProgress, code ledger.Code, constraint string, kind failuretype.Type) *ledger.Diagnostic {
	if err == nil {
		err = &ledger.Diagnostic{}
	}
	d := ledger.CanonicalFailure(err, kind)
	d.Code, d.Stage, d.Constraint = code, p.stage, constraint
	d.Outcome = "old_state_not_discarded"
	d.NextStep = "Replacement refused before discard. Underlying cause unknown unless a validation constraint is stated. Preserve disk/key and inspect privately; do not automatically retry."
	if p.started {
		d.Outcome = "discard_outcome_unknown"
		d.NextStep = "Discard began; old state may be partially or completely removed. Cause unknown unless a validation constraint is stated. Preserve disk/key and remaining state; reconcile privately. Never retry replacement or initialize as recovery."
	}
	if p.discarded {
		d.Outcome = "old_data_discarded_new_initialization_unconfirmed"
		d.NextStep = "Old ledger data was discarded. Fresh initialization is incomplete or unconfirmed. Preserve disk/key and new partial state; inspect privately. Never retry replacement, restore or initialize as recovery."
	}
	if p.initialized {
		d.Outcome = "replacement_initialized_cleanup_unconfirmed"
		d.NextStep = "Old data was discarded and fresh initialization completed, but local closure is unconfirmed. Preserve the new state and reconcile privately before serving. Never replay replacement."
	}
	return d
}

// Validate the existing runtime contract AND the pinned SDK's fresh native
// email/password fields without bootstrapping or opening any database. No old
// credential or collection configuration is read; this is explicitly fresh init.
func validateReplaceOperator(ctx context.Context, credentials ledger.OperatorCredentials) error {
	if err := credentials.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	collection := core.NewAuthCollection(core.CollectionNameSuperusers)
	collection.Fields.GetByName("email").(*core.EmailField).Required = true
	record := core.NewRecord(collection)
	record.SetEmail(credentials.Email)
	record.SetPassword(credentials.Password)
	for _, name := range []string{"email", "password"} {
		if err := collection.Fields.GetByName(name).ValidateValue(ctx, nil, record); err != nil {
			return ledger.CanonicalFailure(err, failuretype.RequestInvalid)
		}
	}
	return ctx.Err()
}

func executeLedgerReplace(ctx context.Context, args []string, observer failureowner.Observer) error {
	return executeLedgerReplaceUsing(ctx, args, observer,
		func() ledger.OperatorCredentials {
			return ledger.OperatorCredentials{Email: os.Getenv("LEDGER_OPERATOR_EMAIL"), Password: os.Getenv("LEDGER_OPERATOR_PASSWORD")}
		}, prepareManagedVolume, replaceLedgerState)
}

func executeLedgerReplaceUsing(ctx context.Context, args []string, observer failureowner.Observer,
	credentials func() ledger.OperatorCredentials,
	volume func(context.Context, string, string, bool) error,
	replace func(context.Context, string, ledger.OperatorCredentials, failureowner.Observer) error,
) (result error) {
	owned := false
	p := &replaceProgress{stage: "replacement_options"}
	defer func() {
		if recover() != nil {
			result = replaceFailure(nil, p, "LEDGER_REPLACE_UNAVAILABLE", "stage_completion_cause_unknown", failuretype.RuntimeSetupUnknown)
			owned = false
		}
		if result != nil && !owned {
			failureowner.Observe(observer, ledger.FailureKind(result, failuretype.RuntimeSetupUnknown))
		}
	}()
	if !validReplaceArgs(args) {
		return replaceFailure(nil, p, "LEDGER_REPLACE_OPTIONS_INVALID", "exact_stopped_service_discard_confirmation_and_fixed_target", failuretype.RequestInvalid)
	}
	p.stage = "replacement_operator"
	input := credentials()
	if err := validateReplaceOperator(ctx, input); err != nil {
		return replaceFailure(err, p, "LEDGER_REPLACE_OPERATOR_INVALID", "valid_fresh_native_operator_credentials", failuretype.RequestInvalid)
	}
	p.stage = "replacement_volume"
	if err := volume(ctx, "ledger", replaceState, false); err != nil {
		// Preserve exact control-socket outcome/category; no discard has begun.
		return err
	}
	owned = true // the state operation owns every primary and close failure
	return replace(ctx, replaceState, input, observer)
}

func replaceLedgerState(ctx context.Context, dir string, credentials ledger.OperatorCredentials, observer failureowner.Observer) error {
	return replaceLedgerStateUsing(ctx, dir, credentials, observer, inspectReplaceV2, discardReplaceTree, initializeReplacement)
}

func initializeReplacement(ctx context.Context, dir string, credentials ledger.OperatorCredentials, observer failureowner.Observer) error {
	if err := ledger.InitOperatorObserved(ctx, dir, credentials, observer); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		failureowner.Observe(observer, ledger.FailureKind(err, failuretype.InitializationUnknown))
		return err
	}
	return initializeWebsiteStateObserved(ctx, dir, observer)
}

func replaceLedgerStateUsing(ctx context.Context, dir string, credentials ledger.OperatorCredentials, observer failureowner.Observer,
	inspect func(context.Context, string, failureowner.Observer) error,
	discard func(context.Context, *replaceTree, *replaceProgress) error,
	initialize func(context.Context, string, ledger.OperatorCredentials, failureowner.Observer) error,
) (result error) {
	p := &replaceProgress{stage: "replacement_admission"}
	owned := false
	defer func() {
		if recover() != nil {
			result = replaceFailure(nil, p, "LEDGER_REPLACE_UNAVAILABLE", "stage_completion_cause_unknown", failuretype.RuntimeSetupUnknown)
			owned = false
		}
		if result != nil && !owned {
			failureowner.Observe(observer, ledger.FailureKind(result, failuretype.RuntimeSetupUnknown))
		}
	}()
	// Production reaches here only after fixed arguments, SDK credential checks
	// and Linux same-descriptor kernel mount verification. The private directory
	// seam permits isolated fixtures, not a CLI-selectable purge path.
	tree, err := openReplaceTreeUsing(ctx, dir, func(close func() error, result *error) {
		closeReplaceResource(close, p, observer, result, &owned)
	})
	if err != nil {
		if _, classified := err.(*ledger.Diagnostic); classified {
			return err
		}
		kind := failuretype.StorageOpenUnknown
		if errors.Is(err, errReplaceTree) || errors.Is(err, os.ErrNotExist) {
			kind = failuretype.PolicyRefused
		}
		return replaceFailure(err, p, "LEDGER_REPLACE_TREE_REFUSED", "bounded_regular_non_symlink_existing_ledger_tree", kind)
	}
	defer func() {
		closeReplaceResource(tree.root.Close, p, observer, &result, &owned)
	}()
	owned = true
	if err := inspect(ctx, dir, observer); err != nil {
		return err
	}
	owned = false
	if err := ctx.Err(); err != nil {
		return replaceFailure(err, p, "LEDGER_REPLACE_CANCELLED", "context_available_before_discard", failuretype.Cancelled)
	}
	p.stage = "replacement_discard"
	if err := discard(ctx, tree, p); err != nil {
		if _, classified := err.(*ledger.Diagnostic); classified {
			return err
		}
		return replaceFailure(err, p, "LEDGER_REPLACE_DISCARD_UNCONFIRMED", "single_bounded_discard_completion", failuretype.NativeWriteUnknown)
	}
	p.discarded = true
	p.stage = "replacement_initialize"
	if err := ctx.Err(); err != nil {
		return replaceFailure(err, p, "LEDGER_REPLACE_CANCELLED", "context_available_before_initialization", failuretype.Cancelled)
	}
	// Reuse normal initialization exactly once; its helpers own primary and
	// distinct SDK teardown failures. Propagation must not count them again.
	owned = true
	if err := initialize(ctx, dir, credentials, observer); err != nil {
		return replaceFailure(err, p, "LEDGER_REPLACE_INIT_UNCONFIRMED", "fresh_operator_and_website_initialization", failuretype.InitializationUnknown)
	}
	p.initialized = true
	return nil // No listener, healthcheck, success log, recorder or retry.
}

func closeReplaceResource(close func() error, p *replaceProgress, observer failureowner.Observer, result *error, owned *bool) {
	var err error
	func() {
		defer func() {
			if recover() != nil {
				err = ledger.CanonicalFailure(&ledger.Diagnostic{}, failuretype.StorageCloseUnknown)
			}
		}()
		err = close()
	}()
	if err == nil {
		return
	}
	fault := replaceFailure(err, p, "LEDGER_REPLACE_CLOSE_UNCONFIRMED", "local_resource_close_cause_unknown", failuretype.StorageCloseUnknown)
	failureowner.Observe(observer, fault.Kind())
	if *result == nil {
		*result, *owned = fault, true
	} else {
		primary, ok := (*result).(*ledger.Diagnostic)
		if !ok {
			// A read can still have an unprojected filesystem, SQLite or context
			// cause when deferred closure runs. Preserve that primary category.
			kind := failuretype.StorageValidationUnknown
			if errors.Is(*result, errReplaceLayout) || errors.Is(*result, errReplaceTree) {
				kind = failuretype.PolicyRefused
			}
			primary = replaceFailure(*result, p, "LEDGER_REPLACE_UNAVAILABLE", "stage_completion_cause_unknown", kind)
		}
		d := *primary
		d.NextStep += " Local resource closure is also unconfirmed."
		*result = &d
	}
}
