package telemetry

import (
	"context"
	"testing"
	"time"

	"possums-intake/internal/failuretype"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestUnqualifiedFailureCoverageCannotPublishHealthyZeros(t *testing.T) {
	for _, fixture := range []string{"production_coverage_gate", "http_only_fixture"} {
		t.Run(fixture, func(t *testing.T) {
			now := epoch
			clockReads := 0
			exporter := &familyExporter{batches: make(chan metricdata.ResourceMetrics, 1)}
			clock := func() time.Time { clockReads++; return now }
			var runtime *Runtime
			if fixture == "http_only_fixture" {
				runtime, _ = NewHTTPTestRuntime(context.Background(), exporter, clock)
			} else {
				runtime = newRuntimeWithFailureCoverage(context.Background(), fixtureConfig, Enabled,
					fakeFactoryAdapter(exporter),
					func(context.Context) (float64, uint64, bool) { return 0, 0, false }, clock, false, false)
			}
			defer runtime.Shutdown()

			before := clockReads
			runtime.ObserveFailure(failuretype.RequestInvalid)
			runtime.ObserveFailure(0)
			runtime.InvalidateFailures()
			if clockReads != before || runtime.failures != (failureWindow{}) {
				t.Fatal("unqualified failure coverage collected observations")
			}
			for window := 0; window < 2; window++ {
				runtime.ObserveTerminal(Status2xx, 0)
				for second := 1; second <= 300; second++ {
					now = epoch.Add(time.Duration(window*300+second) * time.Second)
					runtime.tick(context.Background())
				}
				data := awaitFamilies(t, exporter)
				metrics := data.ScopeMetrics[0].Metrics
				if len(metrics) != 2 || runtime.failures != (failureWindow{}) {
					t.Fatal("unqualified failure population published healthy zeros")
				}
				seen := map[string]bool{}
				for _, metric := range metrics {
					seen[metric.Name] = true
				}
				if !seen[requestsName] || !seen[bucketsName] || seen[errorsName] {
					t.Fatal("coverage gate changed existing qualified request metrics")
				}
			}
		})
	}
}
