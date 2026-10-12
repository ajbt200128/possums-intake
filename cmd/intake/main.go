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
	"possums-intake/internal/telemetry"
)

func execute(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return executeContext(ctx, args)
}

func executeContext(ctx context.Context, args []string) (result error) {
	defer func() {
		if recover() != nil {
			result = runtimeFailure(codeRuntime, "startup", "completed_local_lifecycle", "unavailable", "Underlying startup cause unknown. Preserve original state and inspect privately; never reset it.")
		}
	}()
	mode := "serve"
	if len(args) > 0 && (args[0] == "init" || args[0] == "serve") {
		mode = args[0]
		args = args[1:]
	}
	flags := flag.NewFlagSet("intake", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dir := flags.String("state", "", "Explicit absolute state directory (no default)")
	listen := flags.String("listen", "127.0.0.1:8090", "Numeric loopback listener")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || !filepath.IsAbs(*dir) {
		return &ledger.Diagnostic{Code: ledger.CodePath, Stage: "startup", Constraint: "explicit_command_and_absolute_state", Outcome: "no_effect", NextStep: "Use init or serve with --state and a deliberately selected absolute directory. Credentials are runtime environment only."}
	}
	if mode == "init" {
		listenSet := false
		flags.Visit(func(f *flag.Flag) { listenSet = listenSet || f.Name == "listen" })
		if listenSet {
			return runtimeFailure(codeListen, "startup", "serve_only_listener", "no_effect", "Use --listen only with serve; init creates empty state and starts no listener.")
		}
		if err := ledger.Init(ctx, *dir); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, `{"status":"initialized_empty"}`)
		return nil
	}
	if err := validateListen(*listen); err != nil {
		return err
	}
	key := os.Getenv("LEDGER_ADMIN_KEY") // Raw bytes; never normalize or print it.
	if len(key) < 32 {
		return runtimeFailure(codeAdminKey, "startup", "runtime_admin_key_at_least_32_bytes", "no_effect", "Inject a high-entropy admin key of at least 32 bytes through LEDGER_ADMIN_KEY. Never put it in command arguments or logs.")
	}
	service, err := ledger.Open(ctx, *dir)
	if err != nil {
		return err
	}
	defer func() {
		// Close waits for actual transaction completion. A cleanup failure must
		// not replace a previously observed startup/listener/accounting failure.
		if err := service.Close(); result == nil {
			result = err
		}
	}()
	metrics := telemetry.New(ctx) // serve only; init never constructs an exporter
	defer metrics.Shutdown()      // cancel/join, drop partial windows; never flush
	handler, err := httpapi.NewHandler(service, func() bool { return ctx.Err() == nil && service.Ready() }, key, metrics)
	if err != nil {
		return runtimeFailure(codeHandler, "startup", "custom_accounting_handler", "no_effect", "Handler construction failed; cause unknown. Check runtime key and the qualified custom route build privately.")
	}
	return serve(ctx, service, handler, *listen)
}

func main() {
	if err := execute(os.Args[1:]); err != nil {
		_ = json.NewEncoder(os.Stderr).Encode(ledger.Failure(err))
		os.Exit(1)
	}
}
