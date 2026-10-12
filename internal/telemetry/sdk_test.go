package telemetry

import (
	"context"
	"math"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

const sentinel = "SYNTHETIC_PRIVATE_SENTINEL"

var fixtureConfig = config{environment: "development", slot: "intake-01", key: "synthetic_ingestion_key"}

func fixtureBatch() batch {
	r := &requestRelease{start: epoch, end: epoch.Add(requestPeriod)}
	r.counts[Status2xx] = 1
	r.bins[Status2xx][1] = 1
	return batch{request: r, process: &processRelease{start: epoch.Add(4 * time.Minute), end: epoch.Add(5 * time.Minute), cpu: 0.5, rss: 1350}}
}
func collectFixture(t *testing.T, b batch) metricdata.ResourceMetrics {
	t.Helper()
	e, err := newSDK(fixtureConfig)
	if err != nil {
		t.Fatal("SDK constructor failed")
	}
	defer e.close()
	raw, ok := e.collect(context.Background(), b)
	if !ok {
		t.Fatal("SDK complete-window collection rejected")
	}
	return raw
}
func metricIndex(raw metricdata.ResourceMetrics, name string) int {
	for i, m := range raw.ScopeMetrics[0].Metrics {
		if m.Name == name {
			return i
		}
	}
	panic("fixture metric absent")
}
func TestSDKFamilySelectionNoStaleGauges(t *testing.T) {
	e, err := newSDK(fixtureConfig)
	if err != nil {
		t.Fatal("SDK constructor failed")
	}
	defer e.close()
	b := fixtureBatch()
	for _, want := range []batch{b, {request: b.request}, {process: b.process}, {request: b.request}} {
		raw, ok := e.collect(context.Background(), want)
		if !ok {
			t.Fatal("stale SDK values or missing family")
		}
		n := 2
		if want.request != nil && want.process != nil {
			n = 4
		}
		if len(raw.ScopeMetrics[0].Metrics) != n {
			t.Fatal("unrequested family retained by SDK")
		}
	}
}
func TestSanitizerStripsResourceScopeDescriptionExemplarsAndTimes(t *testing.T) {
	b := fixtureBatch()
	raw := collectFixture(t, b)
	raw.Resource = resource.NewWithAttributes(sentinel, append(fixedResource(fixtureConfig).Attributes(), attribute.String("secret", sentinel))...)
	raw.ScopeMetrics[0].Scope.Attributes = attribute.NewSet(attribute.String("secret", sentinel))
	raw.ScopeMetrics[0].Scope.SchemaURL = sentinel
	for i, m := range raw.ScopeMetrics[0].Metrics {
		raw.ScopeMetrics[0].Metrics[i].Description = sentinel
		switch g := m.Data.(type) {
		case metricdata.Gauge[int64]:
			for j := range g.DataPoints {
				g.DataPoints[j].Time = epoch.Add(17 * time.Second)
				g.DataPoints[j].StartTime = epoch.Add(3 * time.Second)
				g.DataPoints[j].Exemplars = []metricdata.Exemplar[int64]{{FilteredAttributes: []attribute.KeyValue{attribute.String("secret", sentinel)}, TraceID: []byte(sentinel), SpanID: []byte(sentinel), Value: 1}}
			}
			raw.ScopeMetrics[0].Metrics[i].Data = g
		case metricdata.Gauge[float64]:
			g.DataPoints[0].Exemplars = []metricdata.Exemplar[float64]{{FilteredAttributes: []attribute.KeyValue{attribute.String("secret", sentinel)}}}
			raw.ScopeMetrics[0].Metrics[i].Data = g
		}
	}
	clean, ok := sanitize(raw, b, fixtureConfig)
	if !ok {
		t.Fatal("approved scalars failed sanitizer")
	}
	if clean.Resource.SchemaURL() != "" || len(clean.Resource.Attributes()) != 3 || clean.ScopeMetrics[0].Scope.Attributes.Len() != 0 || clean.ScopeMetrics[0].Scope.SchemaURL != "" {
		t.Fatal("free resource/scope metadata survived")
	}
	for _, m := range clean.ScopeMetrics[0].Metrics {
		if m.Description != "" {
			t.Fatal("free description survived")
		}
		switch g := m.Data.(type) {
		case metricdata.Gauge[int64]:
			for _, p := range g.DataPoints {
				if len(p.Exemplars) != 0 || !p.StartTime.Equal(b.request.start) || !p.Time.Equal(b.request.end) {
					t.Fatal("non-boundary time/exemplar survived")
				}
			}
		case metricdata.Gauge[float64]:
			if len(g.DataPoints[0].Exemplars) != 0 {
				t.Fatal("process exemplar survived")
			}
		}
	}
}
func TestSanitizerRejectsHostileAndMalformedFamilies(t *testing.T) {
	cases := map[string]func(*metricdata.ResourceMetrics){
		"metric-name": func(r *metricdata.ResourceMetrics) { r.ScopeMetrics[0].Metrics[0].Name = sentinel },
		"unit":        func(r *metricdata.ResourceMetrics) { r.ScopeMetrics[0].Metrics[0].Unit = sentinel },
		"metric-type": func(r *metricdata.ResourceMetrics) {
			i := metricIndex(*r, requestsName)
			r.ScopeMetrics[0].Metrics[i].Data = metricdata.Gauge[float64]{}
		},
		"attribute-type": func(r *metricdata.ResourceMetrics) {
			i := metricIndex(*r, requestsName)
			g := r.ScopeMetrics[0].Metrics[i].Data.(metricdata.Gauge[int64])
			g.DataPoints[0].Attributes = attribute.NewSet(attribute.Int("status_class", 2))
			r.ScopeMetrics[0].Metrics[i].Data = g
		},
		"scope":   func(r *metricdata.ResourceMetrics) { r.ScopeMetrics[0].Scope.Name = sentinel },
		"version": func(r *metricdata.ResourceMetrics) { r.ScopeMetrics[0].Scope.Version = sentinel },
		"resource-value": func(r *metricdata.ResourceMetrics) {
			r.Resource = resource.NewSchemaless(attribute.String("service.name", sentinel))
		},
		"nil-resource":     func(r *metricdata.ResourceMetrics) { r.Resource = nil },
		"missing-family":   func(r *metricdata.ResourceMetrics) { r.ScopeMetrics[0].Metrics = r.ScopeMetrics[0].Metrics[1:] },
		"duplicate-family": func(r *metricdata.ResourceMetrics) { r.ScopeMetrics[0].Metrics[1] = r.ScopeMetrics[0].Metrics[0] },
		"point-secret": func(r *metricdata.ResourceMetrics) {
			i := metricIndex(*r, requestsName)
			g := r.ScopeMetrics[0].Metrics[i].Data.(metricdata.Gauge[int64])
			g.DataPoints[0].Attributes = attribute.NewSet(attribute.String("status_class", "2xx"), attribute.String("secret", sentinel))
			r.ScopeMetrics[0].Metrics[i].Data = g
		},
		"point-enum": func(r *metricdata.ResourceMetrics) {
			i := metricIndex(*r, requestsName)
			g := r.ScopeMetrics[0].Metrics[i].Data.(metricdata.Gauge[int64])
			g.DataPoints[0].Attributes = attribute.NewSet(attribute.String("status_class", sentinel))
			r.ScopeMetrics[0].Metrics[i].Data = g
		},
		"point-count": func(r *metricdata.ResourceMetrics) {
			i := metricIndex(*r, requestsName)
			g := r.ScopeMetrics[0].Metrics[i].Data.(metricdata.Gauge[int64])
			g.DataPoints[0].Value++
			r.ScopeMetrics[0].Metrics[i].Data = g
		},
		"duplicate-cell": func(r *metricdata.ResourceMetrics) {
			i := metricIndex(*r, bucketsName)
			g := r.ScopeMetrics[0].Metrics[i].Data.(metricdata.Gauge[int64])
			g.DataPoints[1] = g.DataPoints[0]
			r.ScopeMetrics[0].Metrics[i].Data = g
		},
		"missing-cell": func(r *metricdata.ResourceMetrics) {
			i := metricIndex(*r, bucketsName)
			g := r.ScopeMetrics[0].Metrics[i].Data.(metricdata.Gauge[int64])
			g.DataPoints = g.DataPoints[1:]
			r.ScopeMetrics[0].Metrics[i].Data = g
		},
		"nan": func(r *metricdata.ResourceMetrics) {
			i := metricIndex(*r, cpuName)
			g := r.ScopeMetrics[0].Metrics[i].Data.(metricdata.Gauge[float64])
			g.DataPoints[0].Value = math.NaN()
			r.ScopeMetrics[0].Metrics[i].Data = g
		},
		"inf": func(r *metricdata.ResourceMetrics) {
			i := metricIndex(*r, rssName)
			g := r.ScopeMetrics[0].Metrics[i].Data.(metricdata.Gauge[float64])
			g.DataPoints[0].Value = math.Inf(1)
			r.ScopeMetrics[0].Metrics[i].Data = g
		},
		"process-attribute": func(r *metricdata.ResourceMetrics) {
			i := metricIndex(*r, cpuName)
			g := r.ScopeMetrics[0].Metrics[i].Data.(metricdata.Gauge[float64])
			g.DataPoints[0].Attributes = attribute.NewSet(attribute.String("pid", sentinel))
			r.ScopeMetrics[0].Metrics[i].Data = g
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			b := fixtureBatch()
			raw := collectFixture(t, b)
			mutate(&raw)
			if _, ok := sanitize(raw, b, fixtureConfig); ok {
				t.Fatal("unsafe/malformed family passed")
			}
		})
	}
	b := fixtureBatch()
	raw := collectFixture(t, b)
	c := fixtureConfig
	c.environment = sentinel
	if _, ok := sanitize(raw, b, c); ok {
		t.Fatal("unreviewed config resource accepted")
	}
	b.request.counts[0] = 1
	if _, ok := sanitize(raw, b, fixtureConfig); ok {
		t.Fatal("inconsistent source accepted")
	}
}
