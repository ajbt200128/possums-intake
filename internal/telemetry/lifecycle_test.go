package telemetry

import (
	"context"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

func TestActualConstructorRejectsAmbientSDKConfigSilently(t *testing.T) {
	for key, value := range validEnv() {
		t.Setenv(key, value)
	}
	t.Setenv("OTEL_SDK_DISABLED", "false")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "private="+sentinel)
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "private="+sentinel)
	assertQuiet(t, func() {
		r := New(context.Background())
		defer r.Shutdown()
		if r.State() != InvalidConfig || r.queue != nil {
			t.Error("ambient config reached real SDK constructor")
		}
	})
}
func TestParentCancellationDropsPartialAndDisablesHooks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	e := &fakeExporter{started: make(chan struct{}, 1)}
	r := newRuntime(ctx, fixtureConfig, Enabled, fakeFactory(e), currentProcess, func() time.Time { return epoch.Add(time.Second) }, false)
	r.ObserveTerminal(Status2xx, time.Millisecond)
	cancel()
	r.wg.Wait()
	r.ObserveTerminal(Status5xx, 50*time.Millisecond)
	if len(r.queue) != 0 || e.calls.Load() != 0 || !e.shutdown.Load() {
		t.Fatal("cancelled runtime retained partial work or accepted new observations")
	}
	r.Shutdown()
}
func TestObservedDuplicateOrLostTerminalInvalidatesWholeWindow(t *testing.T) {
	for _, name := range []string{"duplicate", "lost"} {
		t.Run(name, func(t *testing.T) {
			now := epoch
			e := &fakeExporter{}
			r := newRuntime(context.Background(), fixtureConfig, Enabled, fakeFactory(e), func(context.Context) (float64, uint64, bool) { return 0, 0, false }, func() time.Time { return now }, false)
			defer r.Shutdown()
			r.ObserveTerminal(Status2xx, 0)
			if name == "duplicate" {
				r.ObserveTerminal(Status2xx, 0)
			}
			r.InvalidateRequests()
			for i := 1; i <= 300; i++ {
				now = epoch.Add(time.Duration(i) * time.Second)
				r.tick(context.Background())
			}
			r.Shutdown()
			if e.calls.Load() != 0 {
				t.Fatal("incomplete lifecycle family exported")
			}
		})
	}
}
func TestTerminalObservationRetainsNoIndividualTimestamp(t *testing.T) {
	now := epoch
	r := newRuntime(context.Background(), fixtureConfig, Enabled, fakeFactory(&fakeExporter{}), currentProcess, func() time.Time { return now }, false)
	defer r.Shutdown()
	now = epoch.Add(111 * time.Millisecond)
	r.ObserveTerminal(Status2xx, 123*time.Millisecond)
	if !r.requests.last.Equal(epoch) {
		t.Fatal("individual timestamp retained instead of heartbeat")
	}
}
func TestFailedSampleDoesNotSuppressCompleteRequestWindow(t *testing.T) {
	now := epoch
	e := &fakeExporter{started: make(chan struct{}, 1)}
	r := newRuntime(context.Background(), fixtureConfig, Enabled, fakeFactory(e), func(context.Context) (float64, uint64, bool) { return 0, 0, false }, func() time.Time { return now }, false)
	defer r.Shutdown()
	r.ObserveTerminal(Unknown, 0)
	for i := 1; i <= 300; i++ {
		now = epoch.Add(time.Duration(i) * time.Second)
		r.tick(context.Background())
	}
	await(t, e.started)
	r.Shutdown()
	if e.calls.Load() != 1 {
		t.Fatal("independent family release failed")
	}
}
func TestSDKConstructorRetainsNoIngestionCredential(t *testing.T) {
	e, err := newSDK(fixtureConfig)
	if err != nil {
		t.Fatal("SDK unavailable")
	}
	defer e.close()
	if e.config.key != "" {
		t.Fatal("credential retained in metric collection configuration")
	}
}
func TestFactoryFailureCleansPartialExporterWithoutFlush(t *testing.T) {
	e := &fakeExporter{}
	cleaned := false
	r := newRuntime(context.Background(), fixtureConfig, Enabled, func(context.Context, config) (sdkmetric.Exporter, func(), error) {
		return e, func() { cleaned = true }, context.Canceled
	}, currentProcess, time.Now, true)
	if r.State() != SDKUnavailable || !e.shutdown.Load() || !cleaned {
		t.Fatal("partial constructor resources not released")
	}
	r.Shutdown()
}
