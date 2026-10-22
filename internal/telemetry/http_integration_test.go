package telemetry_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"possums-intake/internal/httpapi"
	"possums-intake/internal/ledger"
	"possums-intake/internal/telemetry"
	"possums-intake/internal/testfixture"
)

type httpOutageExporter struct {
	started                chan metricdata.ResourceMetrics
	calls, shutdown, flush atomic.Int32
}

func (*httpOutageExporter) Temporality(k sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(k)
}
func (*httpOutageExporter) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(k)
}
func (e *httpOutageExporter) Export(ctx context.Context, data *metricdata.ResourceMetrics) error {
	e.calls.Add(1)
	e.started <- *data
	<-ctx.Done()
	return errors.New("SYNTHETIC_PRIVATE_SENTINEL")
}
func (e *httpOutageExporter) ForceFlush(context.Context) error {
	e.flush.Add(1)
	return errors.New("SYNTHETIC_PRIVATE_SENTINEL")
}
func (e *httpOutageExporter) Shutdown(context.Context) error {
	e.shutdown.Add(1)
	return errors.New("SYNTHETIC_PRIVATE_SENTINEL")
}

func TestHTTPCompleteWindowOutageAndShutdownPreserveAccounting(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "state")
	if ledger.Init(ctx, dir) != nil {
		t.Fatal("state fixture unavailable")
	}
	service, err := ledger.Open(ctx, dir)
	if err != nil {
		t.Fatal("storage fixture unavailable")
	}
	defer service.Close()
	if _, err := testfixture.Create(ctx, service, "synthetic000000"); err != nil {
		t.Fatal("synthetic auth fixture unavailable")
	}
	now := time.Unix(1_800_000_000, 0).UTC() // aligned synthetic window, not request timestamps
	exporter := &httpOutageExporter{started: make(chan metricdata.ResourceMetrics, 1)}
	runtime, tick := telemetry.NewHTTPTestRuntime(ctx, exporter, func() time.Time { return now })
	defer runtime.Shutdown()
	handler, err := httpapi.NewHandler(service, service.Ready, strings.Repeat("s", 32), runtime)
	if err != nil {
		t.Fatal("handler fixture unavailable")
	}
	post := func(path, body, identity string, auth bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if auth {
			req.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 32))
		}
		if identity != "" {
			req.Header.Set("Idempotency-Key", identity)
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		return out
	}
	identity := func(n int) string { return fmt.Sprintf("v1.%013d.%032x", time.Now().UnixMilli(), n) }
	for _, out := range []*httptest.ResponseRecorder{
		post("/balance", `{"account_id":"synthetic000000"}`, "", true),
		post("/credits", `{"account_id":"synthetic000000","amount":"10"}`, identity(1), true),
		post("/balance", `{"account_id":"synthetic000000"}`, "", true),
	} {
		if out.Code != 200 {
			t.Fatal("actual accounting operation failed")
		}
	}
	if post("/balance", `{}`, "", true).Code != 400 || post("/balance", `{}`, "", false).Code != 401 {
		t.Fatal("parser/auth outcome drift")
	}
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/healthz", nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/collections", nil))
	start := now
	for i := 1; i <= 300; i++ {
		now = start.Add(time.Duration(i) * time.Second)
		tick()
	}
	var data metricdata.ResourceMetrics
	select {
	case data = <-exporter.started:
	case <-time.After(5 * time.Second):
		t.Fatal("complete HTTP family not released")
	}
	var requests, buckets int64
	classes := map[string]int64{}
	for _, scope := range data.ScopeMetrics {
		for _, metric := range scope.Metrics {
			gauge, ok := metric.Data.(metricdata.Gauge[int64])
			if !ok {
				t.Fatal("unavailable process samples were exported")
			}
			for _, point := range gauge.DataPoints {
				if metric.Name == "possums.intake.http.requests" {
					requests += point.Value
					for _, attr := range point.Attributes.ToSlice() {
						if attr.Key == "status_class" {
							classes[attr.Value.AsString()] = point.Value
						}
					}
				} else if metric.Name == "possums.intake.http.latency.bucket" {
					buckets += point.Value
				} else {
					t.Fatal("unexpected metric")
				}
				if !point.StartTime.Equal(start) || !point.Time.Equal(start.Add(5*time.Minute)) {
					t.Fatal("partial/nonboundary window")
				}
			}
		}
	}
	if requests != 5 || buckets != 5 || classes["2xx"] != 3 || classes["4xx"] != 2 || classes["unknown"] != 0 || classes["5xx"] != 0 {
		t.Fatal("HTTP terminal population mismatched")
	}
	// Export is actively blocked outside any transaction; accounting still works.
	out := post("/credits", `{"account_id":"synthetic000000","amount":"5"}`, identity(2), true)
	if out.Code != 200 {
		t.Fatal("outage changed accounting")
	}
	runtime.Shutdown() // cancels and joins the blocked exporter, no partial flush
	out = post("/credits", `{"account_id":"synthetic000000","amount":"5"}`, identity(3), true)
	if out.Code != 200 {
		t.Fatal("shutdown changed accounting")
	}
	out = post("/balance", `{"account_id":"synthetic000000"}`, "", true)
	var snapshot struct {
		Available string `json:"available"`
	}
	if json.Unmarshal(out.Body.Bytes(), &snapshot) != nil || snapshot.Available != "20" {
		t.Fatal("outage/shutdown changed money")
	}
	if exporter.calls.Load() != 1 || exporter.shutdown.Load() != 1 || exporter.flush.Load() != 0 {
		t.Fatal("shutdown replayed or flushed")
	}
}
