package telemetry

import (
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"possums-intake/internal/failuretype"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	collector "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

func closeFailures(w *failureWindow, end time.Time) *failureRelease {
	var released *failureRelease
	for at := w.last.Add(time.Second); !at.After(end); at = at.Add(time.Second) {
		if r := w.advance(at); r != nil {
			released = r
		}
	}
	if w.last.Before(end) {
		if r := w.advance(end); r != nil {
			released = r
		}
	}
	return released
}

func failureFixture() *failureRelease {
	return &failureRelease{start: epoch, end: epoch.Add(requestPeriod)}
}

func fullFailureBatch() batch {
	b := fixtureBatch()
	b.errors = failureFixture()
	for i := range b.errors.counts {
		b.errors.counts[i] = int64(i + 1)
	}
	return b
}

func TestFailureWindowsSparseZeroAndAllCategories(t *testing.T) {
	w := newFailureWindow(epoch)
	w.observe(failuretype.TransactionCompletionUnknown)
	r := closeFailures(&w, epoch.Add(requestPeriod))
	if r == nil || !r.consistent() || len(r.counts) != 39 || r.counts[21] != 1 {
		t.Fatal("complete sparse failure window missing")
	}
	for i, count := range r.counts {
		if i != 21 && count != 0 {
			t.Fatal("sparse window changed other categories")
		}
	}
	r = closeFailures(&w, epoch.Add(2*requestPeriod))
	if r == nil || r.counts != [failuretype.Count]int64{} {
		t.Fatal("complete quiet failure window missing")
	}
	for i := 1; i <= failuretype.Count; i++ {
		w.observe(failuretype.Type(i))
	}
	r = closeFailures(&w, epoch.Add(3*requestPeriod))
	if r == nil {
		t.Fatal("full category window missing")
	}
	for _, count := range r.counts {
		if count != 1 {
			t.Fatal("category not counted once")
		}
	}
}

func TestFailureWindowCadenceInvalidationAndWarmup(t *testing.T) {
	for _, name := range []string{"startup", "restart", "gap", "late", "backwards", "skipped", "unset", "invalid", "integrity"} {
		t.Run(name, func(t *testing.T) {
			w := newFailureWindow(epoch)
			switch name {
			case "startup", "restart":
				w = newFailureWindow(epoch.Add(time.Second))
			case "gap":
				w.advance(epoch.Add(2*time.Second + time.Nanosecond))
			case "backwards":
				w.advance(epoch.Add(time.Second))
				w.advance(epoch)
			case "unset":
				w.observe(0)
			case "invalid":
				w.observe(255)
			case "integrity":
				w.valid = false
			case "late":
				closeFailures(&w, epoch.Add(requestPeriod-time.Second))
				if w.advance(epoch.Add(requestPeriod+timely+time.Nanosecond)) != nil {
					t.Fatal("late failure window released")
				}
				if closeFailures(&w, epoch.Add(2*requestPeriod)) != nil || closeFailures(&w, epoch.Add(3*requestPeriod)) == nil {
					t.Fatal("late-window warmup incorrect")
				}
				return
			case "skipped":
				if w.advance(epoch.Add(24*time.Hour)) != nil || closeFailures(&w, epoch.Add(24*time.Hour+requestPeriod)) != nil || closeFailures(&w, epoch.Add(24*time.Hour+2*requestPeriod)) == nil {
					t.Fatal("skipped windows replayed or warmup incorrect")
				}
				return
			}
			w.observe(failuretype.MaintenanceUnknown)
			if closeFailures(&w, epoch.Add(requestPeriod)) != nil {
				t.Fatal("partial or invalid failure window released")
			}
			if closeFailures(&w, epoch.Add(2*requestPeriod)) == nil {
				t.Fatal("fresh full failure window unavailable")
			}
		})
	}
	// Boundary closure at exactly the tolerance is accepted; timestamps stay aligned.
	w := newFailureWindow(epoch)
	closeFailures(&w, epoch.Add(requestPeriod-time.Second))
	if r := w.advance(epoch.Add(requestPeriod + timely)); r == nil || !r.end.Equal(epoch.Add(requestPeriod)) {
		t.Fatal("timely closure withheld or shifted boundaries")
	}
}

func TestFailureCountCellSumAndSourceIntegrity(t *testing.T) {
	for _, name := range []string{"cell-overflow", "sum-overflow", "negative", "above-limit"} {
		t.Run(name, func(t *testing.T) {
			w := newFailureWindow(epoch)
			switch name {
			case "cell-overflow":
				w.counts[0] = maxExactInt
			case "sum-overflow":
				w.counts[0], w.counts[1] = maxExactInt-1, 1
			case "negative":
				w.counts[0] = -1
			case "above-limit":
				w.counts[0] = maxExactInt + 1
			}
			before := w.counts
			w.observe(failuretype.RequestInvalid)
			if w.valid || w.counts != before || closeFailures(&w, epoch.Add(requestPeriod)) != nil {
				t.Fatal("failure integrity violation released or wrapped")
			}
		})
	}
	r := failureFixture()
	r.counts[0], r.counts[1] = maxExactInt, 1
	if r.consistent() {
		t.Fatal("source sum overflow accepted")
	}
	r.counts[1] = 0
	if !r.consistent() {
		t.Fatal("exact maximum rejected")
	}
	r.start = r.start.Add(time.Nanosecond)
	if r.consistent() {
		t.Fatal("unaligned failure source accepted")
	}
}

func TestFailureSDKSelectionNoStaleGauges(t *testing.T) {
	e, err := newSDK(fixtureConfig)
	if err != nil {
		t.Fatal("SDK unavailable")
	}
	defer e.close()
	b := fullFailureBatch()
	for _, selected := range []batch{b, {errors: b.errors}, {request: b.request}, {process: b.process}, {request: b.request, errors: b.errors}, {errors: failureFixture()}, b} {
		raw, ok := e.collect(context.Background(), selected)
		if !ok {
			t.Fatal("stale or incomplete family in SDK collection")
		}
		want := 0
		if selected.request != nil {
			want += 2
		}
		if selected.process != nil {
			want += 2
		}
		if selected.errors != nil {
			want++
		}
		if len(raw.ScopeMetrics[0].Metrics) != want {
			t.Fatal("unselected gauge retained")
		}
		if selected.errors != nil {
			g := raw.ScopeMetrics[0].Metrics[metricIndex(raw, errorsName)].Data.(metricdata.Gauge[int64])
			if len(g.DataPoints) != 39 {
				t.Fatal("failure cells suppressed by cardinality limit")
			}
		}
	}
	if _, ok := e.collect(context.Background(), batch{}); ok {
		t.Fatal("empty batch manufactured gauges")
	}
}

func TestFailureSanitizerRejectsMalformedStreams(t *testing.T) {
	cases := map[string]func(*metricdata.Metrics){
		"unit": func(m *metricdata.Metrics) { m.Unit = "{request}" },
		"type": func(m *metricdata.Metrics) { m.Data = metricdata.Sum[int64]{} },
		"nan": func(m *metricdata.Metrics) {
			m.Data = metricdata.Gauge[float64]{DataPoints: []metricdata.DataPoint[float64]{{Value: math.NaN()}}}
		},
		"inf": func(m *metricdata.Metrics) {
			m.Data = metricdata.Gauge[float64]{DataPoints: []metricdata.DataPoint[float64]{{Value: math.Inf(1)}}}
		},
		"missing": func(m *metricdata.Metrics) {
			g := m.Data.(metricdata.Gauge[int64])
			g.DataPoints = g.DataPoints[1:]
			m.Data = g
		},
		"extra": func(m *metricdata.Metrics) {
			g := m.Data.(metricdata.Gauge[int64])
			g.DataPoints = append(g.DataPoints, g.DataPoints[0])
			m.Data = g
		},
		"duplicate": func(m *metricdata.Metrics) {
			g := m.Data.(metricdata.Gauge[int64])
			g.DataPoints[1] = g.DataPoints[0]
			m.Data = g
		},
	}
	pointCases := map[string]func(*metricdata.DataPoint[int64]){
		"unknown": func(p *metricdata.DataPoint[int64]) {
			p.Attributes = attribute.NewSet(attribute.String("error_type", sentinel))
		},
		"unset": func(p *metricdata.DataPoint[int64]) {
			p.Attributes = attribute.NewSet(attribute.String("error_type", ""))
		},
		"overflow-label": func(p *metricdata.DataPoint[int64]) {
			p.Attributes = attribute.NewSet(attribute.Bool("otel.metric.overflow", true))
		},
		"numeric-label": func(p *metricdata.DataPoint[int64]) { p.Attributes = attribute.NewSet(attribute.Int("error_type", 1)) },
		"extra-attr": func(p *metricdata.DataPoint[int64]) {
			p.Attributes = attribute.NewSet(attribute.String("error_type", failuretype.RequestInvalid.Label()), attribute.String("account_id", sentinel))
		},
		"no-attr": func(p *metricdata.DataPoint[int64]) { p.Attributes = attribute.NewSet() },
		"wrong-key": func(p *metricdata.DataPoint[int64]) {
			p.Attributes = attribute.NewSet(attribute.String("status_class", "unknown"))
		},
		"wrong-value": func(p *metricdata.DataPoint[int64]) { p.Value++ },
		"negative":    func(p *metricdata.DataPoint[int64]) { p.Value = -1 },
		"overflow":    func(p *metricdata.DataPoint[int64]) { p.Value = maxExactInt + 1 },
	}
	for name, mutate := range pointCases {
		cases[name] = func(m *metricdata.Metrics) {
			g := m.Data.(metricdata.Gauge[int64])
			mutate(&g.DataPoints[0])
			m.Data = g
		}
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			b := fullFailureBatch()
			raw := collectFixture(t, b)
			mutate(&raw.ScopeMetrics[0].Metrics[metricIndex(raw, errorsName)])
			if _, ok := sanitize(raw, b, fixtureConfig); ok {
				t.Fatal("malformed error stream accepted")
			}
		})
	}
	for _, name := range []string{"missing-family", "duplicate-family", "unselected-family", "source-sum", "source-cell", "source-start", "source-end", "resource", "scope"} {
		t.Run(name, func(t *testing.T) {
			b := fullFailureBatch()
			raw := collectFixture(t, b)
			i := metricIndex(raw, errorsName)
			switch name {
			case "missing-family":
				raw.ScopeMetrics[0].Metrics = append(raw.ScopeMetrics[0].Metrics[:i], raw.ScopeMetrics[0].Metrics[i+1:]...)
			case "duplicate-family":
				raw.ScopeMetrics[0].Metrics[0] = raw.ScopeMetrics[0].Metrics[i]
			case "unselected-family":
				b.errors = nil
			case "source-sum":
				b.errors.counts[0] = maxExactInt
			case "source-cell":
				b.errors.counts[0] = maxExactInt + 1
			case "source-start":
				b.errors.start = b.errors.start.Add(time.Second)
			case "source-end":
				b.errors.end = b.errors.end.Add(time.Second)
			case "resource":
				raw.Resource = resource.NewSchemaless(attribute.String("service.name", sentinel))
			case "scope":
				raw.ScopeMetrics[0].Scope.Name = sentinel
			}
			if _, ok := sanitize(raw, b, fixtureConfig); ok {
				t.Fatal("invalid failure batch accepted")
			}
		})
	}
}

func TestFailureMaximumSupportedTLSProtobufOneRequest(t *testing.T) {
	body := make(chan []byte, 2)
	var attempts atomic.Int32
	var headerOK atomic.Bool
	server := captureServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		headerOK.Store(r.Method == http.MethodPost && r.URL.Path == "/v1/metrics" && r.URL.RawQuery == "" && r.Header.Get("Content-Type") == "application/x-protobuf" && r.Header.Get("X-Honeycomb-Team") == fixtureConfig.key && r.Header.Get("Content-Encoding") == "")
		wire, _ := io.ReadAll(io.LimitReader(r.Body, 16*1024+1))
		body <- wire
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(200)
	}), true)
	defer server.Close()
	tr := server.Client().Transport.(*http.Transport).Clone()
	tr.DisableKeepAlives, tr.ForceAttemptHTTP2 = true, false
	tr.MaxConnsPerHost = 1
	e := captureExporter(t, server, tr)
	b := fullFailureBatch()
	// All cells are encoded, including zeros, with fixed-width maximum integer
	// values and the largest admitted means. Both families' checked sums fit.
	b.request.counts, b.request.bins = [4]int64{}, [4][9]int64{}
	remaining := maxExactInt
	for i := range b.request.bins {
		for j := range b.request.bins[i] {
			n := maxExactInt / 36
			if i == 3 && j == 8 {
				n = remaining
			}
			b.request.bins[i][j] = n
			b.request.counts[i] += n
			remaining -= n
		}
	}
	remaining = maxExactInt
	for i := range b.errors.counts {
		n := maxExactInt / 39
		if i == 38 {
			n = remaining
		}
		b.errors.counts[i] = n
		remaining -= n
	}
	b.process.cpu, b.process.rss = maxCPU, float64(maxExactInt)
	raw := collectFixture(t, b)
	raw.Resource = resource.NewWithAttributes(sentinel, append(fixedResource(fixtureConfig).Attributes(), attribute.String("private", sentinel))...)
	raw.ScopeMetrics[0].Scope.Attributes = attribute.NewSet(attribute.String("private", sentinel))
	raw.ScopeMetrics[0].Scope.SchemaURL = sentinel
	for i, m := range raw.ScopeMetrics[0].Metrics {
		raw.ScopeMetrics[0].Metrics[i].Description = sentinel
		if g, ok := m.Data.(metricdata.Gauge[int64]); ok {
			for j := range g.DataPoints {
				g.DataPoints[j].StartTime, g.DataPoints[j].Time = epoch.Add(time.Second), epoch.Add(2*time.Second)
				g.DataPoints[j].Exemplars = []metricdata.Exemplar[int64]{{FilteredAttributes: []attribute.KeyValue{attribute.String("private", sentinel)}, TraceID: []byte(sentinel), SpanID: []byte(sentinel)}}
			}
			raw.ScopeMetrics[0].Metrics[i].Data = g
		}
	}
	clean, ok := sanitize(raw, b, fixtureConfig)
	if !ok {
		t.Fatal("maximum closed batch rejected")
	}
	assertQuiet(t, func() {
		if e.Export(context.Background(), &clean) != nil {
			t.Error("maximum supported export failed")
		}
	})
	wire := capturedBody(t, body)
	if attempts.Load() != 1 || len(body) != 0 || !headerOK.Load() || len(wire) >= 16*1024 {
		t.Fatal("maximum batch split, replayed or exceeded unchanged body cap")
	}
	for _, forbidden := range []string{sentinel, fixtureConfig.key, "account_id", "reservation_id", "idempotency_key", "charged_amount", "trace_id", "span_id", "otel.metric.overflow"} {
		if bytes.Contains(wire, []byte(forbidden)) {
			t.Fatal("forbidden material in failure wire")
		}
	}
	var decoded collector.ExportMetricsServiceRequest
	if proto.Unmarshal(wire, &decoded) != nil || len(decoded.ResourceMetrics) != 1 || len(decoded.ResourceMetrics[0].ScopeMetrics) != 1 {
		t.Fatal("supported protobuf shape invalid")
	}
	scope := decoded.ResourceMetrics[0].ScopeMetrics[0]
	if len(scope.Metrics) != 5 {
		t.Fatal("five gauges missing")
	}
	points := 0
	var failures *metricpb.Metric
	var others []*metricpb.Metric
	for _, m := range scope.Metrics {
		if m.GetGauge() == nil {
			t.Fatal("nongauge in maximum batch")
		}
		points += len(m.GetGauge().DataPoints)
		if m.Name == errorsName {
			if failures != nil {
				t.Fatal("duplicate error gauge")
			}
			failures = m
		} else {
			others = append(others, m)
		}
	}
	if points != 81 || failures == nil || failures.Unit != "{error}" || failures.Description != "" || len(failures.Metadata) != 0 || len(failures.GetGauge().DataPoints) != 39 {
		t.Fatal("maximum point/error schema mismatch")
	}
	var seen [failuretype.Count]bool
	for _, p := range failures.GetGauge().DataPoints {
		attrs := attrsMap(t, p.Attributes)
		cell := -1
		for i := range seen {
			if attrs["error_type"] == failuretype.Type(i+1).Label() {
				cell = i
			}
		}
		if len(attrs) != 1 || cell < 0 || seen[cell] {
			t.Fatal("failure labels not exact distinct closed set")
		}
		seen[cell] = true
		if _, ok := p.Value.(*metricpb.NumberDataPoint_AsInt); !ok || p.GetAsInt() != b.errors.counts[cell] || p.StartTimeUnixNano != uint64(b.errors.start.UnixNano()) || p.TimeUnixNano != uint64(b.errors.end.UnixNano()) || len(p.Exemplars) != 0 || p.Flags != 0 {
			t.Fatal("failure value, boundary, exemplar or type mismatch")
		}
	}
	// Reuse the existing strict resource/scope and four-family wire checks.
	scope.Metrics = others
	assertDecoded(t, &decoded, b)
	t.Logf("supported TLS/protobuf maximum: 81 points, 5 gauges, %d bytes, 1 origin attempt", len(wire))
}

// Numeric-only test exporter captures selected families, never raw diagnostics.
type familyExporter struct {
	fakeExporter
	batches chan metricdata.ResourceMetrics
}

func (e *familyExporter) Export(ctx context.Context, data *metricdata.ResourceMetrics) error {
	e.batches <- *data
	return e.fakeExporter.Export(ctx, data)
}
func awaitFamilies(t *testing.T, e *familyExporter) metricdata.ResourceMetrics {
	t.Helper()
	select {
	case b := <-e.batches:
		return b
	case <-time.After(5 * time.Second):
		t.Fatal("family release absent")
		return metricdata.ResourceMetrics{}
	}
}

func TestFailureRuntimeIndependentFamiliesAndCombinedTick(t *testing.T) {
	for _, invalid := range []string{"none", "requests", "process", "failures", "invalid-type", "overflow", "integrity"} {
		t.Run(invalid, func(t *testing.T) {
			now := epoch
			e := &familyExporter{batches: make(chan metricdata.ResourceMetrics, 10)}
			r := newRuntime(context.Background(), fixtureConfig, Enabled, fakeFactoryAdapter(e), func(context.Context) (float64, uint64, bool) {
				return now.Sub(epoch).Seconds() / 2, 1000, invalid != "process"
			}, func() time.Time { return now }, false)
			defer r.Shutdown()
			r.ObserveTerminal(Status2xx, 0)
			r.ObserveFailure(failuretype.NativeAuthUnknown)
			switch invalid {
			case "requests":
				r.InvalidateRequests()
			case "failures":
				r.InvalidateFailures()
			case "invalid-type":
				r.ObserveFailure(0)
			case "overflow":
				r.failures.counts[0] = maxExactInt
				r.ObserveFailure(failuretype.RequestInvalid)
			case "integrity":
				r.failures.counts[0] = -1
			}
			for i := 1; i <= 300; i++ {
				now = epoch.Add(time.Duration(i) * time.Second)
				r.tick(context.Background())
				if invalid != "process" && (i == 120 || i == 180 || i == 240) {
					awaitFamilies(t, e)
				}
			}
			data := awaitFamilies(t, e)
			seen := map[string]bool{}
			for _, m := range data.ScopeMetrics[0].Metrics {
				seen[m.Name] = true
			}
			wantFailures := invalid == "none" || invalid == "requests" || invalid == "process"
			if seen[errorsName] != wantFailures || seen[requestsName] != (invalid != "requests") || seen[cpuName] != (invalid != "process") || seen[rssName] != (invalid != "process") {
				t.Fatal("invalid family suppressed other families or tick failed to combine")
			}
		})
	}
}

func fakeFactoryAdapter(e *familyExporter) exporterFactory {
	return func(context.Context, config) (sdkmetric.Exporter, func(), error) { return e, func() {}, nil }
}

func TestFailureRuntimeObservationsNeverCertifyHeartbeat(t *testing.T) {
	now := epoch
	r := newRuntime(context.Background(), fixtureConfig, Enabled, fakeFactory(&fakeExporter{}), currentProcess, func() time.Time { return now }, false)
	defer r.Shutdown()
	now = epoch.Add(111 * time.Millisecond)
	r.ObserveFailure(failuretype.RequestReadUnknown)
	if !r.failures.last.Equal(epoch) {
		t.Fatal("individual failure timestamp retained")
	}
	// Frequent observations alone cannot manufacture quiet-window coverage.
	for i := 1; i <= 300; i++ {
		now = epoch.Add(time.Duration(i) * time.Second)
		r.ObserveFailure(failuretype.RequestReadUnknown)
	}
	if len(r.queue) != 0 || r.failures.counts[31] != 1 || !r.failures.last.Equal(epoch.Add(requestPeriod)) {
		t.Fatal("observations certified coverage or retained nonboundary time")
	}
}

func TestFailureDisabledRuntimeHooksDoNoWork(t *testing.T) {
	assertQuiet(t, func() {
		for _, state := range []State{Disabled, InvalidConfig, SDKUnavailable} {
			r := newRuntime(context.Background(), config{}, state,
				func(context.Context, config) (sdkmetric.Exporter, func(), error) {
					t.Error("disabled failure runtime constructed SDK/exporter")
					return nil, nil, nil
				}, func(context.Context) (float64, uint64, bool) {
					t.Error("disabled failure runtime sampled process")
					return 0, 0, false
				}, func() time.Time {
					t.Error("disabled failure observation read clock")
					return epoch
				}, true)
			for i := 0; i <= 255; i++ {
				r.ObserveFailure(failuretype.Type(i))
			}
			r.InvalidateFailures()
			r.Shutdown()
			if r.queue != nil || r.failures != (failureWindow{}) || r.State() != state {
				t.Error("disabled failure hooks retained work")
			}
		}
	})
}

func TestFailureFailedExportQuietNonrecursiveAndNoReplay(t *testing.T) {
	for _, block := range []bool{false, true} {
		assertQuiet(t, func() {
			e := &fakeExporter{finished: make(chan struct{}, 1), block: block}
			r := newRuntime(context.Background(), fixtureConfig, Enabled, fakeFactory(e), currentProcess, func() time.Time { return epoch }, false)
			defer r.Shutdown()
			r.enqueue(fullFailureBatch())
			await(t, e.finished)
			r.Shutdown()
			if e.calls.Load() != 1 || !e.shutdown.Load() || r.failures != (failureWindow{}) || len(r.queue) != 0 {
				t.Error("failed error export recursed, retained state or replayed")
			}
		})
	}
}

func TestFailureRuntimeCancellationShutdownAndBoundedQuietOutage(t *testing.T) {
	assertQuiet(t, func() {
		ctx, cancel := context.WithCancel(context.Background())
		e := &fakeExporter{started: make(chan struct{}, 2), block: true}
		r := newRuntime(ctx, fixtureConfig, Enabled, fakeFactory(e), currentProcess, func() time.Time { return epoch }, false)
		r.ObserveFailure(failuretype.RuntimeServeUnknown)
		r.enqueue(fullFailureBatch())
		await(t, e.started)
		for i := 0; i < 100; i++ {
			r.enqueue(fullFailureBatch())
		}
		if len(r.queue) != 1 || cap(r.queue) != 1 {
			t.Error("error batches exceeded pending bound")
		}
		cancel()
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r.ObserveFailure(failuretype.MaintenanceUnknown)
				r.InvalidateFailures()
				r.Shutdown()
			}()
		}
		wg.Wait()
		r.ObserveFailure(failuretype.ResponseDeliveryUnknown)
		if e.calls.Load() != 1 || !e.shutdown.Load() || len(r.queue) != 0 || r.failures != (failureWindow{}) {
			t.Error("cancel/shutdown retained, flushed or recursively replayed error state")
		}
	})
	for _, alignedStart := range []bool{true, false} {
		start := epoch
		if !alignedStart {
			start = start.Add(time.Second)
		}
		e := &fakeExporter{}
		r := newRuntime(context.Background(), fixtureConfig, Enabled, fakeFactory(e), currentProcess, func() time.Time { return start }, false)
		r.ObserveFailure(failuretype.StorageCloseUnknown)
		r.Shutdown()
		if e.calls.Load() != 0 || r.failures != (failureWindow{}) {
			t.Fatal("partial shutdown flushed failures")
		}
	}
}
