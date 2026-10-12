package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

const (
	requestsName = "possums.intake.http.requests"
	bucketsName  = "possums.intake.http.latency.bucket"
	cpuName      = "possums.intake.process.cpu"
	rssName      = "possums.intake.process.rss"
	scopeName    = "possums-intake/telemetry"
	scopeVersion = "1"
)

func fixedResource(c config) *resource.Resource {
	return resource.NewSchemaless(attribute.String("service.name", "possums-intake"), attribute.String("deployment.environment.name", c.environment), attribute.String("possums.infrastructure.slot", c.slot))
}

type sdkEngine struct {
	provider *sdkmetric.MeterProvider
	reader   *sdkmetric.ManualReader
	current  batch // exclusively owned by the sender; only its synchronous Collect invokes the callback
	config   config
}

func newSDK(c config) (*sdkEngine, error) {
	e := &sdkEngine{config: config{environment: c.environment, slot: c.slot}}
	e.reader = sdkmetric.NewManualReader(sdkmetric.WithTemporalitySelector(sdkmetric.DefaultTemporalitySelector), sdkmetric.WithAggregationSelector(sdkmetric.DefaultAggregationSelector))
	e.provider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(e.reader), sdkmetric.WithResource(fixedResource(c)), sdkmetric.WithExemplarFilter(exemplar.AlwaysOffFilter), sdkmetric.WithCardinalityLimit(40))
	m := e.provider.Meter(scopeName, metric.WithInstrumentationVersion(scopeVersion))
	req, err := m.Int64ObservableGauge(requestsName, metric.WithUnit("{request}"))
	if err != nil {
		e.close()
		return nil, err
	}
	bins, err := m.Int64ObservableGauge(bucketsName, metric.WithUnit("{request}"))
	if err != nil {
		e.close()
		return nil, err
	}
	cpu, err := m.Float64ObservableGauge(cpuName, metric.WithUnit("{cpu}"))
	if err != nil {
		e.close()
		return nil, err
	}
	rss, err := m.Float64ObservableGauge(rssName, metric.WithUnit("By"))
	if err != nil {
		e.close()
		return nil, err
	}
	_, err = m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		if r := e.current.request; r != nil {
			for i, status := range statusLabels {
				attrs := attribute.String("status_class", status)
				o.ObserveInt64(req, r.counts[i], metric.WithAttributes(attrs))
				for j, bin := range binLabels {
					o.ObserveInt64(bins, r.bins[i][j], metric.WithAttributes(attrs, attribute.String("latency_bin", bin)))
				}
			}
		}
		if p := e.current.process; p != nil {
			o.ObserveFloat64(cpu, p.cpu)
			o.ObserveFloat64(rss, p.rss)
		}
		return nil
	}, req, bins, cpu, rss)
	if err != nil {
		e.close()
		return nil, err
	}
	return e, nil
}
func (e *sdkEngine) close() {
	ctx, cancel := context.WithTimeout(context.Background(), exportTimeout)
	defer cancel()
	_ = e.provider.Shutdown(ctx)
}
func (e *sdkEngine) collect(ctx context.Context, b batch) (metricdata.ResourceMetrics, bool) {
	e.current = b
	var raw metricdata.ResourceMetrics
	err := e.reader.Collect(ctx, &raw)
	e.current = batch{}
	if err != nil {
		return metricdata.ResourceMetrics{}, false
	}
	return sanitize(raw, b, e.config)
}

// This is the final release boundary, before the supported exporter serializes.
// Never forward raw SDK metadata. Rebuild only validated scalar gauges, fixed
// resource/scope fields and aggregate boundary timestamps; drop exemplars.
func sanitize(raw metricdata.ResourceMetrics, b batch, c config) (metricdata.ResourceMetrics, bool) {
	empty := metricdata.ResourceMetrics{}
	if (c.environment != "development" && c.environment != "production") || !slotPattern.MatchString(c.slot) {
		return empty, false
	}
	if b.request == nil && b.process == nil {
		return empty, false
	}
	if b.request != nil && !b.request.consistent() || b.process != nil && !b.process.valid() {
		return empty, false
	}
	if raw.Resource == nil || len(raw.ScopeMetrics) != 1 {
		return empty, false
	}
	expectedResource := fixedResource(c)
	for _, a := range expectedResource.Attributes() {
		v, ok := raw.Resource.Set().Value(a.Key)
		if !ok || v != a.Value {
			return empty, false
		}
	}
	scope := raw.ScopeMetrics[0]
	if scope.Scope.Name != scopeName || scope.Scope.Version != scopeVersion || len(scope.Metrics) > 4 {
		return empty, false
	}
	clean := metricdata.ScopeMetrics{Scope: instrumentation.Scope{Name: scopeName, Version: scopeVersion}}
	seen := map[string]bool{}
	for _, m := range scope.Metrics {
		if seen[m.Name] {
			return empty, false
		}
		seen[m.Name] = true
		out := metricdata.Metrics{Name: m.Name, Unit: m.Unit}
		switch m.Name {
		case requestsName, bucketsName:
			r := b.request
			if r == nil || m.Unit != "{request}" {
				return empty, false
			}
			gauge, ok := m.Data.(metricdata.Gauge[int64])
			if !ok {
				return empty, false
			}
			n := 4
			if m.Name == bucketsName {
				n = 36
			}
			if len(gauge.DataPoints) != n {
				return empty, false
			}
			points := make([]metricdata.DataPoint[int64], 0, n)
			cells := map[[2]int]bool{}
			for _, p := range gauge.DataPoints {
				status := -1
				bin := -1
				for _, a := range p.Attributes.ToSlice() {
					if a.Value.Type() != attribute.STRING {
						return empty, false
					}
					switch a.Key {
					case "status_class":
						for i, s := range statusLabels {
							if a.Value.AsString() == s {
								status = i
							}
						}
					case "latency_bin":
						for i, s := range binLabels {
							if a.Value.AsString() == s {
								bin = i
							}
						}
					default:
						return empty, false
					}
				}
				if status < 0 {
					return empty, false
				}
				expected := r.counts[status]
				attrs := []attribute.KeyValue{attribute.String("status_class", statusLabels[status])}
				if m.Name == bucketsName {
					if bin < 0 || p.Attributes.Len() != 2 {
						return empty, false
					}
					expected = r.bins[status][bin]
					attrs = append(attrs, attribute.String("latency_bin", binLabels[bin]))
				} else if bin != -1 || p.Attributes.Len() != 1 {
					return empty, false
				}
				cell := [2]int{status, bin}
				if cells[cell] || p.Value != expected || p.Value < 0 || p.Value > maxExactInt {
					return empty, false
				}
				cells[cell] = true
				points = append(points, metricdata.DataPoint[int64]{Attributes: attribute.NewSet(attrs...), StartTime: r.start, Time: r.end, Value: p.Value})
			}
			out.Data = metricdata.Gauge[int64]{DataPoints: points}
		case cpuName, rssName:
			p := b.process
			if p == nil {
				return empty, false
			}
			gauge, ok := m.Data.(metricdata.Gauge[float64])
			if !ok || len(gauge.DataPoints) != 1 {
				return empty, false
			}
			v := gauge.DataPoints[0]
			expected, unit, maximum := p.cpu, "{cpu}", maxCPU
			if m.Name == rssName {
				expected, unit, maximum = p.rss, "By", float64(maxExactInt)
			}
			if m.Unit != unit || v.Attributes.Len() != 0 || v.Value != expected || !finiteRange(v.Value, maximum) {
				return empty, false
			}
			out.Data = metricdata.Gauge[float64]{DataPoints: []metricdata.DataPoint[float64]{{StartTime: p.start, Time: p.end, Value: v.Value}}}
		default:
			return empty, false
		}
		clean.Metrics = append(clean.Metrics, out)
	}
	if b.request != nil && (!seen[requestsName] || !seen[bucketsName]) || b.process != nil && (!seen[cpuName] || !seen[rssName]) {
		return empty, false
	}
	return metricdata.ResourceMetrics{Resource: expectedResource, ScopeMetrics: []metricdata.ScopeMetrics{clean}}, true
}
