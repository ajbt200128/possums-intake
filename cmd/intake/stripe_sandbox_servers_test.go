package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

func sandboxTestListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("SANDBOX_TEST_LISTENER")
	}
	t.Cleanup(func() { listener.Close() })
	return listener
}
func sandboxAwait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("SANDBOX_TEST_JOIN_TIMEOUT")
	}
}
func sandboxNotDone(t *testing.T, ch <-chan error) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("SANDBOX_TEST_PREMATURE_COMPLETION")
	default:
	}
}

func TestStripeSandboxLocalPairShutdownBeforeServe(t *testing.T) {
	ctx := context.Background()
	web, webhook := sandboxTestListener(t), sandboxTestListener(t)
	pair := newSandboxLocalServers(localServer(ctx, http.NotFoundHandler()), localServer(ctx, http.NotFoundHandler()), webhook, nil)
	if pair.Shutdown(ctx) != nil || !errors.Is(pair.Serve(web), http.ErrServerClosed) {
		t.Fatal("SANDBOX_TEST_CANCEL_BEFORE_SERVE")
	}
}

func TestStripeSandboxLocalPairDrainsBothActualHandlers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	work, stopWork := context.WithCancel(ctx)
	defer stopWork()
	var entered, release, draining [2]chan struct{}
	var servers [2]*http.Server
	listeners := [2]net.Listener{sandboxTestListener(t), sandboxTestListener(t)}
	for i := range servers {
		entered[i], release[i], draining[i] = make(chan struct{}), make(chan struct{}), make(chan struct{})
		servers[i] = localServer(work, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(entered[i])
			<-release[i] // Cancellation is not handler completion.
			w.WriteHeader(http.StatusNoContent)
		}))
		servers[i].RegisterOnShutdown(func() { close(draining[i]) })
	}
	pair := newSandboxLocalServers(servers[0], servers[1], listeners[1], nil)
	done := make(chan error, 1)
	go func() {
		done <- coordinateServeObserved(ctx, work, stopWork, &nonfatalCleanup{ready: true}, pair, listeners[0], nil, time.Now(), nil)
	}()
	responses := make(chan error, 2)
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	for i := range servers {
		go func() {
			r, err := client.Get("http://" + listeners[i].Addr().String())
			if r != nil {
				io.Copy(io.Discard, r.Body)
				r.Body.Close()
			}
			responses <- err
		}()
	}
	for _, ch := range entered {
		sandboxAwait(t, ch)
	}
	cancel()
	for _, ch := range draining {
		sandboxAwait(t, ch)
	}
	sandboxNotDone(t, done)
	close(release[0])
	sandboxNotDone(t, done)
	close(release[1])
	if <-done != nil {
		t.Fatal("SANDBOX_TEST_ACTUAL_DRAIN")
	}
	for range servers {
		if <-responses != nil {
			t.Fatal("SANDBOX_TEST_HANDLER_DELIVERY")
		}
	}
}

type sandboxPanicServe struct{ *coordinatedHTTP }

func (s sandboxPanicServe) Serve(listener net.Listener) error {
	_ = s.coordinatedHTTP.Serve(listener)
	panic("SYNTHETIC_PRIVATE_SENTINEL")
}

func newSandboxCoordinatedHTTP() *coordinatedHTTP {
	return &coordinatedHTTP{serveEntered: make(chan struct{}), serveRelease: make(chan struct{}), serveJoined: make(chan struct{}), drainEntered: make(chan struct{}), drainRelease: make(chan struct{}), drainJoined: make(chan struct{})}
}

func TestStripeSandboxLocalPairFaultsJoinServersAndMaintenance(t *testing.T) {
	for _, trigger := range []string{"cancel", "serve_error", "serve_panic"} {
		for _, drain := range []string{"normal", "error", "panic", "combined"} {
			if trigger == "cancel" && drain == "combined" {
				continue
			}
			t.Run(trigger+"/"+drain, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				work, stopWork := context.WithCancel(ctx)
				defer stopWork()
				first, second := newSandboxCoordinatedHTTP(), newSandboxCoordinatedHTTP()
				var website httpLifecycle = first
				if trigger == "serve_error" {
					first.serveErr = errors.New("SYNTHETIC_PRIVATE_SENTINEL")
				}
				if trigger == "serve_panic" {
					website = sandboxPanicServe{first}
				}
				if drain == "error" {
					second.drainErr = errors.New("SYNTHETIC_PRIVATE_SENTINEL")
				}
				if drain == "panic" {
					second.panicDrain = true
				}
				if drain == "combined" {
					first.drainErr = errors.New("SYNTHETIC_PRIVATE_SENTINEL")
					second.panicDrain = true
					second.serveErr = errors.New("SYNTHETIC_PRIVATE_SENTINEL")
				}
				web, webhook := sandboxTestListener(t), sandboxTestListener(t)
				o := &runtimeObserver{}
				pair := newSandboxLocalServers(website, second, webhook, o)
				maintenance := coordinatedCleanup{entered: make(chan struct{}), release: make(chan struct{}), joined: make(chan struct{})}
				ticks := make(chan time.Time, 1)
				ticks <- time.Now()
				done := make(chan error, 1)
				go func() {
					done <- coordinateServeObserved(ctx, work, stopWork, maintenance, pair, web, ticks, time.Now(), o)
				}()
				for _, ch := range []chan struct{}{first.serveEntered, second.serveEntered, maintenance.entered} {
					sandboxAwait(t, ch)
				}
				if trigger == "cancel" {
					cancel()
				} else {
					close(first.serveRelease)
				}
				for _, ch := range []chan struct{}{first.drainEntered, second.drainEntered} {
					sandboxAwait(t, ch)
				}
				sandboxNotDone(t, done)
				close(first.drainRelease)
				sandboxAwait(t, first.drainJoined)
				sandboxNotDone(t, done)
				close(second.drainRelease)
				sandboxAwait(t, second.drainJoined)
				// Both Shutdown calls have returned; Serve and maintenance still
				// must join, even after shutdown panic/error.
				sandboxNotDone(t, done)
				if trigger == "cancel" {
					close(first.serveRelease)
				}
				close(second.serveRelease)
				for _, ch := range []chan struct{}{first.serveJoined, second.serveJoined} {
					sandboxAwait(t, ch)
				}
				sandboxNotDone(t, done)
				close(maintenance.release)
				err := <-done
				sandboxAwait(t, maintenance.joined)
				var want []failuretype.Type
				if trigger != "cancel" {
					want = append(want, failuretype.RuntimeServeUnknown)
				}
				if drain == "combined" {
					want = append(want, failuretype.RuntimeServeUnknown, failuretype.RuntimeShutdownUnknown)
				}
				if drain != "normal" {
					want = append(want, failuretype.RuntimeShutdownUnknown)
				}
				o.want(t, want...)
				if len(want) == 0 {
					if err != nil {
						t.Fatal("SANDBOX_TEST_NORMAL_CANCEL_RESULT")
					}
					return
				}
				if err == nil || httpDrainUnconfirmed(err) != (drain != "normal") {
					t.Fatal("SANDBOX_TEST_DRAIN_EVIDENCE")
				}
				if trigger != "cancel" && ledger.FailureKind(err, failuretype.RuntimeSetupUnknown) != failuretype.RuntimeServeUnknown {
					t.Fatal("SANDBOX_TEST_PRIMARY_LOST")
				}
				assertClosedRuntime(t, err)
			})
		}
	}
}

func TestStripeSandboxLocalPairCancellationStartRace(t *testing.T) {
	for range 20 {
		ctx, cancel := context.WithCancel(context.Background())
		work, stopWork := context.WithCancel(ctx)
		web, webhook := sandboxTestListener(t), sandboxTestListener(t)
		pair := newSandboxLocalServers(localServer(work, http.NotFoundHandler()), localServer(work, http.NotFoundHandler()), webhook, nil)
		done := make(chan error, 1)
		go func() {
			done <- coordinateServeObserved(ctx, work, stopWork, &nonfatalCleanup{ready: true}, pair, web, nil, time.Now(), nil)
		}()
		cancel()
		if <-done != nil {
			t.Fatal("SANDBOX_TEST_CANCEL_START_RACE")
		}
		stopWork()
	}
}
