package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

// This explicit local setup command never unlocks a volume, initializes ledger
// state, configures Stripe, enables a listener, or creates customers/credentials.
func executeSyntheticPaymentInit(ctx context.Context, args []string) (result error) {
	defer func() {
		if recover() != nil {
			result = runtimeFailure("PAYMENT_SETUP_UNCONFIRMED", "synthetic_payment_setup", "setup_completion_cause_unknown", "unknown", "Preserve original state and inspect locally. No provider request was made; do not reset state.")
		}
	}()
	flags := flag.NewFlagSet("synthetic-payments-init", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dir := flags.String("state", "", "Absolute existing local synthetic ledger state")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || !filepath.IsAbs(*dir) {
		return runtimeFailure("PAYMENT_SETUP_OPTIONS_INVALID", "synthetic_payment_setup", "explicit_local_state_only", "no_effect", "Use synthetic-payments-init --state with an existing local synthetic ledger directory. No listener, volume or provider options are accepted.")
	}
	if _, exists := os.LookupEnv("LEDGER_VOLUME_KEY_B64"); exists {
		return runtimeFailure("PAYMENT_SETUP_OPTIONS_INVALID", "synthetic_payment_setup", "no_managed_volume_secret", "no_effect", "This local synthetic setup does not unlock managed storage. Use an isolated local synthetic state directory without volume secrets.")
	}
	service, err := ledger.Open(ctx, *dir)
	if err != nil {
		return err
	}
	defer func() {
		if err := service.Close(); err != nil && result == nil {
			result = err
		}
	}()
	if err := service.InitializeSyntheticPayments(ctx); err != nil {
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(struct {
		Status string `json:"status"`
	}{"synthetic_payments_initialized"}); err != nil {
		return ledger.CanonicalFailure(err, failuretype.ResponseDeliveryUnknown)
	}
	return nil
}
