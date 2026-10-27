package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"possums-intake/internal/stripepay"
)

// Exercise the real SDK timeout and net/http final flush, not a recorder's
// successful buffered Write. All provider traffic stays in this loopback fixture.
func TestLocalServerDeliversAfterProviderTimeout(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	provider.Config.ErrorLog = log.New(io.Discard, "", 0)
	provider.Start()
	t.Cleanup(provider.Close)
	adapter, err := stripepay.NewOffline(stripepay.OfflineConfig{
		Endpoint: provider.URL, ReturnOrigin: provider.URL,
		APIKey: stripepay.FixtureAPIKey, WebhookSecret: stripepay.FixtureWebhookSecret,
	})
	if err != nil {
		t.Fatal("SERVER_BUDGET_OFFLINE_ADAPTER_UNAVAILABLE")
	}
	const body = "SYNTHETIC_PROVIDER_TIMEOUT outcome=creation_unknown_not_replayed\n"
	typed := make(chan bool, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		// Real authentication/preparation precedes the adapter's own deadline.
		time.Sleep(100 * time.Millisecond)
		now := time.Now().UTC()
		_, err := adapter.CreateCheckout(r.Context(), stripepay.CreateRequest{
			Purchase: stripepay.Purchase{Reference: strings.Repeat("a", 64), GrossCents: 500,
				IssuedAt: now, ExpiresAt: now.Add(time.Hour).Truncate(time.Second)},
			IdempotencyKey: strings.Repeat("b", 64),
		})
		var failure *stripepay.Error
		known := errors.As(err, &failure) && failure.Code == stripepay.CodeProvider &&
			failure.Stage == stripepay.StageCreate && failure.Constraint == stripepay.ConstraintTransport &&
			failure.Outcome == stripepay.OutcomeCreateUnknown
		typed <- known
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if !known {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, body)
	})
	ctx, cancel := context.WithCancel(context.Background())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cancel()
		t.Fatal("SERVER_BUDGET_LOOPBACK_UNAVAILABLE")
	}
	server := localServer(ctx, handler)
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
		if err := <-served; !errors.Is(err, http.ErrServerClosed) {
			t.Error("SERVER_BUDGET_JOIN_UNCONFIRMED")
		}
	})
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 12 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Post("http://"+listener.Addr().String(), "text/plain", strings.NewReader("synthetic"))
	if err != nil {
		t.Fatal("SERVER_BUDGET_TYPED_RESPONSE_NOT_DELIVERED")
	}
	defer response.Body.Close()
	got, err := io.ReadAll(io.LimitReader(response.Body, 1024))
	if err != nil || response.StatusCode != http.StatusServiceUnavailable || string(got) != body ||
		response.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatal("SERVER_BUDGET_TYPED_RESPONSE_CHANGED")
	}
	if !<-typed || calls.Load() != 1 {
		t.Fatal("SERVER_BUDGET_PROVIDER_TIMEOUT_OR_ONCE_ONLY_CHANGED")
	}
}
