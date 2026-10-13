package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"possums-intake/internal/ledger"
)

func TestLocalListenAndServerBudgets(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8090", "127.0.0.2:0", "[::1]:8090"} {
		if validateListen(address) != nil {
			t.Fatal("numeric loopback rejected")
		}
	}
	for _, address := range []string{":8090", "0.0.0.0:8090", "[::]:8090", "localhost:8090", "192.0.2.1:8090", "127.0.0.1:65536", "127.0.0.1:-1", "127.0.0.1:08090", "[::1%lo0]:8090", "SYNTHETIC_PRIVATE_SENTINEL"} {
		if d := ledger.Failure(validateListen(address)); d.Code != codeListen || d.Outcome != "no_effect" {
			t.Fatal("unsafe listener allowed")
		}
	}
	s := localServer(context.Background(), http.NotFoundHandler())
	if s.ReadHeaderTimeout != 2*time.Second || s.ReadTimeout != 5*time.Second || s.WriteTimeout != 5*time.Second || s.IdleTimeout != 30*time.Second || s.MaxHeaderBytes != 8192 {
		t.Fatal("HTTP budgets drifted")
	}
	if s.ErrorLog.Writer() != io.Discard {
		t.Fatal("raw HTTP logging enabled")
	}
}

type cleanupFixture struct {
	ready   atomic.Bool
	runs    chan bool
	release chan struct{}
}

func (f *cleanupFixture) Ready() bool { return f.ready.Load() }
func (f *cleanupFixture) Cleanup(ctx context.Context, prune bool) (ledger.CleanupResult, error) {
	f.runs <- prune
	if f.release != nil {
		<-f.release
	}
	return ledger.CleanupResult{}, nil
}

func TestMaintenanceCadenceAndCancellationJoinsActualWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := &cleanupFixture{runs: make(chan bool, 4), release: make(chan struct{})}
	f.ready.Store(true)
	ticks := make(chan time.Time, 4)
	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- maintain(ctx, f, ticks, started) }()
	ticks <- time.Now()
	if <-f.runs {
		t.Fatal("early prune")
	}
	cancel()
	select {
	case <-done:
		t.Fatal("maintenance abandoned active work")
	case <-time.After(20 * time.Millisecond):
	}
	close(f.release)
	if <-done != nil {
		t.Fatal("cancellation manufactured failure")
	}
	if len(f.runs) != 0 {
		t.Fatal("shutdown initiated extra accounting work")
	}

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	f = &cleanupFixture{runs: make(chan bool, 4)}
	f.ready.Store(true)
	ticks = make(chan time.Time)
	done = make(chan error, 1)
	started = time.Now()
	go func() { done <- maintain(ctx, f, ticks, started) }()
	now := started
	for _, tick := range []struct {
		offset time.Duration
		prune  bool
	}{{10 * time.Second, false}, {60 * time.Second, true}, {70 * time.Second, false}, {120 * time.Second, true}} {
		ticks <- now.Add(tick.offset)
		if got := <-f.runs; got != tick.prune {
			t.Fatal("10s cleanup/60s prune selection changed")
		}
	}
	cancel()
	if <-done != nil {
		t.Fatal("maintenance stop failed")
	}
}

type failingMaintenance struct {
	failure error
	cancel  context.CancelFunc
}

func (f failingMaintenance) Ready() bool { return false }
func (f failingMaintenance) Cleanup(context.Context, bool) (ledger.CleanupResult, error) {
	if f.cancel != nil {
		f.cancel()
	}
	return ledger.CleanupResult{}, f.failure
}
func TestFatalMaintenancePreservesKnownCategory(t *testing.T) {
	failure := &ledger.Diagnostic{Code: ledger.CodeCorrupt, Stage: "state_validation", Constraint: "canonical_consistent_accounting", Outcome: "no_effect", NextStep: "Preserve original state."}
	for _, cancelDuringFailure := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		fixture := failingMaintenance{failure: failure}
		if cancelDuringFailure {
			fixture.cancel = cancel
		}
		ticks := make(chan time.Time, 1)
		ticks <- time.Now()
		err := maintain(ctx, fixture, ticks, time.Now())
		cancel()
		if err == nil || ledger.Failure(err).Code != failure.Code || ledger.Failure(err).Kind() != failure.Kind() {
			t.Fatal("maintenance or cancellation erased observed failure")
		}
	}
}

func TestShutdownWaitsForActualHTTPHandlerCompletion(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("loopback fixture unavailable")
	}
	address := listener.Addr().String()
	listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	f := &cleanupFixture{runs: make(chan bool, 4)}
	f.ready.Store(true)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		<-release // models a transaction completing after request cancellation
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	done := make(chan error, 1)
	go func() { done <- serve(ctx, f, h, address) }()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		client := &http.Client{Timeout: 3 * time.Second}
		deadline := time.Now().Add(2 * time.Second)
		for {
			response, err := client.Get("http://" + address)
			if err == nil {
				response.Body.Close()
				return
			}
			if !time.Now().Before(deadline) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("HTTP handler did not start")
	}
	cancel()
	select {
	case err := <-done:
		close(release)
		t.Fatalf("HTTP handler abandoned: %t", err != nil)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatal("orderly shutdown failed")
	}
	<-requestDone
}
