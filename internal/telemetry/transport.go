package telemetry

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

const exportTimeout = 2 * time.Second

// Removing GetBody and disabling connection reuse also closes net/http's
// transparent replay path for this body-bearing POST. This is transport policy,
// not a custom serializer/exporter or a custom DNS resolver.
type oneAttempt struct{ transport *http.Transport }

func (t oneAttempt) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.GetBody = nil
	return t.transport.RoundTrip(clone)
}
func exportClient(transport *http.Transport) *http.Client {
	return &http.Client{Transport: oneAttempt{transport}, Timeout: exportTimeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}
func productionTransport() *http.Transport {
	return &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: exportTimeout, KeepAlive: -1}).DialContext,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "api.honeycomb.io"},
		TLSHandshakeTimeout: exportTimeout, ResponseHeaderTimeout: exportTimeout,
		DisableKeepAlives: true, ForceAttemptHTTP2: false, MaxConnsPerHost: 1,
	}
}
func productionExporter(ctx context.Context, c config) (sdkmetric.Exporter, func(), error) {
	transport := productionTransport()
	exporter, err := otlpmetrichttp.New(ctx, exportOptions(c, exportClient(transport))...)
	return exporter, transport.CloseIdleConnections, err
}
func exportOptions(c config, client *http.Client) []otlpmetrichttp.Option {
	return []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpointURL(honeycombEndpoint), otlpmetrichttp.WithURLPath("/v1/metrics"),
		otlpmetrichttp.WithHeaders(map[string]string{"x-honeycomb-team": c.key}),
		otlpmetrichttp.WithHTTPClient(client),
		otlpmetrichttp.WithCompression(otlpmetrichttp.NoCompression),
		otlpmetrichttp.WithRetry(otlpmetrichttp.RetryConfig{Enabled: false}),
		otlpmetrichttp.WithTimeout(exportTimeout), otlpmetrichttp.WithMaxRequestSize(16 * 1024),
		otlpmetrichttp.WithTemporalitySelector(sdkmetric.DefaultTemporalitySelector),
		otlpmetrichttp.WithAggregationSelector(sdkmetric.DefaultAggregationSelector),
	}
}
