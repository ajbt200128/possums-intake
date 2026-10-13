package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
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

func executeObserved(ctx context.Context, args []string, observer failureowner.Observer) (result error) {
	owned := false
	metricsObserver := independentMetricsObserver(observer)
	synthetic := len(args) > 0 && args[0] == "synthetic-serve"
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
	if len(args) > 0 && (args[0] == "init" || args[0] == "serve" || args[0] == "synthetic-serve") {
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
	operatorMode := flags.Bool("operator", false, "Opt-in restricted synthetic native dashboard (init/serve only)")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || (!synthetic && !filepath.IsAbs(*dir)) {
		if synthetic {
			return runtimeFailure(codeSyntheticOptions, "startup", "synthetic_listen_only", "no_effect", "Use synthetic-serve with optional --listen only. State is always newly temporary and native operator mode is mandatory.")
		}
		return ledger.CanonicalFailure(&ledger.Diagnostic{Code: ledger.CodePath}, failuretype.RuntimeSetupUnknown)
	}
	if synthetic {
		invalidOption := false
		flags.Visit(func(f *flag.Flag) { invalidOption = invalidOption || f.Name == "state" || f.Name == "operator" })
		if invalidOption {
			return runtimeFailure(codeSyntheticOptions, "startup", "synthetic_listen_only", "no_effect", "Use synthetic-serve with optional --listen only. State is always newly temporary and native operator mode is mandatory.")
		}
		*operatorMode = true
	}
	if mode == "init" {
		listenSet := false
		flags.Visit(func(f *flag.Flag) { listenSet = listenSet || f.Name == "listen" })
		if listenSet {
			return runtimeFailure(codeListen, "startup", "serve_only_listener", "no_effect", "Use --listen only with serve; init creates empty state and starts no listener.")
		}
		var err error
		if *operatorMode {
			err = ledger.InitOperatorObserved(ctx, *dir, ledger.OperatorCredentials{Email: os.Getenv("LEDGER_OPERATOR_EMAIL"), Password: os.Getenv("LEDGER_OPERATOR_PASSWORD")}, observer)
		} else {
			err = ledger.InitObserved(ctx, *dir, observer)
		}
		owned = true
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(os.Stdout, `{"status":"initialized_empty"}`); err != nil {
			fault := ledger.CanonicalFailure(err, failuretype.ResponseDeliveryUnknown)
			failureowner.Observe(observer, fault.Kind())
			return fault
		}
		return nil
	}
	if err := validateModeListen(*listen, synthetic); err != nil {
		return err
	}
	key := os.Getenv("LEDGER_ADMIN_KEY") // Raw bytes; never normalize or print it.
	if len(key) < 32 {
		return runtimeFailure(codeAdminKey, "startup", "runtime_admin_key_at_least_32_bytes", "no_effect", "Inject a high-entropy admin key of at least 32 bytes through LEDGER_ADMIN_KEY. Never put it in command arguments or logs.")
	}
	removeScratch := false
	if synthetic {
		credentials := ledger.OperatorCredentials{Email: os.Getenv("LEDGER_OPERATOR_EMAIL"), Password: os.Getenv("LEDGER_OPERATOR_PASSWORD")}
		if err := credentials.Validate(); err != nil {
			return runtimeFailure("LEDGER_OPERATOR_SETUP_INVALID", "operator_setup", "valid_runtime_operator_credentials", "no_effect", "Supply valid LEDGER_OPERATOR_EMAIL and LEDGER_OPERATOR_PASSWORD through runtime environment for synthetic-serve. No scratch state was created.")
		}
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
		if err := ledger.InitOperatorObserved(ctx, scratch, credentials, observer); err != nil {
			owned = true
			return err
		}
	}
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
	owned = true // serve/maintenance own primary and distinct joined failures
	return serveObserved(ctx, service, handler, *listen, observer)
}

func main() {
	if err := execute(os.Args[1:]); err != nil {
		if writeErr := json.NewEncoder(os.Stderr).Encode(ledger.Failure(err)); writeErr != nil {
			_ = ledger.CanonicalFailure(writeErr, failuretype.ResponseDeliveryUnknown)
		}
		os.Exit(1)
	}
}
