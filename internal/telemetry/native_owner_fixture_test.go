package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// NewSelectedOwnerTestRuntime admits only this explicitly synthetic selected
// population. It is not a production constructor or complete coverage claim.
func NewSelectedOwnerTestRuntime(ctx context.Context, exporter sdkmetric.Exporter, now func() time.Time) (*Runtime, func()) {
	r := newRuntime(ctx, fixtureConfig, Enabled,
		func(context.Context, config) (sdkmetric.Exporter, func(), error) { return exporter, nil, nil },
		func(context.Context) (float64, uint64, bool) { return 0, 0, false }, now, false)
	return r, func() { r.tick(ctx) }
}

func NewSelectedOwnerWireCapture(t *testing.T, handler http.Handler) (*httptest.Server, sdkmetric.Exporter) {
	server := captureServer(handler, true)
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.DisableKeepAlives, transport.ForceAttemptHTTP2 = true, false
	return server, captureExporter(t, server, transport)
}

func QuietSelectedOwnerFixture(t *testing.T, work func()) { assertQuiet(t, work) }
