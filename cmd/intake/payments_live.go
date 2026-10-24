package main

import (
	"context"
	"flag"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
	"possums-intake/internal/payments"
	"possums-intake/internal/stripepay"
)

func livePaymentOptionsFailure() *ledger.Diagnostic {
	d := ledger.CanonicalFailure(&ledger.Diagnostic{}, failuretype.RequestInvalid)
	d.Code, d.Stage, d.Constraint, d.Outcome = "PAYMENT_LIVE_OPTIONS_INVALID", "live_payment_options", "explicit_existing_setup_or_operator_serve_only", "no_effect"
	d.NextStep = "Use live-payments-init with only an absolute existing --state and optional existing --tinfoil-volume, or select --stripe-live only for serve with --operator. Setup never initializes a disk or credentials; environment alone never enables payments."
	return d
}

// This command adds only absent exact v2 payment state. It never constructs a
// Stripe client, looks up operator/accounting credentials, or starts a listener.
func executeLivePaymentInitObserved(ctx context.Context, args []string, observer failureowner.Observer) (result error) {
	owned := false
	defer func() {
		if recover() != nil {
			fault := runtimeFailure("PAYMENT_LIVE_SETUP_UNCONFIRMED", "live_payment_setup", "setup_completion_cause_unknown", "unknown", "Underlying setup cause and local completion unknown. Preserve original or partial state and prior effects; inspect privately without replay or reset. Setup initiates no provider request.")
			failureowner.Observe(observer, fault.Kind())
			if result == nil {
				result = fault
			}
			owned = true
		}
		if result != nil && !owned {
			failureowner.Observe(observer, ledger.FailureKind(result, failuretype.RuntimeSetupUnknown))
		}
	}()
	flags := flag.NewFlagSet("live-payments-init", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dir := flags.String("state", "", "Absolute existing ledger state")
	volume := flags.String("tinfoil-volume", "", "Existing managed volume name")
	if flags.Parse(args) != nil || flags.NArg() != 0 || !filepath.IsAbs(*dir) {
		return livePaymentOptionsFailure()
	}
	invalid := false
	flags.Visit(func(f *flag.Flag) { invalid = invalid || f.Name == "tinfoil-volume" && *volume == "" })
	if invalid || (*volume != "" && (!validVolumeName(*volume) || !validVolumeStatePath(*dir))) {
		return livePaymentOptionsFailure()
	}
	if _, present := os.LookupEnv(volumeKeyEnv); present && *volume == "" {
		return runtimeFailure(codeVolumeOptions, "volume_options", "volume_key_requires_named_volume", "no_effect", "Select the existing named volume before payment setup. No storage or provider work was started; preserve the original disk and key.")
	}
	if *volume != "" {
		if err := prepareManagedVolume(ctx, *volume, *dir, false); err != nil {
			return err
		}
	}
	service, err := ledger.OpenObserved(ctx, *dir, observer)
	if err != nil {
		owned = true
		return err
	}
	defer func() {
		if err := service.CloseObserved(observer); err != nil && result == nil {
			result, owned = err, true
		}
	}()
	call, report := ledger.WithFailureReport(ctx)
	err = service.InitializeLiveStripePayments(call)
	ledger.ObserveRunFailures(observer, report, err, failuretype.RuntimeSetupUnknown)
	owned = err != nil
	return err // Passive successful exit only: no success log or publication.
}

// The CLI understands ledger diagnostics only. Project only the exact closed
// constructor/admission variants reachable here, never arbitrary exported fields
// or an exception graph. Core diagnostics already carry authoritative outcomes.
func livePaymentDiagnostic(err error) *ledger.Diagnostic {
	if d, ok := err.(*ledger.Diagnostic); ok && d != nil {
		return d
	}
	makeDiagnostic := func(code ledger.Code, stage, constraint, outcome, next string, kind failuretype.Type) *ledger.Diagnostic {
		d := ledger.CanonicalFailure(&ledger.Diagnostic{}, kind)
		d.Code, d.Stage, d.Constraint, d.Outcome, d.NextStep = code, stage, constraint, outcome, next
		return d
	}
	if e, ok := err.(*stripepay.Error); ok && e != nil && e.Stage == stripepay.StageLiveConfiguration && e.Outcome == stripepay.OutcomeNotAttempted && e.HTTPStatus == 0 {
		if e.Code == stripepay.CodeLiveConfiguration && e.Constraint == stripepay.ConstraintLive {
			return makeDiagnostic("STRIPE_LIVE_CONFIGURATION_INVALID", "live_configuration", "fixed_live_and_private_live_configuration_required", "not_attempted", "Correct the private live key, endpoint signing secret, explicitly selected product tax code and canonical HTTPS return origin. No provider request or storage work was started.", failuretype.RequestInvalid)
		}
		if e.Code == stripepay.CodeUnexpected && e.Constraint == stripepay.ConstraintUnknown {
			return makeDiagnostic("STRIPE_COMPONENT_CAUSE_UNKNOWN", "live_configuration", "component_cause_unknown", "not_attempted", "Live adapter configuration cause unknown. Preserve original state and inspect privately; no provider request or storage work was started. Do not replay a mutation to diagnose it.", failuretype.RuntimeSetupUnknown)
		}
	}
	if e, ok := err.(*payments.Error); ok && e != nil && e.Stage == "payment_admission" && e.HTTPStatus == 0 {
		switch {
		case e.Code == "PAYMENT_LIVE_CONFIGURATION_INVALID" && e.Constraint == "explicit_initialized_live_ledger_and_tax_adapter_required" && e.Outcome == "not_attempted":
			return makeDiagnostic("PAYMENT_LIVE_CONFIGURATION_INVALID", "payment_admission", "explicit_initialized_live_ledger_and_tax_adapter_required", "not_attempted", "Use explicitly initialized live state and the qualified live adapter. Admission made no provider request; preserve original state and do not initialize as recovery.", failuretype.RequestInvalid)
		case e.Code == "PAYMENT_MODE_MISMATCH" && e.Constraint == "persisted_payment_identity_must_match_adapter" && e.Outcome == "not_attempted":
			return makeDiagnostic("PAYMENT_MODE_MISMATCH", "payment_admission", "persisted_payment_identity_must_match_adapter", "not_attempted", "The existing payment mode does not match live admission. Preserve original state; never relabel, reset or migrate synthetic payments. No provider request was started.", failuretype.PolicyRefused)
		case e.Code == "PAYMENT_PURGE_UNAVAILABLE" && e.Constraint == "complete_bounded_startup_purge_required" && e.Outcome == "unavailable":
			return makeDiagnostic("PAYMENT_PURGE_UNAVAILABLE", "payment_admission", "complete_bounded_startup_purge_required", "unavailable", "Startup purge completion is unavailable; underlying cause unknown. Preserve original state and previously completed effects; inspect privately without replay. No provider request was started.", failuretype.RuntimeSetupUnknown)
		case e.Code == "PAYMENT_COMPONENT_CAUSE_UNKNOWN" && e.Constraint == "component_cause_unknown" && e.Outcome == "unknown":
			return makeDiagnostic("PAYMENT_COMPONENT_CAUSE_UNKNOWN", "payment_admission", "component_cause_unknown", "unknown", "Payment admission cause and local completion unknown. Preserve original state and prior effects; inspect privately without replay. Admission initiates no provider request.", failuretype.RuntimeSetupUnknown)
		}
	}
	return makeDiagnostic("PAYMENT_LIVE_RUNTIME_UNAVAILABLE", "live_payment_runtime", "component_cause_unknown", "unknown", "Underlying live payment runtime cause and local completion unknown. Preserve original or partial state and previously observed effects; inspect privately. Do not replay setup or a mutation to diagnose it; setup/admission initiates no provider request.", failuretype.RuntimeSetupUnknown)
}

// Mount without ServeMux normalization or website/native body parsing. The raw
// handler retains its own exact-path/query/method/signature admission checks.
func withLivePaymentWebhook(fallback http.Handler, service *payments.Service) http.Handler {
	if service == nil {
		return fallback
	}
	webhook := service.WebhookHandler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == payments.WebhookPath {
			webhook.ServeHTTP(w, r)
			return
		}
		fallback.ServeHTTP(w, r)
	})
}
