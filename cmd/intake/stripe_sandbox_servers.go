package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"

	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

// Private to the disposable sandbox mode. Shutdown closes admission on BOTH
// supported servers, drains their handlers without a deadline and joins both
// Serve workers. An error/panic is unconfirmed drain, never completion evidence.
type sandboxLocalServers struct {
	servers  [2]httpLifecycle
	webhook  net.Listener
	observer failureowner.Observer
	mu       sync.Mutex
	started  bool
	stopping bool
	selected chan struct{}
	done     [2]chan struct{}
	errors   [2]error
	first    int
}

func newSandboxLocalServers(website, webhook httpLifecycle, listener net.Listener, observer failureowner.Observer) *sandboxLocalServers {
	return &sandboxLocalServers{servers: [2]httpLifecycle{website, webhook}, webhook: listener, observer: observer, selected: make(chan struct{}), done: [2]chan struct{}{make(chan struct{}), make(chan struct{})}}
}

func (s *sandboxLocalServers) Serve(website net.Listener) error {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return http.ErrServerClosed
	}
	s.started = true
	s.mu.Unlock()
	completed := make(chan int, 2)
	for i, listener := range [2]net.Listener{website, s.webhook} {
		go func(index int, bound net.Listener) {
			s.errors[index] = serveHTTP(s.servers[index], bound)
			close(s.done[index])
			completed <- index
		}(i, listener)
	}
	first := <-completed
	s.first = first
	close(s.selected)
	return s.errors[first]
}

func (s *sandboxLocalServers) Shutdown(context.Context) error {
	s.mu.Lock()
	s.stopping = true
	started := s.started
	s.mu.Unlock()
	// Run both shutdowns even if either one fails or panics. shutdownHTTP is
	// the existing closed recovery boundary; no raw host errors survive it.
	var drains [2]error
	var joined sync.WaitGroup
	for i := range s.servers {
		index := i
		joined.Go(func() { drains[index] = shutdownHTTP(s.servers[index]) })
	}
	joined.Wait()
	if started {
		for _, done := range s.done {
			<-done
		}
		<-s.selected
		secondary := s.errors[1-s.first]
		if secondary != nil && !errors.Is(secondary, http.ErrServerClosed) {
			fault := ledger.CanonicalFailure(secondary, failuretype.RuntimeServeUnknown)
			// The coordinator observes a returned failure once. If drain already
			// has its own failure, this independent serve fault is owned here.
			if drains[0] == nil && drains[1] == nil {
				return fault
			}
			failureowner.Observe(s.observer, fault.Kind())
		}
	}
	if drains[0] != nil {
		if drains[1] != nil {
			failureowner.Observe(s.observer, ledger.FailureKind(drains[1], failuretype.RuntimeShutdownUnknown))
		}
		return drains[0]
	}
	return drains[1]
}
