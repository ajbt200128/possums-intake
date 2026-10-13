package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
	"possums-intake/internal/telemetry"
)

// FailureObserver is optional on the existing TerminalObserver dependency.
// It accepts no request data and does not enable the telemetry error family.
type FailureObserver interface {
	ObserveFailure(failuretype.Type)
	InvalidateFailures()
}

// Five numeric fault slots, including distinct post-run faults. Integrity loss
// withholds all observations rather than presenting a truncated healthy batch.
type failureBatch struct {
	kinds   [5]failuretype.Type
	count   int
	invalid bool
}

func (b *failureBatch) add(kind failuretype.Type) {
	if !kind.Valid() || b.count == len(b.kinds) {
		b.invalid = true
		return
	}
	b.kinds[b.count] = kind
	b.count++
}

func (b *responseBuffer) collectAccounting(report *ledger.FailureReport) {
	primaryInReport := false
	if report.WasUsed() {
		kinds, n, valid, primary := report.TakeWithPrimary()
		if !b.accountingInvoked || !valid {
			b.requestInterruption = 0
			b.failures.invalid = true
			return // no partial observations from unavailable evidence
		}
		primaryInReport = primary
		if !primary {
			b.requestInterruption = 0 // completion faults/post-run decisions are not a run primary
		}
		for _, kind := range kinds[:n] {
			b.failures.add(kind)
		}
	}
	// A successful run may return a later policy decision (expired settlement),
	// even when completion faults occupy report slots of the very same category.
	// Unused reports are legal for pre-run validation and fake Accounting.
	if !primaryInReport && b.primary.Valid() {
		b.failures.add(b.primary)
	}
}

func observeFailures(observer TerminalObserver, batch failureBatch) {
	failures, ok := observer.(FailureObserver)
	if !ok {
		return // legacy terminal-only collectors remain supported
	}
	defer func() {
		if recover() != nil {
			invalidateFailures(failures)
		}
	}()
	if batch.invalid {
		invalidateFailures(failures)
		return
	}
	for _, kind := range batch.kinds[:batch.count] {
		failures.ObserveFailure(kind)
	}
}

func invalidateFailures(observer FailureObserver) {
	defer func() { _ = recover() }()
	observer.InvalidateFailures()
}

// Only supported context evidence changes an HTTP I/O fallback category.
func ioFailureKind(err error, fallback failuretype.Type) failuretype.Type {
	kind := ledger.FailureKind(err, fallback)
	if kind == failuretype.Cancelled || kind == failuretype.DeadlineExceeded {
		return kind
	}
	return fallback
}

func safeErrorIs(err, target error) (matches bool) {
	defer func() { _ = recover() }()
	return errors.Is(err, target)
}

func expectedHTTPError(err error) bool {
	return safeErrorIs(err, http.ErrAbortHandler) || safeErrorIs(err, http.ErrServerClosed) || safeErrorIs(err, http.ErrNotSupported)
}

// Capture a typed primary candidate before discarding its cause; accounting
// candidates still require report-role validation. A matching request-context
// interruption proves the shared domain, unlike a category alone.
func matchingRequestInterruption(kind failuretype.Type, r *http.Request) failuretype.Type {
	if kind != failuretype.Cancelled && kind != failuretype.DeadlineExceeded {
		return 0
	}
	cause := r.Context().Err()
	if (kind == failuretype.Cancelled && cause == context.Canceled) ||
		(kind == failuretype.DeadlineExceeded && cause == context.DeadlineExceeded) {
		return kind
	}
	return 0
}

func (b *responseBuffer) deliveryFailure(kind failuretype.Type) {
	// Only proven primary interruption of this request context can account for
	// its consequent typed delivery interruption. Equal labels are not identity.
	if (kind == failuretype.Cancelled || kind == failuretype.DeadlineExceeded) && kind == b.requestInterruption {
		return
	}
	b.failures.add(kind)
}

func deliveryKind(err error) failuretype.Type {
	// Ambient request cancellation does not establish the cause of this I/O
	// error or panic. Only supported evidence from the I/O cause can do that.
	return ioFailureKind(err, failuretype.ResponseDeliveryUnknown)
}

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
