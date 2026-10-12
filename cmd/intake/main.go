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

func executeContext(ctx context.Context, args []string) (result error) {
	synthetic := len(args) > 0 && args[0] == "synthetic-serve"
	defer func() {
		if recover() != nil {
			if synthetic {
				result = runtimeFailure(codeRuntime, "startup", "completed_local_lifecycle", "unknown", "Underlying synthetic startup cause unknown. Temporary state may remain; inspect privately and do not reuse it.")
				return
			}
			result = runtimeFailure(codeRuntime, "startup", "completed_local_lifecycle", "unavailable", "Underlying startup cause unknown. Preserve original state and inspect privately; never reset it.")
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
		return &ledger.Diagnostic{Code: ledger.CodePath, Stage: "startup", Constraint: "explicit_command_and_absolute_state", Outcome: "no_effect", NextStep: "Use init or serve with --state and a deliberately selected absolute directory. Credentials are runtime environment only."}
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
			err = ledger.InitOperator(ctx, *dir, ledger.OperatorCredentials{Email: os.Getenv("LEDGER_OPERATOR_EMAIL"), Password: os.Getenv("LEDGER_OPERATOR_PASSWORD")})
		} else {
			err = ledger.Init(ctx, *dir)
		}
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, `{"status":"initialized_empty"}`)
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
			if err := cleanupScratch(scratch, removeScratch); err != nil && result == nil {
				result = err
			}
		}()
		// Init/Open failures may hide unconfirmed framework storage teardown.
		// Preserve that scratch state rather than unlinking possibly open files.
		if err := ledger.InitOperator(ctx, scratch, credentials); err != nil {
			return err
		}
	}
	service, err := ledger.Open(ctx, *dir)
	if err != nil {
		return err
	}
	defer func() {
		// Close waits for actual transaction completion. A cleanup failure must
		// not replace a previously observed startup/listener/accounting failure.
		panicked := false
		if synthetic {
			panicked = recover() != nil
		}
		closeErr := service.Close()
		if panicked {
			result = runtimeFailure(codeRuntime, "shutdown", "completed_local_lifecycle", "unknown", "Underlying lifecycle cause unknown. Temporary state is preserved; inspect privately and do not reuse it.")
		}
		removeScratch = closeErr == nil && (result == nil || (ledger.Failure(result).Code != codeShutdown && ledger.Failure(result).Code != codeRuntime))
		if result == nil {
			result = closeErr
		}
	}()
	var metrics httpapi.TerminalObserver
	if !synthetic {
		runtime := telemetry.New(ctx)
		metrics = runtime
		defer runtime.Shutdown() // cancel/join, drop partial windows; never flush
	}
	handler, err := httpapi.NewHandler(service, func() bool { return ctx.Err() == nil && service.Ready() }, key, metrics)
	if err != nil {
		return runtimeFailure(codeHandler, "startup", "custom_accounting_handler", "no_effect", "Handler construction failed; cause unknown. Check runtime key and the qualified custom route build privately.")
	}
	if *operatorMode {
		app, err := service.OperatorApp()
		if err != nil {
			return err
		}
		handler, err = operator.NewHandler(handler, app, service)
		if err != nil {
			return runtimeFailure(codeHandler, "operator_setup", "restricted_native_router", "no_effect", "Native operator router unavailable; cause unknown. Check the qualified pinned build privately. Serve never creates or resets a login.")
		}
	}
	return serve(ctx, service, handler, *listen)
}

func main() {
	if err := execute(os.Args[1:]); err != nil {
		_ = json.NewEncoder(os.Stderr).Encode(ledger.Failure(err))
		os.Exit(1)
	}
}
