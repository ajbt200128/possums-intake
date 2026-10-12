package telemetry

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func configFromMap(values map[string]string) (config, State) {
	entries := make([]string, 0, len(values))
	for k, v := range values {
		entries = append(entries, k+"="+v)
	}
	return loadConfig(entries, func(k string) string { return values[k] })
}
func validEnv() map[string]string {
	return map[string]string{"INTAKE_METRICS_ENDPOINT": honeycombEndpoint, "INTAKE_METRICS_ENVIRONMENT": "development", "INTAKE_METRICS_SLOT": "intake-01", "INTAKE_METRICS_HONEYCOMB_API_KEY": "synthetic_ingestion_key"}
}
func TestConfigClosedEnablementAndOffOverride(t *testing.T) {
	if _, s := configFromMap(nil); s != Disabled {
		t.Fatal("missing config enabled")
	}
	if _, s := configFromMap(validEnv()); s != Enabled {
		t.Fatal("valid allowlisted config disabled")
	}
	for _, pair := range [][2]string{
		{"INTAKE_METRICS_ENDPOINT", "http://api.honeycomb.io/v1/metrics"},
		{"INTAKE_METRICS_ENDPOINT", "https://api.honeycomb.io:443/v1/metrics"},
		{"INTAKE_METRICS_ENDPOINT", "https://api.eu1.honeycomb.io/v1/metrics"},
		{"INTAKE_METRICS_ENDPOINT", "http://localhost:4318/v1/metrics"},
		{"INTAKE_METRICS_ENVIRONMENT", "test"}, {"INTAKE_METRICS_SLOT", sentinel},
		{"INTAKE_METRICS_SLOT", "intake-001"}, {"INTAKE_METRICS_HONEYCOMB_API_KEY", ""},
		{"INTAKE_METRICS_HONEYCOMB_API_KEY", "synthetic\r\nheader"},
		{"OTEL_SDK_DISABLED", "garbage"},
		{"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", sentinel}, {"OTEL_EXPORTER_OTLP_HEADERS", sentinel},
		{"OTEL_EXPORTER_OTLP_CERTIFICATE", sentinel}, {"OTEL_RESOURCE_ATTRIBUTES", sentinel},
		{"OTEL_SERVICE_NAME", sentinel}, {"OTEL_METRICS_EXEMPLAR_FILTER", sentinel}, {"OTEL_GO_X_SELF_OBSERVABILITY", sentinel},
	} {
		e := validEnv()
		e[pair[0]] = pair[1]
		if c, s := configFromMap(e); s != InvalidConfig || c != (config{}) {
			t.Fatal("invalid/ambient config survived")
		}
		e["OTEL_SDK_DISABLED"] = "true"
		if _, s := configFromMap(e); s != Disabled {
			t.Fatal("off switch did not override invalid config")
		}
	}
	e := validEnv()
	e["OTEL_SDK_DISABLED"] = "false"
	if _, s := configFromMap(e); s != Enabled {
		t.Fatal("false off switch not accepted")
	}
}

// Capture only synthetic test output, never print it on failure.
func assertQuiet(t *testing.T, f func()) {
	t.Helper()
	outRead, outWrite, err := os.Pipe()
	if err != nil {
		t.Fatal("capture unavailable")
	}
	errRead, errWrite, err := os.Pipe()
	if err != nil {
		t.Fatal("capture unavailable")
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outWrite, errWrite
	outResult := make(chan []byte, 1)
	errResult := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(outRead); outResult <- b }()
	go func() { b, _ := io.ReadAll(errRead); errResult <- b }()
	defer func() {
		os.Stdout, os.Stderr = stdout, stderr
		outWrite.Close()
		errWrite.Close()
		a, b := <-outResult, <-errResult
		outRead.Close()
		errRead.Close()
		if len(a) != 0 || len(b) != 0 {
			t.Error("telemetry emitted unexpected output (content withheld)")
		}
	}()
	f()
}
func TestDisabledRuntimeNoSDKSourceOrWork(t *testing.T) {
	assertQuiet(t, func() {
		for _, state := range []State{Disabled, InvalidConfig} {
			r := newRuntime(context.Background(), config{}, state, func(context.Context, config) (sdkmetric.Exporter, func(), error) {
				t.Error("disabled exporter constructed")
				return nil, nil, nil
			}, func(context.Context) (float64, uint64, bool) { t.Error("disabled process sampled"); return 0, 0, false }, time.Now, true)
			r.ObserveTerminal(Status2xx, time.Millisecond)
			r.InvalidateRequests()
			r.Shutdown()
			r.Shutdown()
			if r.queue != nil || r.State() != state {
				t.Error("disabled runtime allocated sender state")
			}
		}
		t.Setenv("OTEL_SDK_DISABLED", "true")
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", sentinel)
		r := New(context.Background())
		if r.State() != Disabled {
			t.Error("real constructor off override failed")
		}
		r.Shutdown()
	})
}

type fakeExporter struct {
	calls    atomic.Int32
	started  chan struct{}
	finished chan struct{}
	block    bool
	shutdown atomic.Bool
}

func (*fakeExporter) Temporality(k sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(k)
}
func (*fakeExporter) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(k)
}
func (e *fakeExporter) Export(ctx context.Context, _ *metricdata.ResourceMetrics) error {
	e.calls.Add(1)
	if e.started != nil {
		select {
		case e.started <- struct{}{}:
		default:
		}
	}
	if e.block {
		<-ctx.Done()
	}
	if e.finished != nil {
		e.finished <- struct{}{}
	}
	return errors.New(sentinel)
}
func (*fakeExporter) ForceFlush(context.Context) error { return errors.New(sentinel) }
func (e *fakeExporter) Shutdown(context.Context) error {
	e.shutdown.Store(true)
	return errors.New(sentinel)
}
func fakeFactory(e *fakeExporter) exporterFactory {
	return func(context.Context, config) (sdkmetric.Exporter, func(), error) { return e, func() {}, nil }
}
func await(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("synthetic work did not reach expected stage")
	}
}
func TestConstructorHostileSDKErrorDisablesOnlyTelemetry(t *testing.T) {
	assertQuiet(t, func() {
		r := newRuntime(context.Background(), fixtureConfig, Enabled, func(context.Context, config) (sdkmetric.Exporter, func(), error) {
			return nil, nil, errors.New(sentinel)
		}, currentProcess, time.Now, true)
		if r.State() != SDKUnavailable {
			t.Error("SDK failure category lost")
		}
		r.ObserveTerminal(Status2xx, 0)
		r.Shutdown()
	})
}
func TestBoundedOutageNoReplayJoinedShutdownAndRaces(t *testing.T) {
	assertQuiet(t, func() {
		e := &fakeExporter{started: make(chan struct{}, 10), block: true}
		r := newRuntime(context.Background(), fixtureConfig, Enabled, fakeFactory(e), currentProcess, func() time.Time { return epoch }, false)
		r.enqueue(fixtureBatch())
		await(t, e.started)
		r.enqueue(fixtureBatch())
		for i := 0; i < 100; i++ {
			r.enqueue(fixtureBatch())
		}
		if len(r.queue) != 1 || cap(r.queue) != 1 {
			t.Error("pending batch bound violated")
		}
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					r.ObserveTerminal(Status2xx, time.Millisecond)
					r.InvalidateRequests()
					_ = r.State()
				}
			}()
		}
		wg.Add(1)
		go func() { defer wg.Done(); r.Shutdown() }()
		r.Shutdown()
		wg.Wait()
		if !e.shutdown.Load() || e.calls.Load() != 1 || len(r.queue) != 0 {
			t.Error("shutdown flushed/replayed or failed to join")
		}
		r.Shutdown()
	})
}
func TestTimeoutDropsBatchWithoutSDKDiagnostics(t *testing.T) {
	assertQuiet(t, func() {
		e := &fakeExporter{started: make(chan struct{}, 1), finished: make(chan struct{}, 1), block: true}
		r := newRuntime(context.Background(), fixtureConfig, Enabled, fakeFactory(e), currentProcess, func() time.Time { return epoch }, false)
		defer r.Shutdown()
		r.enqueue(fixtureBatch())
		await(t, e.started)
		// Sender must finish the context-limited attempt without an error callback.
		await(t, e.finished)
		r.Shutdown()
		if e.calls.Load() != 1 {
			t.Error("automatic outage replay")
		}
	})
}
func TestRuntimeFakeClockTerminalAndSampleHooks(t *testing.T) {
	var mu sync.Mutex
	now := epoch
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	set := func(at time.Time) { mu.Lock(); now = at; mu.Unlock() }
	e := &fakeExporter{started: make(chan struct{}, 100)}
	r := newRuntime(context.Background(), fixtureConfig, Enabled, fakeFactory(e), func(context.Context) (float64, uint64, bool) { return clock().Sub(epoch).Seconds() / 2, 1000, true }, clock, false)
	defer r.Shutdown()
	r.ObserveTerminal(Status4xx, 50*time.Millisecond)
	for i := 1; i <= 300; i++ {
		set(epoch.Add(time.Duration(i) * time.Second))
		r.tick(context.Background())
		// Complete process windows at 120/180/240, and both families at 300.
		if i == 120 || i == 180 || i == 240 || i == 300 {
			await(t, e.started)
		}
	}
	if e.calls.Load() != 4 {
		t.Fatal("unexpected warmup or window release count")
	}
	set(epoch.Add(24 * time.Hour))
	r.tick(context.Background())
	if len(e.started) != 0 {
		t.Fatal("stalled runtime produced catch-up windows")
	}
}
func TestProcessSourceOnlyCurrentNumericMethods(t *testing.T) {
	t.Setenv("HOST_PROC", sentinel)
	t.Setenv("HOST_SYS", sentinel)
	assertQuiet(t, func() {
		cpu, rss, ok := currentProcess(context.Background())
		if !ok || !finiteRange(cpu, 1e12) || rss == 0 {
			t.Error("current process numeric source unavailable on qualification host")
		}
	})
}
func TestPackageHasNoAccountingOrHTTPInstrumentationDependencies(t *testing.T) {
	// Import-level structural boundary: telemetry cannot initiate money work.
	files := []string{"config.go", "windows.go", "sdk.go", "runtime.go", "transport.go", "process.go"}
	for _, name := range files {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal("source boundary unavailable")
		}
		for _, forbidden := range []string{"possums-intake/internal/ledger", "possums-intake/internal/httpapi", "otelhttp", "otelsql", "SetMeterProvider", "SetErrorHandler"} {
			if strings.Contains(string(b), forbidden) {
				t.Fatal("telemetry boundary expanded")
			}
		}
	}
}
