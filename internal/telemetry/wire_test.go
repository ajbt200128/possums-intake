package telemetry

import (
	"bytes"
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	collector "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// Local destination overrides exist ONLY in this test file. Reuse every runtime
// option, appending the isolated numeric-loopback endpoint last. No runtime flag
// or public constructor can set a local destination.
func captureExporter(t *testing.T, server *httptest.Server, transport *http.Transport) sdkmetric.Exporter {
	t.Helper()
	u, err := url.Parse(server.URL)
	if err != nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		t.Fatal("capture is not isolated numeric loopback")
	}
	opts := exportOptions(fixtureConfig, exportClient(transport))
	opts = append(opts, otlpmetrichttp.WithEndpointURL(server.URL+"/v1/metrics"))
	e, err := otlpmetrichttp.New(context.Background(), opts...)
	if err != nil {
		t.Fatal("supported capture exporter unavailable")
	}
	t.Cleanup(func() { shutdownExporter(e); transport.CloseIdleConnections() })
	return e
}
func captureServer(handler http.Handler, tls bool) *httptest.Server {
	s := httptest.NewUnstartedServer(handler)
	// Synthetic TLS failure tests must not emit even the server's raw handshake errors.
	s.Config.ErrorLog = log.New(io.Discard, "", 0)
	if tls {
		s.StartTLS()
	} else {
		s.Start()
	}
	return s
}
func capturedBody(t *testing.T, body <-chan []byte) []byte {
	t.Helper()
	select {
	case b := <-body:
		return b
	case <-time.After(5 * time.Second):
		t.Fatal("synthetic protobuf request absent")
		return nil
	}
}
func TestSupportedExporterActualProtobufPrivacy(t *testing.T) {
	body := make(chan []byte, 1)
	var headerOK atomic.Bool
	server := captureServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerOK.Store(r.Method == http.MethodPost && r.URL.Path == "/v1/metrics" && r.URL.RawQuery == "" && r.Header.Get("Content-Type") == "application/x-protobuf" && r.Header.Get("X-Honeycomb-Team") == fixtureConfig.key && r.Header.Get("Content-Encoding") == "")
		b, _ := io.ReadAll(io.LimitReader(r.Body, 16*1024+1))
		body <- b
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(200)
	}), true)
	defer server.Close()
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true
	transport.ForceAttemptHTTP2 = false
	e := captureExporter(t, server, transport)
	b := fixtureBatch()
	raw := collectFixture(t, b)
	raw.Resource = resource.NewWithAttributes(sentinel, append(fixedResource(fixtureConfig).Attributes(), attribute.String("private", sentinel))...)
	raw.ScopeMetrics[0].Scope.Attributes = attribute.NewSet(attribute.String("private", sentinel))
	raw.ScopeMetrics[0].Scope.SchemaURL = sentinel
	for i := range raw.ScopeMetrics[0].Metrics {
		raw.ScopeMetrics[0].Metrics[i].Description = sentinel
	}
	clean, ok := sanitize(raw, b, fixtureConfig)
	if !ok {
		t.Fatal("safe aggregate metadata sanitization failed")
	}
	assertQuiet(t, func() {
		if e.Export(context.Background(), &clean) != nil {
			t.Error("synthetic supported exporter failed")
		}
	})
	wire := capturedBody(t, body)
	for _, s := range []string{sentinel, fixtureConfig.key, "account_id", "reservation_id", "idempotency_key", "charged_amount", "trace_id", "span_id"} {
		if bytes.Contains(wire, []byte(s)) {
			t.Fatal("forbidden synthetic material survived serialization")
		}
	}
	if len(wire) > 16*1024 || !headerOK.Load() {
		t.Fatal("wire/header contract violated")
	}
	var decoded collector.ExportMetricsServiceRequest
	if proto.Unmarshal(wire, &decoded) != nil {
		t.Fatal("actual serializer bytes invalid protobuf")
	}
	assertDecoded(t, &decoded, b)
}
func attrsMap(t *testing.T, attrs []*commonpb.KeyValue) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, a := range attrs {
		if a.Value == nil {
			t.Fatal("attribute missing value")
		}
		if _, ok := a.Value.Value.(*commonpb.AnyValue_StringValue); !ok {
			t.Fatal("unexpected attribute type")
		}
		if _, exists := m[a.Key]; exists {
			t.Fatal("duplicate attribute")
		}
		m[a.Key] = a.Value.GetStringValue()
	}
	return m
}
func assertDecoded(t *testing.T, d *collector.ExportMetricsServiceRequest, b batch) {
	t.Helper()
	if len(d.ResourceMetrics) != 1 {
		t.Fatal("unexpected resource cardinality")
	}
	r := d.ResourceMetrics[0]
	if r.SchemaUrl != "" || r.Resource.DroppedAttributesCount != 0 || len(r.ScopeMetrics) != 1 {
		t.Fatal("unexpected resource metadata")
	}
	attrs := attrsMap(t, r.Resource.Attributes)
	if len(attrs) != 3 || attrs["service.name"] != "possums-intake" || attrs["deployment.environment.name"] != "development" || attrs["possums.infrastructure.slot"] != "intake-01" {
		t.Fatal("resource allowlist violated")
	}
	scope := r.ScopeMetrics[0]
	if scope.SchemaUrl != "" || scope.Scope.Name != scopeName || scope.Scope.Version != scopeVersion || len(scope.Scope.Attributes) != 0 || scope.Scope.DroppedAttributesCount != 0 || len(scope.Metrics) != 4 {
		t.Fatal("scope/schema contract violated")
	}
	seen := map[string]bool{}
	for _, m := range scope.Metrics {
		if seen[m.Name] || m.Description != "" || len(m.Metadata) != 0 {
			t.Fatal("free/duplicate metric metadata")
		}
		seen[m.Name] = true
		g := m.GetGauge()
		if g == nil {
			t.Fatal("non-gauge serialization")
		}
		for _, p := range g.DataPoints {
			if len(p.Exemplars) != 0 || p.Flags != 0 {
				t.Fatal("unexpected exemplar or flags")
			}
		}
		switch m.Name {
		case requestsName, bucketsName:
			n := 4
			if m.Name == bucketsName {
				n = 36
			}
			if m.Unit != "{request}" || len(g.DataPoints) != n {
				t.Fatal("count/bucket shape mismatch")
			}
			cells := map[string]bool{}
			for _, p := range g.DataPoints {
				attrs := attrsMap(t, p.Attributes)
				status := attrs["status_class"]
				statusIndex := -1
				for i, s := range statusLabels {
					if status == s {
						statusIndex = i
					}
				}
				if statusIndex < 0 {
					t.Fatal("nonclosed status class")
				}
				expected := b.request.counts[statusIndex]
				key := status
				if m.Name == bucketsName {
					if len(attrs) != 2 {
						t.Fatal("bucket dimensions expanded")
					}
					bin := -1
					for i, s := range binLabels {
						if attrs["latency_bin"] == s {
							bin = i
						}
					}
					if bin < 0 {
						t.Fatal("nonclosed latency bin")
					}
					expected = b.request.bins[statusIndex][bin]
					key += "/" + strconv.Itoa(bin)
				} else if len(attrs) != 1 {
					t.Fatal("request dimensions expanded")
				}
				if cells[key] {
					t.Fatal("repeated gauge cell")
				}
				cells[key] = true
				if _, ok := p.Value.(*metricpb.NumberDataPoint_AsInt); !ok || p.GetAsInt() != expected || p.StartTimeUnixNano != uint64(b.request.start.UnixNano()) || p.TimeUnixNano != uint64(b.request.end.UnixNano()) {
					t.Fatal("integer count or boundary mismatch")
				}
			}
		case cpuName, rssName:
			unit, value := "{cpu}", b.process.cpu
			if m.Name == rssName {
				unit, value = "By", b.process.rss
			}
			if m.Unit != unit || len(g.DataPoints) != 1 {
				t.Fatal("process gauge shape mismatch")
			}
			p := g.DataPoints[0]
			if _, ok := p.Value.(*metricpb.NumberDataPoint_AsDouble); !ok || p.GetAsDouble() != value || len(p.Attributes) != 0 || p.StartTimeUnixNano != uint64(b.process.start.UnixNano()) || p.TimeUnixNano != uint64(b.process.end.UnixNano()) {
				t.Fatal("process mean/boundary mismatch")
			}
		default:
			t.Fatal("metric name outside allowlist")
		}
	}
}
func TestRedirectRefusedAndAuthenticationNotForwarded(t *testing.T) {
	var forwarded atomic.Int32
	var attempts atomic.Int32
	target := captureServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1); w.WriteHeader(200) }), false)
	defer target.Close()
	origin := captureServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Location", target.URL+"/private")
		w.WriteHeader(http.StatusTemporaryRedirect)
		_, _ = w.Write([]byte(sentinel))
	}), false)
	defer origin.Close()
	transport := productionTransport()
	transport.TLSClientConfig = nil
	e := captureExporter(t, origin, transport)
	raw := collectFixture(t, fixtureBatch())
	assertQuiet(t, func() {
		if e.Export(context.Background(), &raw) == nil {
			t.Error("redirect falsely accepted")
		}
	})
	if forwarded.Load() != 0 || attempts.Load() != 1 {
		t.Fatal("redirect forwarded credentials or replayed")
	}
}
func TestRetryableHostileErrorsPartialSuccessAndTransportNoReplay(t *testing.T) {
	for _, status := range []int{200, 429, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var attempts atomic.Int32
			s := captureServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				if status == 200 {
					w.Header().Set("Content-Type", "application/x-protobuf")
					response := &collector.ExportMetricsServiceResponse{PartialSuccess: &collector.ExportMetricsPartialSuccess{RejectedDataPoints: 1, ErrorMessage: sentinel}}
					wire, _ := proto.Marshal(response)
					_, _ = w.Write(wire)
					return
				}
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(sentinel))
			}), false)
			defer s.Close()
			transport := productionTransport()
			transport.TLSClientConfig = nil
			e := captureExporter(t, s, transport)
			raw := collectFixture(t, fixtureBatch())
			assertQuiet(t, func() {
				if e.Export(context.Background(), &raw) == nil {
					t.Error("upstream rejection falsely succeeded")
				}
			})
			if attempts.Load() != 1 {
				t.Fatal("exporter automatically replayed rejected batch")
			}
		})
	}
}
func TestTLSVerificationAndFixedProductionTransport(t *testing.T) {
	var attempts atomic.Int32
	s := captureServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts.Add(1); w.WriteHeader(200) }), true)
	defer s.Close()
	tr := productionTransport()
	if tr.Proxy != nil || !tr.DisableKeepAlives || tr.ForceAttemptHTTP2 || tr.TLSClientConfig.InsecureSkipVerify || tr.TLSClientConfig.ServerName != "api.honeycomb.io" || tr.TLSClientConfig.RootCAs != nil {
		t.Fatal("production transport configuration weakened")
	}
	e := captureExporter(t, s, tr)
	raw := collectFixture(t, fixtureBatch())
	assertQuiet(t, func() {
		if e.Export(context.Background(), &raw) == nil {
			t.Error("untrusted local certificate accepted")
		}
	})
	if attempts.Load() != 0 {
		t.Fatal("HTTP data sent before certificate verification")
	}
	// Trusting the fixture issuer must still not bypass hostname verification.
	tr = s.Client().Transport.(*http.Transport).Clone()
	tr.TLSClientConfig.ServerName = "api.honeycomb.io"
	tr.DisableKeepAlives = true
	e = captureExporter(t, s, tr)
	assertQuiet(t, func() {
		if e.Export(context.Background(), &raw) == nil {
			t.Error("wrong hostname accepted")
		}
	})
	if attempts.Load() != 0 {
		t.Fatal("HTTP data sent before hostname verification")
	}
	opts := exportOptions(fixtureConfig, exportClient(productionTransport()))
	if len(opts) == 0 || !strings.HasPrefix(honeycombEndpoint, "https://api.honeycomb.io/") {
		t.Fatal("production endpoint unavailable")
	}
}
func TestTransportRemovesBodyReplayHandle(t *testing.T) {
	var attempts atomic.Int32
	s := captureServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		h, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		c, _, err := h.Hijack()
		if err == nil {
			c.Close()
		}
	}), false)
	defer s.Close()
	tr := productionTransport()
	tr.TLSClientConfig = nil
	e := captureExporter(t, s, tr)
	raw := collectFixture(t, fixtureBatch())
	assertQuiet(t, func() {
		if e.Export(context.Background(), &raw) == nil {
			t.Error("interrupted transport falsely succeeded")
		}
	})
	if attempts.Load() != 1 {
		t.Fatal("interrupted transport replayed")
	}
}
