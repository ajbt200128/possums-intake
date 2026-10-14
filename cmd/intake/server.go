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

	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
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
	kind := failuretype.RuntimeSetupUnknown
	switch code {
	case codeSyntheticOptions, codeAdminKey, codeListen, "LEDGER_OPERATOR_SETUP_INVALID":
		kind = failuretype.RequestInvalid
	case codeBind, codeServe:
		kind = failuretype.RuntimeServeUnknown
	case codeShutdown, codeScratchCleanup:
		kind = failuretype.RuntimeShutdownUnknown
	case codeRuntime:
		if stage == "shutdown" {
			kind = failuretype.RuntimeShutdownUnknown
		}
	}
	d := ledger.CanonicalFailure(&ledger.Diagnostic{}, kind)
	d.Code, d.Stage, d.Constraint, d.Outcome, d.NextStep = code, stage, constraint, outcome, next
	return d
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
	return maintainObserved(ctx, service, ticks, started, nil)
}

func maintainObserved(ctx context.Context, service maintenanceLedger, ticks <-chan time.Time, started time.Time, observer failureowner.Observer) (result error) {
	defer func() {
		if recover() != nil {
			result = ledger.CanonicalFailure(&ledger.Diagnostic{}, failuretype.MaintenanceUnknown)
			failureowner.Observe(observer, ledger.FailureKind(result, failuretype.MaintenanceUnknown))
		}
	}()
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
			runCtx, report := ledger.WithFailureReport(ctx)
			_, err := service.Cleanup(runCtx, prune)
			ledger.ObserveMaintenanceFailures(ctx, observer, report, err)
			if err != nil && !service.Ready() {
				return ledger.CanonicalFailure(err, failuretype.MaintenanceUnknown)
			}
			// Contention/cancellation is not fatal. No automatic request logs,
			// error dumps or replay; the next ordinary tick is the next batch.
		}
	}
}

func serve(ctx context.Context, service maintenanceLedger, handler http.Handler, address string) error {
	return serveObserved(ctx, service, handler, address, nil)
}

func serveObserved(ctx context.Context, service maintenanceLedger, handler http.Handler, address string, observer failureowner.Observer) error {
	return serveStartupObserved(ctx, service, handler, address, observer, nil)
}

func serveStartupObserved(ctx context.Context, service maintenanceLedger, handler http.Handler, address string, observer failureowner.Observer, startup *startupRecorder) (result error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		fault := runtimeFailure(codeBind, "listener_start", "available_numeric_listener", "no_effect", "Listener could not open; underlying cause unknown. Check local port availability and permissions privately. No accounting HTTP operation ran; money and reservation rows are preserved.")
		failureowner.Observe(observer, fault.Kind())
		return fault
	}
	startup.listenerBound()
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	server := localServer(work, handler)
	started := time.Now()
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	return coordinateServeObserved(ctx, work, cancel, service, server, listener, ticker.C, started, observer)
}

// The default HTTP server and focused fault fixtures use this same drain/join
// coordinator. Cancellation never establishes completion or permits early close.
func coordinateServeObserved(ctx, work context.Context, cancel context.CancelFunc, service maintenanceLedger, server httpLifecycle, listener net.Listener, ticks <-chan time.Time, started time.Time, observer failureowner.Observer) (result error) {
	served := make(chan error, 1)
	go func() { served <- serveHTTP(server, listener) }()
	maintained := make(chan error, 1)
	go func() { maintained <- maintainObserved(work, service, ticks, started, observer) }()
	var serveDone, maintenanceDone bool
	observeServe := func(err error) {
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fault := ledger.CanonicalFailure(err, failuretype.RuntimeServeUnknown)
			failureowner.Observe(observer, fault.Kind())
			if result == nil {
				result = fault
			}
		}
	}
	defer func() {
		if recover() != nil {
			fault := runtimeFailure(codeRuntime, "shutdown", "joined_runtime_work", "unknown", "Runtime coordination failed; cause unknown. Preserve original state and resolve uncertain mutations by original identity.")
			failureowner.Observe(observer, fault.Kind())
			if result == nil {
				result = fault
			}
		}
		cancel() // Cancels request/cleanup work, never abandons commit/fsync.
		// No deadline establishes completion. Drain and join on normal return
		// AND coordination panic before the caller can close storage.
		if err := shutdownHTTP(server); err != nil {
			failureowner.Observe(observer, ledger.FailureKind(err, failuretype.RuntimeShutdownUnknown))
			if result == nil {
				result = err
			} else {
				// Preserve the primary and the separate unconfirmed-drain
				// evidence needed by the existing scratch-retention policy.
				result = &unconfirmedHTTPDrain{result.(*ledger.Diagnostic)}
			}
		}
		if !serveDone {
			observeServe(<-served)
		}
		if !maintenanceDone {
			if err := <-maintained; err != nil && result == nil {
				result = err
			}
		}
	}()
	select {
	case <-ctx.Done():
	case err := <-served:
		serveDone = true
		observeServe(err)
	case err := <-maintained:
		maintenanceDone = true
		result = err
	}
	return result
}
