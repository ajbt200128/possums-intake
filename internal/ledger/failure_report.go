package ledger

import (
	"context"
	"sync"

	"possums-intake/internal/failuretype"
)

// FailureReport is transient numeric evidence for one run invocation. The outer
// owner must discard it after Take and must not recount the returned diagnostic.
// It contains no causes, identifiers, timestamps or exporter callbacks.
// Do not copy a report, reuse its context for another run, or consume it early.
// Validation before run is outside this seam and remains the outer owner's job.
type FailureReport struct {
	mu        sync.Mutex
	batch     transactionFailures
	published bool
	consumed  bool
}

type failureReportKey struct{}

// WithFailureReport scopes a report to one transaction call. It does not enable
// collection/export and does not change any accounting API result.
func WithFailureReport(ctx context.Context) (context.Context, *FailureReport) {
	report := &FailureReport{}
	return context.WithValue(ctx, failureReportKey{}, report), report
}

// Take consumes the fixed report once. false means unavailable, never healthy
// zeros: invalid/overflowed evidence, premature consumption, or reuse. Only the
// first count entries of the array are populated. An invalid report yields no
// partial observations.
func (r *FailureReport) Take() ([3]failuretype.Type, int, bool) {
	if r == nil {
		return [3]failuretype.Type{}, 0, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.consumed || !r.published || r.batch.invalid {
		r.consumed = true
		r.batch = transactionFailures{invalid: true}
		return [3]failuretype.Type{}, 0, false
	}
	r.consumed = true
	batch := r.batch
	r.batch = transactionFailures{}
	return batch.kinds, batch.count, true
}

func (r *FailureReport) publish(batch transactionFailures) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.published || r.consumed {
		r.batch = transactionFailures{invalid: true}
		return
	}
	r.batch = batch
	r.published = true
}

// A hostile context cannot turn this optional observation into accounting work
// or failure. Only our concrete pointer is accepted; no reporting interface is
// called. If Value hides/panics instead of exposing the report, its owner sees
// an unpublished (invalid) report on Take.
func reportFromContext(ctx context.Context) (report *FailureReport) {
	defer func() {
		if recover() != nil {
			report = nil
		}
	}()
	report, _ = ctx.Value(failureReportKey{}).(*FailureReport)
	return report
}

type transactionFailures struct {
	kinds   [3]failuretype.Type
	count   int
	invalid bool
}

func (b *transactionFailures) add(kind failuretype.Type) {
	if !kind.Valid() || b.count == len(b.kinds) {
		b.invalid = true
		return
	}
	b.kinds[b.count] = kind
	b.count++
}
