package telemetry

import (
	"context"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// NewHTTPTestRuntime is compiled only into tests. There is no production
// destination override: the caller supplies an in-memory synthetic exporter.
func NewHTTPTestRuntime(ctx context.Context, exporter sdkmetric.Exporter, now func() time.Time) (*Runtime, func()) {
	// This HTTP-only fixture never qualifies failure ownership, including after
	// window warmup. Foundation fixtures enable that independent family explicitly.
	r := newRuntimeWithFailureCoverage(ctx, fixtureConfig, Enabled,
		func(context.Context, config) (sdkmetric.Exporter, func(), error) { return exporter, nil, nil },
		func(context.Context) (float64, uint64, bool) { return 0, 0, false }, now, false, false)
	return r, func() { r.tick(ctx) }
}
