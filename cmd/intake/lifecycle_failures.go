package main

import (
	"context"
	"net"

	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
	"possums-intake/internal/telemetry"
)

type httpDrain interface{ Shutdown(context.Context) error }

func shutdownHTTP(server httpDrain) (result error) {
	defer func() {
		if recover() != nil {
			result = &ledger.Diagnostic{}
		}
		if result != nil {
			d := ledger.CanonicalFailure(result, failuretype.RuntimeShutdownUnknown)
			d.Code, d.Stage, d.Constraint, d.Outcome = codeShutdown, "shutdown", "completed_http_drain", "unknown"
			d.NextStep = "HTTP shutdown could not be confirmed; cause unknown. Preserve original state and resolve uncertain mutations by original identity."
			result = d
		}
	}()
	return server.Shutdown(context.Background())
}

// Keep only the closed primary and one bit of local drain evidence. The
// secondary was already classified/observed; no cause or error graph survives.
// execute unwraps this after its scratch-retention decision, before presentation.
type unconfirmedHTTPDrain struct{ *ledger.Diagnostic }

func (e *unconfirmedHTTPDrain) Unwrap() error { return e.Diagnostic }

func httpDrainUnconfirmed(err error) bool {
	if _, unconfirmed := err.(*unconfirmedHTTPDrain); unconfirmed {
		return true
	}
	return err != nil && ledger.Failure(err).Code == codeShutdown
}

type httpLifecycle interface {
	httpDrain
	Serve(net.Listener) error
}

func serveHTTP(server httpLifecycle, listener net.Listener) (result error) {
	defer func() {
		if recover() != nil {
			result = ledger.CanonicalFailure(&ledger.Diagnostic{}, failuretype.RuntimeServeUnknown)
		}
	}()
	return server.Serve(listener)
}

// A supplied Runtime is never an independent telemetry-fault sink, even if it
// is not the runtime constructed by execute. Use the supported concrete type,
// not type-name labels or reflection. Nil means classification without delivery.
func independentMetricsObserver(observer failureowner.Observer) failureowner.Observer {
	if _, recursive := observer.(*telemetry.Runtime); recursive {
		return nil
	}
	return observer
}

func observeMetricsSetup(state telemetry.State, observer failureowner.Observer) {
	if state == telemetry.InvalidConfig || state == telemetry.SDKUnavailable {
		d := ledger.CanonicalFailure(&ledger.Diagnostic{}, failuretype.RuntimeSetupUnknown)
		failureowner.Observe(independentMetricsObserver(observer), d.Kind())
	}
}

func observeMetricsShutdown(runtime metricsShutdown, observer failureowner.Observer) {
	if err := shutdownMetrics(runtime); err != nil {
		failureowner.Observe(independentMetricsObserver(observer), ledger.FailureKind(err, failuretype.RuntimeShutdownUnknown))
	}
}

type metricsShutdown interface{ Shutdown() }

// Telemetry failures must not recursively enter their own pipeline. These
// local classifications establish no export outcome and trigger no retry.
func shutdownMetrics(runtime metricsShutdown) (result error) {
	defer func() {
		if recover() != nil {
			result = ledger.CanonicalFailure(&ledger.Diagnostic{}, failuretype.RuntimeShutdownUnknown)
		}
	}()
	if runtime != nil {
		runtime.Shutdown()
	}
	return nil
}
