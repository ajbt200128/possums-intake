package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/httpapi"
	"possums-intake/internal/ledger"
	"possums-intake/internal/operator"
	"possums-intake/internal/payments"
	"possums-intake/internal/stripepay"
	"possums-intake/internal/telemetry"
)

func execute(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return executeContext(ctx, args)
}

func executeContext(ctx context.Context, args []string) error {
	return executeObserved(ctx, args, nil)
}

func executeObserved(ctx context.Context, args []string, observer failureowner.Observer) error {
	if len(args) > 0 && args[0] == "ledger-replace" {
		return executeLedgerReplace(ctx, args[1:], observer)
	}
	if len(args) > 0 && args[0] == "stripe-sandbox-dev" {
		return executeStripeSandboxDev(ctx, args[1:], observer)
	}
	if len(args) > 0 && args[0] == "live-payments-init" {
		return executeLivePaymentInitObserved(ctx, args[1:], observer)
	}
	if len(args) > 0 && args[0] == "synthetic-payments-init" {
		return executeSyntheticPaymentInit(ctx, args[1:])
	}
	if len(args) > 0 && args[0] == "stripe-sandbox-local" {
		return executeStripeSandboxLocal(ctx, args[1:], observer)
	}
	if len(args) > 0 && args[0] == "website-local" {
		return executeWebsiteLocal(ctx, args[1:], observer)
	}
	if len(args) > 0 && args[0] == "synthetic-healthcheck" {
		return syntheticHealthcheck(ctx, args[1:])
	}
	return executeRecorded(ctx, args, observer, startupRecorderForArgs(args))
}

func executeRecorded(ctx context.Context, args []string, observer failureowner.Observer, startup *startupRecorder) error {
	if startup != nil {
		startup.observer = observer
	}
	startup.checkpoint(phaseValidation)
	result := executeStartupObserved(ctx, args, observer, startup)
	// All existing storage/worker/scratch defers complete before the optional
	// diagnostic hold. It owns no listener, worker, retry or accounting work.
	startup.finish(ctx, result)
	return result
}

func executeStartupObserved(ctx context.Context, args []string, observer failureowner.Observer, startup *startupRecorder) error {
	return executeStartupInputs(ctx, args, observer, startup, os.Getenv)
}

func executeStartupInputs(ctx context.Context, args []string, observer failureowner.Observer, startup *startupRecorder, input func(string) string) (result error) {
	owned := false
	metricsObserver := independentMetricsObserver(observer)
	synthetic := len(args) > 0 && args[0] == "synthetic-serve"
	explicitServe := len(args) > 0 && args[0] == "serve"
	defer func() {
		if recover() != nil {
			fault := runtimeFailure(codeRuntime, "startup", "completed_local_lifecycle", "unavailable", "Underlying startup cause unknown. Preserve original or partial state and inspect privately; never reset it.")
			failureowner.Observe(observer, fault.Kind())
			if result == nil {
				result = fault
			}
			owned = true
		}
		if result != nil && !owned {
			failureowner.Observe(observer, ledger.FailureKind(result, failuretype.RuntimeSetupUnknown))
		}
		if drain, ok := result.(*unconfirmedHTTPDrain); ok {
			result = drain.Diagnostic // retain the primary after scratch policy ran
		}
	}()
	mode := "serve"
	if len(args) > 0 && (args[0] == "init" || args[0] == "website-init" || args[0] == "serve" || args[0] == "synthetic-serve") {
		mode = args[0]
		args = args[1:]
	}
	flags := flag.NewFlagSet("intake", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dir := flags.String("state", "", "Explicit absolute state directory (no default)")
	defaultListen := "127.0.0.1:8090"
	if synthetic {
		defaultListen = "0.0.0.0:8090"
	}
	listen := flags.String("listen", defaultListen, "Numeric listener")
	operatorMode := flags.Bool("operator", false, "Expose trusted native administration (init/serve only)")
	stripeLive := flags.Bool("stripe-live", false, "Explicit live Stripe Tax admission (serve with --operator only)")
	volumeName := flags.String("tinfoil-volume", "", "Opt-in managed-secret volume mount name (init/website-init/serve)")
	initializeVolume := flags.Bool("initialize-volume", false, "Explicitly initialize a blank Tinfoil disk (init only)")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || (!synthetic && !filepath.IsAbs(*dir)) {
		if synthetic {
			return runtimeFailure(codeSyntheticOptions, "startup", "synthetic_listen_only", "no_effect", "Use synthetic-serve with optional --listen only. State is always newly temporary and native operator mode is mandatory.")
		}
		if mode == "website-init" {
			return websiteSetupOptionsFailure()
		}
		return ledger.CanonicalFailure(&ledger.Diagnostic{Code: ledger.CodePath}, failuretype.RuntimeSetupUnknown)
	}
	stripeLivePresent := false
	flags.Visit(func(f *flag.Flag) { stripeLivePresent = stripeLivePresent || f.Name == "stripe-live" })
	if stripeLivePresent && (!explicitServe || !*operatorMode) {
		return livePaymentOptionsFailure()
	}
	if synthetic {
		invalidOption := false
		flags.Visit(func(f *flag.Flag) {
			invalidOption = invalidOption || f.Name == "state" || f.Name == "operator" || f.Name == "tinfoil-volume" || f.Name == "initialize-volume"
		})
		if invalidOption {
			return runtimeFailure(codeSyntheticOptions, "startup", "synthetic_listen_only", "no_effect", "Use synthetic-serve with optional --listen only. State is always newly temporary and native operator mode is mandatory.")
		}
		*operatorMode = true
	}
	if mode == "website-init" {
		invalidOption := false
		flags.Visit(func(f *flag.Flag) {
			invalidOption = invalidOption || (f.Name != "state" && f.Name != "tinfoil-volume") || (f.Name == "tinfoil-volume" && *volumeName == "")
		})
		if invalidOption || (*volumeName != "" && (!validVolumeName(*volumeName) || !validVolumeStatePath(*dir))) {
			return websiteSetupOptionsFailure()
		}
	}
	if _, supplied := os.LookupEnv("LEDGER_VOLUME_KEY_B64"); supplied && *volumeName == "" {
		return runtimeFailure(codeVolumeOptions, "volume_options", "volume_key_requires_named_volume", "no_effect", "Select --tinfoil-volume when supplying a volume key. Disposable synthetic serving cannot use persistent volume secrets.")
	}
	if *initializeVolume && (mode != "init" || *volumeName == "") {
		return runtimeFailure(codeVolumeOptions, "volume_options", "explicit_init_and_named_volume", "no_effect", "Use --initialize-volume only with init and --tinfoil-volume for a deliberately new blank disk. Never use it to recover existing state.")
	}
	// The managed exception is inert until the real parser and volume-option
	// boundary confirm explicit existing-only serving. It grants no init authority.
	if startup != nil && startup.pendingManaged && mode == "serve" && *volumeName != "" && !*initializeVolume {
		startup.pendingManaged = false
		startup.checkpoint(phaseValidation)
	}
	if mode == "init" || mode == "website-init" {
		listenSet := false
		flags.Visit(func(f *flag.Flag) { listenSet = listenSet || f.Name == "listen" })
		if listenSet {
			return runtimeFailure(codeListen, "startup", "serve_only_listener", "no_effect", "Use --listen only with serve; init creates empty state and starts no listener.")
		}
		if mode == "init" {
			if *volumeName != "" {
				if *operatorMode {
					credentials := ledger.OperatorCredentials{Email: os.Getenv("LEDGER_OPERATOR_EMAIL"), Password: os.Getenv("LEDGER_OPERATOR_PASSWORD")}
					if err := credentials.Validate(); err != nil {
						return runtimeFailure("LEDGER_OPERATOR_SETUP_INVALID", "operator_setup", "valid_runtime_operator_credentials", "no_effect", "Supply valid native operator credentials before explicitly initializing the volume. No volume operation was started.")
					}
				}
				if err := prepareManagedVolume(ctx, *volumeName, *dir, *initializeVolume); err != nil {
					return err
				}
			}
			owned = true // initialization helpers own primary and cleanup failures
			var err error
			if *operatorMode {
				err = ledger.InitOperatorObserved(ctx, *dir, ledger.OperatorCredentials{Email: os.Getenv("LEDGER_OPERATOR_EMAIL"), Password: os.Getenv("LEDGER_OPERATOR_PASSWORD")}, observer)
			} else {
				err = ledger.InitObserved(ctx, *dir, observer)
			}
			if err != nil {
				return err
			}
		} else if *volumeName != "" {
			// Additive setup unlocks existing storage only; it grants no ledger,
			// disk or operator-credential initialization authority.
			if err := prepareManagedVolume(ctx, *volumeName, *dir, false); err != nil {
				return err
			}
		}
		owned = true
		if err := initializeWebsiteStateObserved(ctx, *dir, observer); err != nil {
			return err
		}
		status := "initialized_empty"
		if mode == "website-init" {
			status = "website_initialized"
		}
		if err := json.NewEncoder(os.Stdout).Encode(struct {
			Status string `json:"status"`
		}{status}); err != nil {
			fault := ledger.CanonicalFailure(err, failuretype.ResponseDeliveryUnknown)
			failureowner.Observe(observer, fault.Kind())
			return fault
		}
		return nil
	}
	if err := validateModeListen(*listen, synthetic || *volumeName != ""); err != nil {
		return err
	}
	var stripeClient *stripepay.Client
	if *stripeLive {
		var err error
		stripeClient, err = stripepay.NewLive(stripepay.LiveConfig{
			APIKey: input("STRIPE_LIVE_API_KEY"), WebhookSecret: input("STRIPE_LIVE_WEBHOOK_SECRET"),
			TaxCode: input("STRIPE_LIVE_TAX_CODE"), ReturnOrigin: input("STRIPE_RETURN_ORIGIN"),
		})
		if err != nil {
			return livePaymentDiagnostic(err)
		}
	}
	key := os.Getenv("LEDGER_ADMIN_KEY") // Raw bytes; never normalize or print it.
	if len(key) < 32 {
		return runtimeFailure(codeAdminKey, "startup", "runtime_admin_key_at_least_32_bytes", "no_effect", "Inject a high-entropy admin key of at least 32 bytes through LEDGER_ADMIN_KEY. Never put it in command arguments or logs.")
	}
	if *volumeName != "" {
		startup.checkpoint(phaseVolumePrepare)
		if err := prepareManagedVolume(ctx, *volumeName, *dir, false); err != nil {
			return err
		}
	}
	removeScratch := false
	if synthetic {
		credentials := ledger.OperatorCredentials{Email: os.Getenv("LEDGER_OPERATOR_EMAIL"), Password: os.Getenv("LEDGER_OPERATOR_PASSWORD")}
		if err := credentials.Validate(); err != nil {
			return runtimeFailure("LEDGER_OPERATOR_SETUP_INVALID", "operator_setup", "valid_runtime_operator_credentials", "no_effect", "Supply valid LEDGER_OPERATOR_EMAIL and LEDGER_OPERATOR_PASSWORD through runtime environment for synthetic-serve. No scratch state was created.")
		}
		startup.checkpoint(phaseScratch)
		scratch, err := os.MkdirTemp("", "possums-intake-synthetic-")
		if err != nil {
			return runtimeFailure(codeScratch, "scratch_create", "new_temporary_directory", "no_effect", "Temporary state could not be created; cause unknown. Check runtime temporary storage privately.")
		}
		*dir = scratch
		defer func() {
			if err := cleanupScratch(scratch, removeScratch); err != nil {
				failureowner.Observe(observer, ledger.FailureKind(err, failuretype.RuntimeShutdownUnknown))
				if result == nil {
					result, owned = err, true
				}
			}
		}()
		// Init/Open failures may hide unconfirmed framework storage teardown.
		// Preserve that scratch state rather than unlinking possibly open files.
		startup.checkpoint(phaseInit)
		if err := ledger.InitOperatorObserved(ctx, scratch, credentials, observer); err != nil {
			owned = true
			return err
		}
	}
	startup.checkpoint(phaseOpen)
	service, err := ledger.OpenObserved(ctx, *dir, observer)
	if err != nil {
		owned = true
		return err
	}
	defer func() {
		// Close waits for actual transaction completion. A cleanup failure must
		// not replace a previously observed startup/listener/accounting failure.
		panicked := recover() != nil
		closeErr := service.CloseObserved(observer)
		if panicked {
			fault := runtimeFailure(codeRuntime, "shutdown", "completed_local_lifecycle", "unknown", "Underlying lifecycle cause unknown. Temporary state is preserved; inspect privately and do not reuse it.")
			failureowner.Observe(observer, fault.Kind())
			if result == nil {
				result = fault
			}
			owned = true
		}
		removeScratch = closeErr == nil && !httpDrainUnconfirmed(result) && (result == nil || ledger.Failure(result).Code != codeRuntime)
		if result == nil && closeErr != nil {
			result, owned = closeErr, true
		}
	}()
	var paymentService *payments.Service
	if stripeClient != nil {
		paymentService, err = payments.NewLiveObserved(ctx, service, stripeClient, observer)
		if err != nil {
			owned = true // admission owns every primary and secondary failure
			return livePaymentDiagnostic(err)
		}
		// Successful admission owns no later startup failure.
	}
	var metrics httpapi.TerminalObserver
	if !synthetic {
		runtime := telemetry.New(ctx)
		metrics = runtime
		if observer == nil {
			observer = runtime
		}
		observeMetricsSetup(runtime.State(), metricsObserver)
		defer func() {
			observeMetricsShutdown(runtime, metricsObserver)
		}()
	}
	startup.checkpoint(phaseHandler)
	handler, err := httpapi.NewHandler(service, func() bool { return ctx.Err() == nil && service.Ready() }, key, metrics)
	if err != nil {
		return runtimeFailure(codeHandler, "startup", "custom_accounting_handler", "no_effect", "Handler construction failed; cause unknown. Check runtime key and the qualified custom route build privately.")
	}
	if *operatorMode {
		app, err := service.OperatorAppObserved(observer)
		if err != nil {
			owned = true
			return err
		}
		handler, err = operator.NewHandlerObserved(handler, app, service, observer)
		if err != nil {
			owned = true
			return err
		}
	}
	startup.checkpoint(phaseWebsite)
	if synthetic {
		// synthetic-serve explicitly creates fresh disposable state on every run.
		app, err := service.CustomerAuthApp()
		if err != nil {
			return err
		}
		if err := initializeWebsite(ctx, app); err != nil {
			return err
		}
	}
	handler, err = newWebsiteHandlerWithPayments(service, handler, observer, paymentService)
	if err != nil {
		return err
	}
	handler = withLivePaymentWebhook(handler, paymentService)
	owned = true // serve/maintenance own primary and distinct joined failures
	startup.checkpoint(phaseBind)
	return serveStartupObserved(ctx, service, handler, *listen, observer, startup)
}

func main() {
	if err := execute(os.Args[1:]); err != nil {
		if writeErr := json.NewEncoder(os.Stderr).Encode(ledger.Failure(err)); writeErr != nil {
			_ = ledger.CanonicalFailure(writeErr, failuretype.ResponseDeliveryUnknown)
		}
		os.Exit(1)
	}
}
