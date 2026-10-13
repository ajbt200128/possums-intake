package ledger

import (
	"context"

	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
)

// ObserveRunFailures consumes one run report after the operation returned and
// the writer gate was released. A reported primary is not counted again; a
// post-run decision is distinct even if its category equals a hook fault.
// The return value identifies a valid reported primary, not category identity.
func ObserveRunFailures(observer failureowner.Observer, report *FailureReport, err error, fallback failuretype.Type) bool {
	return observeRunFailures(observer, report, err, fallback, false)
}

// Only writer admission proves that cancellation came from this caller's
// context. CodeWork may instead represent an independent hook cancellation;
// a cancelled ambient context alone is not evidence to suppress that fault.
func ObserveMaintenanceFailures(ctx context.Context, observer failureowner.Observer, report *FailureReport, err error) bool {
	d, kind := classifiedFailure(err, failuretype.MaintenanceUnknown)
	normal := ctx.Err() == context.Canceled && d != nil && d.Code == CodeWriter && kind == failuretype.Cancelled
	return observeRunFailures(observer, report, err, failuretype.MaintenanceUnknown, normal)
}

func observeRunFailures(observer failureowner.Observer, report *FailureReport, err error, fallback failuretype.Type, excludePrimary bool) bool {
	kind := FailureKind(err, fallback) // mandatory even without a collector
	primary := false
	if report.WasUsed() {
		kinds, n, valid, reportedPrimary := report.TakeWithPrimary()
		if !valid {
			failureowner.Invalidate(observer)
			return false
		}
		primary = reportedPrimary
		for i, reported := range kinds[:n] {
			// Run reports place the primary first. Never suppress secondary
			// completion faults, including another cancellation of the same type.
			if excludePrimary && primary && i == 0 && reported == failuretype.Cancelled {
				continue
			}
			failureowner.Observe(observer, reported)
		}
	}
	if err != nil && !primary {
		failureowner.Observe(observer, kind)
	}
	return primary
}

func lifecycleFailure(err error, fallback failuretype.Type) *Diagnostic {
	d := CanonicalFailure(err, fallback)
	d.Stage = failureStage(fallback)
	d.Outcome = "unavailable"
	d.NextStep = "Underlying cause unknown. Previously observed accounting outcomes are unchanged. Preserve original or partial state and inspect this component privately; never initialize as recovery."
	return d
}

// clearStorage also owns exceptions raised by supported teardown hooks. It
// neither retries teardown nor replaces an already known primary result.
func clearStorage(clear func() error, observer failureowner.Observer) (result error) {
	defer func() {
		if recover() != nil {
			result = diagnostic(CodeClose)
		}
		if result != nil {
			kinds, count, valid := closureKinds(result)
			d := diagnostic(CodeClose)
			if valid {
				d.kind = kinds[0]
				for _, kind := range kinds[:count] {
					failureowner.Observe(observer, kind)
				}
			} else {
				failureowner.Invalidate(observer)
			}
			result = d
		}
	}()
	return clear()
}

// PocketBase ClearBootstrap joins at most four DB-pool close errors. Inspect
// only that direct supported join, not a recursive exception graph/registry.
func closureKinds(err error) (kinds [4]failuretype.Type, count int, valid bool) {
	defer func() {
		if recover() != nil {
			kinds, count, valid = [4]failuretype.Type{}, 0, false
		}
	}()
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		causes := joined.Unwrap()
		if len(causes) == 0 || len(causes) > len(kinds) {
			return kinds, 0, false
		}
		for _, cause := range causes {
			if cause == nil {
				return [4]failuretype.Type{}, 0, false
			}
			kinds[count] = FailureKind(cause, failuretype.StorageCloseUnknown)
			count++
		}
	} else {
		kinds[0], count = FailureKind(err, failuretype.StorageCloseUnknown), 1
	}
	return kinds, count, kinds[0].Valid()
}
