package httpapi

import (
	"time"

	"possums-intake/internal/telemetry"
)

func statusClass(status int) telemetry.StatusClass {
	switch {
	case status >= 200 && status < 300:
		return telemetry.Status2xx
	case status >= 400 && status < 500:
		return telemetry.Status4xx
	case status >= 500 && status < 600:
		return telemetry.Status5xx
	default:
		return telemetry.Unknown
	}
}

// This guard is request-local. No identity, context or request data is retained.
func terminalCompletion(observer TerminalObserver, started time.Time) func(telemetry.StatusClass) {
	observed := false
	return func(class telemetry.StatusClass) {
		if observer == nil {
			return
		}
		defer func() {
			if recover() != nil {
				invalidateRequests(observer)
			}
		}()
		if observed {
			invalidateRequests(observer)
			return
		}
		observed = true
		duration := time.Since(started)
		if duration < 0 {
			invalidateRequests(observer)
			return // no fabricated latency when timing evidence is invalid
		}
		observer.ObserveTerminal(class, duration)
	}
}

func invalidateRequests(observer TerminalObserver) {
	if observer == nil {
		return
	}
	defer func() { _ = recover() }() // telemetry cannot alter an HTTP/accounting result
	observer.InvalidateRequests()
}
