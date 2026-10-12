package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"possums-intake/internal/ledger"
)

const (
	codeSyntheticOptions ledger.Code = "LEDGER_SYNTHETIC_OPTIONS_INVALID"
	codeScratch          ledger.Code = "LEDGER_SYNTHETIC_TEMP_UNAVAILABLE"
	codeScratchCleanup   ledger.Code = "LEDGER_SYNTHETIC_CLEANUP_UNAVAILABLE"
	codeAdminKey         ledger.Code = "LEDGER_RUNTIME_KEY_INVALID"
	codeListen           ledger.Code = "LEDGER_LISTEN_INVALID"
	codeBind             ledger.Code = "LEDGER_LISTENER_START_UNAVAILABLE"
	codeServe            ledger.Code = "LEDGER_HTTP_SERVE_UNAVAILABLE"
	codeShutdown         ledger.Code = "LEDGER_HTTP_SHUTDOWN_UNAVAILABLE"
	codeHandler          ledger.Code = "LEDGER_HTTP_HANDLER_UNAVAILABLE"
	codeRuntime          ledger.Code = "LEDGER_RUNTIME_UNAVAILABLE"
	cleanupInterval                  = 10 * time.Second
	pruneInterval                    = 60 * time.Second
)

func runtimeFailure(code ledger.Code, stage, constraint, outcome, next string) *ledger.Diagnostic {
	return &ledger.Diagnostic{Code: code, Stage: stage, Constraint: constraint, Outcome: outcome, NextStep: next}
}

func validateListen(address string) error {
	return validateModeListen(address, false)
}

func validateModeListen(address string, synthetic bool) error {
	host, port, err := net.SplitHostPort(address)
	ip, ipErr := netip.ParseAddr(host)
	n, portErr := strconv.ParseUint(port, 10, 16)
	if err != nil || ipErr != nil || !(ip.IsLoopback() || (synthetic && ip.IsUnspecified())) || ip.Zone() != "" || portErr != nil || strconv.FormatUint(n, 10) != port {
		if synthetic {
			return runtimeFailure(codeListen, "startup", "numeric_loopback_or_wildcard_and_port", "no_effect", "Select a numeric loopback or wildcard address and numeric port for synthetic-serve. Hostnames, zones and other interfaces are not permitted.")
		}
		return runtimeFailure(codeListen, "startup", "numeric_loopback_and_port", "no_effect", "Select a numeric loopback address and port for local acceptance. Wildcard, remote and hostname listeners are not authorized.")
	}
	return nil
}

func localServer(ctx context.Context, handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 * 1024,
		// net/http normally logs panic payloads/stacks and transport errors.
		// Route-owned closed diagnostics are the request reporting boundary.
		ErrorLog:    log.New(io.Discard, "", 0),
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
}

type maintenanceLedger interface {
	Cleanup(context.Context, bool) (ledger.CleanupResult, error)
	Ready() bool
}

// One joined worker, no overlapping runs or catch-up loop. A delayed tick can
// retain rows longer, but accounting reads/admissions enforce expiry themselves.
func maintain(ctx context.Context, service maintenanceLedger, ticks <-chan time.Time, started time.Time) error {
	nextPrune := started.Add(pruneInterval)
	for {
		select {
		case <-ctx.Done():
			return nil
		case now, ok := <-ticks:
			if !ok || ctx.Err() != nil {
				return nil
			}
			prune := !now.Before(nextPrune)
			if prune {
				nextPrune = now.Add(pruneInterval)
			}
			_, err := service.Cleanup(ctx, prune)
			if err != nil && !service.Ready() {
				return err
			}
			// Contention/cancellation is not fatal. No automatic request logs,
			// error dumps or replay; the next ordinary tick is the next batch.
		}
	}
}

func serve(ctx context.Context, service maintenanceLedger, handler http.Handler, address string) (result error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return runtimeFailure(codeBind, "listener_start", "available_numeric_listener", "no_effect", "Listener could not open; underlying cause unknown. Check local port availability and permissions privately. No accounting HTTP operation ran; money and reservation rows are preserved.")
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	server := localServer(work, handler)
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	started := time.Now()
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	maintained := make(chan error, 1)
	go func() { maintained <- maintain(work, service, ticker.C, started) }()
	var serveDone, maintenanceDone bool
	observeServe := func(err error) {
		if result == nil && err != nil && !errors.Is(err, http.ErrServerClosed) {
			result = runtimeFailure(codeServe, "http_serve", "usable_listener", "unknown", "HTTP serving stopped; cause unknown. Delivery is not an accounting receipt. Resolve uncertain mutations using their original identity.")
		}
	}
	select {
	case <-ctx.Done():
	case err := <-served:
		serveDone = true
		observeServe(err)
	case err := <-maintained:
		maintenanceDone = true
		result = err
	}
	cancel() // Cancels request/cleanup work, but never abandons commit/fsync.
	// No shutdown deadline can establish accounting completion. Shutdown waits
	// for handlers, then we join both workers before the caller closes storage.
	if err := server.Shutdown(context.Background()); err != nil && result == nil {
		result = runtimeFailure(codeShutdown, "shutdown", "completed_http_drain", "unknown", "HTTP shutdown could not be confirmed; cause unknown. Preserve original state and resolve uncertain mutations by original identity.")
	}
	if !serveDone {
		observeServe(<-served)
	}
	if !maintenanceDone {
		if err := <-maintained; err != nil && result == nil {
			result = err
		}
	}
	return result
}
