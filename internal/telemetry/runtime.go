package telemetry

import (
	"context"
	"sync"
	"time"

	"possums-intake/internal/failuretype"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

type exporterFactory func(context.Context, config) (sdkmetric.Exporter, func(), error)

// Runtime owns its independent heartbeat/process sampler and bounded sender.
// It registers no global OTel provider, explicit resource detectors, logger or
// flush API. SDK-internal default-resource evaluation remains an upstream boundary.
type Runtime struct {
	mu         sync.Mutex
	state      State
	closed          bool
	failureCoverage bool
	requests        requestWindow
	failures   failureWindow
	process    processWindow
	nextSample time.Time
	source     processSource
	now        func() time.Time
	queue      chan batch
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

// New reads the closed environment schema once. Missing/invalid configuration
// disables only telemetry. This constructor performs no export; a configured
// runtime can later export complete windows, so real config requires approval.
func New(ctx context.Context) *Runtime {
	c, state := runtimeConfig()
	// Boundary owners are not integrated yet. Do not publish healthy zeros for
	// an unobserved population; enable this only after coverage qualification.
	return newRuntimeWithFailureCoverage(ctx, c, state, productionExporter, currentProcess, time.Now, true, false)
}
func newRuntime(parent context.Context, c config, state State, factory exporterFactory, source processSource, now func() time.Time, runClock bool) *Runtime {
	return newRuntimeWithFailureCoverage(parent, c, state, factory, source, now, runClock, true)
}
func newRuntimeWithFailureCoverage(parent context.Context, c config, state State, factory exporterFactory, source processSource, now func() time.Time, runClock, failureCoverage bool) *Runtime {
	r := &Runtime{state: state, source: source, now: now, failureCoverage: failureCoverage}
	if state != Enabled {
		return r
	}
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	exporter, cleanup, err := factory(ctx, c)
	if err != nil {
		if exporter != nil {
			shutdownExporter(exporter)
		}
		if cleanup != nil {
			cleanup()
		}
		cancel()
		r.state = SDKUnavailable
		return r
	}
	engine, err := newSDK(c)
	if err != nil {
		shutdownExporter(exporter)
		if cleanup != nil {
			cleanup()
		}
		cancel()
		r.state = SDKUnavailable
		return r
	}
	start := now()
	r.requests = newRequestWindow(start)
	if failureCoverage {
		r.failures = newFailureWindow(start)
	}
	r.process = newProcessWindow(start)
	r.nextSample = start.Truncate(samplePeriod).Add(samplePeriod)
	r.queue = make(chan batch, 1)
	r.wg.Add(1)
	go r.send(ctx, engine, exporter, cleanup)
	if runClock {
		r.wg.Add(1)
		go r.clock(ctx)
	}
	return r
}
func (r *Runtime) State() State { r.mu.Lock(); defer r.mu.Unlock(); return r.state }

// ObserveTerminal must be called exactly once after a custom accounting route's
// final outcome is classified. It never receives an HTTP request or context.
// Invalid enums/durations invalidate the whole request window. Unknown delivery
// must use Unknown, not an assumed 2xx. Call outside any accounting transaction.
func (r *Runtime) ObserveTerminal(class StatusClass, duration time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != Enabled || r.closed {
		return
	}
	start, heartbeat := r.requests.start, r.requests.last
	release := r.requests.advance(r.now())
	// Keep only the independent heartbeat (or new window boundary), never an
	// individual terminal timestamp. The transient duration becomes one bin.
	if r.requests.start.Equal(start) {
		r.requests.last = heartbeat
	} else {
		r.requests.last = r.requests.start
	}
	r.requests.observe(class, duration)
	if release != nil {
		r.enqueue(batch{request: release})
	}
}

// InvalidateRequests withholds the current window when integration observes a
// lost/duplicate terminal or another lifecycle-integrity failure. No identifier
// or error is accepted. Exactly-once middleware is separately integrated.
func (r *Runtime) InvalidateRequests() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == Enabled && !r.closed {
		r.requests.valid = false
	}
}

// ObserveFailure accepts only a closed numeric classification, outside accounting
// transactions. Boundary mapping and once-only observation ownership are separate.
// Invalid types or counts withhold only the failure family.
func (r *Runtime) ObserveFailure(kind failuretype.Type) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != Enabled || r.closed || !r.failureCoverage {
		return
	}
	start, heartbeat := r.failures.start, r.failures.last
	release := r.failures.advance(r.now())
	// Observations cannot certify quiet-window coverage or retain event times.
	if r.failures.start.Equal(start) {
		r.failures.last = heartbeat
	} else {
		r.failures.last = r.failures.start
	}
	r.failures.observe(kind)
	if release != nil {
		r.enqueue(batch{errors: release})
	}
}

// InvalidateFailures withholds the current error family on known integrity loss.
func (r *Runtime) InvalidateFailures() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == Enabled && !r.closed && r.failureCoverage {
		r.failures.valid = false
	}
}
func (r *Runtime) enqueue(b batch) {
	select {
	case r.queue <- b:
	default:
	} // no replay, spool or blocking request path
}
func (r *Runtime) clock(ctx context.Context) {
	defer r.wg.Done()
	for {
		now := r.now()
		next := now.Truncate(time.Second).Add(time.Second)
		timer := time.NewTimer(next.Sub(now))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			r.tick(ctx)
		}
	}
}
func (r *Runtime) tick(ctx context.Context) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	now := r.now()
	req := r.requests.advance(now)
	var failures *failureRelease
	if r.failureCoverage {
		failures = r.failures.advance(now)
	}
	due := r.nextSample
	sample := !now.Before(due)
	if sample {
		r.nextSample = now.Truncate(samplePeriod).Add(samplePeriod)
	}
	r.mu.Unlock()
	var proc *processRelease
	if sample {
		sampleCtx, cancel := context.WithTimeout(ctx, timely)
		cpu, rss, valid := r.source(sampleCtx)
		cancel()
		r.mu.Lock()
		if !r.closed {
			proc = r.process.sample(due, r.now(), cpu, rss, valid)
		}
		r.mu.Unlock()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed && (req != nil || proc != nil || failures != nil) {
		r.enqueue(batch{request: req, process: proc, errors: failures})
	}
}
func (r *Runtime) send(ctx context.Context, engine *sdkEngine, exporter sdkmetric.Exporter, cleanup func()) {
	defer r.wg.Done()
	defer func() {
		r.mu.Lock()
		r.closed = true
		r.requests = requestWindow{}
		r.failures = failureWindow{}
		r.process = processWindow{}
		r.discard()
		r.mu.Unlock()
		shutdownExporter(exporter)
		engine.close()
		if cleanup != nil {
			cleanup()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-r.queue:
			if ctx.Err() != nil {
				return
			}
			attempt, cancel := context.WithTimeout(ctx, exportTimeout)
			data, valid := engine.collect(attempt, b)
			if valid && attempt.Err() == nil {
				_ = exporter.Export(attempt, &data)
			}
			// Raw SDK/transport errors are neither displayed nor retained. No periodic
			// reader routes them through OTel's global error handler.
			cancel()
		}
	}
}
func shutdownExporter(e sdkmetric.Exporter) {
	ctx, cancel := context.WithTimeout(context.Background(), exportTimeout)
	defer cancel()
	_ = e.Shutdown(ctx)
}
func (r *Runtime) discard() {
	for {
		select {
		case <-r.queue:
		default:
			return
		}
	}
}

// Shutdown cancels and JOINS owned work. It drops partial and pending windows,
// never flushes. No secret-bearing sender is detached after this returns.
// Standard DNS / OS numeric reads may outlive deadlines; this is not a hard
// whole-process shutdown bound. Concurrent/repeated Shutdown calls are safe.
func (r *Runtime) Shutdown() {
	r.mu.Lock()
	r.closed = true
	r.requests = requestWindow{}
	r.failures = failureWindow{}
	r.process = processWindow{}
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	r.wg.Wait()
}
