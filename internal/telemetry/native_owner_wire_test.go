package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	collector "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/httpapi"
	"possums-intake/internal/ledger"
	"possums-intake/internal/operator"
	"possums-intake/internal/telemetry"
)

type ownerCollectExporter struct {
	batches chan metricdata.ResourceMetrics
}

func (*ownerCollectExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(kind)
}
func (*ownerCollectExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(kind)
}
func (e *ownerCollectExporter) Export(ctx context.Context, data *metricdata.ResourceMetrics) error {
	select {
	case e.batches <- *data:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (*ownerCollectExporter) ForceFlush(context.Context) error {
	return errors.New("SYNTHETIC_FORBIDDEN_FLUSH")
}
func (*ownerCollectExporter) Shutdown(context.Context) error { return nil }

func TestSelectedActualReportInvalidityWithholdsOnlyErrorFamily(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	ctx := context.Background()
	if ledger.Init(ctx, dir) != nil {
		t.Fatal("state fixture unavailable")
	}
	s, err := ledger.Open(ctx, dir)
	if err != nil {
		t.Fatal("actual service unavailable")
	}
	defer s.Close()
	now := time.Unix(1_800_000_000, 0).UTC()
	start := now
	e := &ownerCollectExporter{batches: make(chan metricdata.ResourceMetrics, 2)}
	r, tick := telemetry.NewSelectedOwnerTestRuntime(ctx, e, func() time.Time { return now })
	defer r.Shutdown()
	h, err := httpapi.NewHandler(s, s.Ready, strings.Repeat("s", 32), r)
	if err != nil {
		t.Fatal("HTTP fixture unavailable")
	}
	req := httptest.NewRequest("POST", "/accounts/provision", strings.NewReader(`{"account_id":"synthetic"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 32))
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	if out.Code != 200 {
		t.Fatal("actual custom operation unavailable")
	}
	// Deliberately consume too early, then invoke actual Cleanup. The outer
	// owner sees report-integrity loss, not a fabricated zero-error window.
	runCtx, report := ledger.WithFailureReport(ctx)
	_, _, _ = report.Take()
	_, cleanupErr := s.Cleanup(runCtx, false)
	ledger.ObserveRunFailures(r, report, cleanupErr, failuretype.MaintenanceUnknown)
	if cleanupErr != nil {
		t.Fatal("numeric report misuse changed cleanup")
	}
	for window := 1; window <= 2; window++ {
		for i := (window-1)*300 + 1; i <= window*300; i++ {
			now = start.Add(time.Duration(i) * time.Second)
			tick()
		}
		var data metricdata.ResourceMetrics
		select {
		case data = <-e.batches:
		case <-time.After(5 * time.Second):
			t.Fatal("selected family absent")
		}
		seenErrors := false
		for _, metric := range data.ScopeMetrics[0].Metrics {
			if metric.Name == "possums.intake.interface.errors" {
				seenErrors = true
				gauge := metric.Data.(metricdata.Gauge[int64])
				if len(gauge.DataPoints) != 39 {
					t.Fatal("quiet window lacks exact closed population")
				}
				for _, point := range gauge.DataPoints {
					if point.Value != 0 {
						t.Fatal("quiet window manufactured failure")
					}
				}
			}
		}
		if seenErrors != (window == 2) || len(data.ScopeMetrics[0].Metrics) != window+1 {
			t.Fatal("integrity loss affected independent request family or never recovered")
		}
	}
}

func ownerWireAttributes(t *testing.T, attrs []*commonpb.KeyValue) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, attr := range attrs {
		if attr.Value == nil {
			t.Fatal("wire attribute unavailable")
		}
		if _, ok := attr.Value.Value.(*commonpb.AnyValue_StringValue); !ok {
			t.Fatal("wire attribute type differs")
		}
		if _, duplicate := result[attr.Key]; duplicate {
			t.Fatal("duplicate wire attribute")
		}
		result[attr.Key] = attr.Value.GetStringValue()
	}
	return result
}

// This private runtime admits a SELECTED synthetic owner population, not whole
// service coverage. Production's static admission remains false. The source
// fixtures, supported SDK and numeric-loopback TLS are the only destination.
func TestSelectedActualOwnersSupportedTLSWireAndOutageNoReplay(t *testing.T) {
	epoch := time.Unix(1_800_000_000, 0).UTC()
	const sentinel = "SYNTHETIC_PRIVATE_SENTINEL"
	for _, outage := range []bool{false, true} {
		t.Run(map[bool]string{false: "wire", true: "outage"}[outage], func(t *testing.T) {
			telemetry.QuietSelectedOwnerFixture(t, func() {
				body := make(chan []byte, 1)
				var attempts atomic.Int64
				wireServer, exporter := telemetry.NewSelectedOwnerWireCapture(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					attempts.Add(1)
					raw, _ := io.ReadAll(io.LimitReader(req.Body, 16*1024+1))
					body <- raw
					if outage {
						w.WriteHeader(503)
						_, _ = w.Write([]byte(sentinel))
						return
					}
					w.Header().Set("Content-Type", "application/x-protobuf")
				}))
				defer wireServer.Close()
				var clock atomic.Int64
				clock.Store(epoch.UnixNano())
				now := func() time.Time { return time.Unix(0, clock.Load()).UTC() }
				r, tick := telemetry.NewSelectedOwnerTestRuntime(context.Background(), exporter, now)
				defer r.Shutdown()
				dir := filepath.Join(t.TempDir(), "state")
				credentials := ledger.OperatorCredentials{Email: "fixture@example.invalid", Password: "SYNTHETIC_NATIVE_PASSWORD_123"}
				if ledger.InitOperatorObserved(context.Background(), dir, credentials, r) != nil {
					t.Error("actual synthetic init unavailable")
					return
				}
				if ledger.InitObserved(context.Background(), dir, r) == nil {
					t.Error("nonempty init accepted")
					return
				}
				s, err := ledger.OpenObserved(context.Background(), dir, r)
				if err != nil {
					t.Error("actual synthetic open unavailable")
					return
				}
				defer s.Close()
				app, err := s.OperatorAppObserved(r)
				if err != nil {
					t.Error("native app unavailable")
					return
				}
				key := "SYNTHETIC_ACCOUNTING_KEY_AT_LEAST_32_BYTES"
				custom, err := httpapi.NewHandler(s, s.Ready, key, r)
				if err != nil {
					t.Error("custom owner unavailable")
					return
				}
				h, err := operator.NewHandlerObserved(custom, app, s, r)
				if err != nil {
					t.Error("native owner unavailable")
					return
				}
				done := make(chan struct{}, 1)
				service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { h.ServeHTTP(w, req); done <- struct{}{} }))
				defer service.Close()
				send := func(method, path, payload, token string) (int, []byte) {
					req, _ := http.NewRequest(method, service.URL+path, strings.NewReader(payload))
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Authorization", token)
					req.Header.Set("User-Agent", sentinel)
					resp, err := service.Client().Do(req)
					if err != nil {
						t.Error("actual owner HTTP unavailable")
						return 0, nil
					}
					raw, _ := io.ReadAll(resp.Body)
					resp.Body.Close()
					<-done
					if resp.StatusCode >= 400 && bytes.Contains(raw, []byte(sentinel)) {
						t.Error("hostile native error escaped")
					}
					return resp.StatusCode, raw
				}
				if status, _ := send("POST", "/accounts/provision", `{"account_id":"`+sentinel+`"}`, "Bearer "+key); status != 200 {
					t.Error("custom success unavailable")
				}
				send("POST", "/balance", `{"account_id":"`+sentinel+`"}`, "Bearer "+sentinel)
				send("GET", "/api/settings", "", "")
				send("GET", "/api/backups", "", "")
				send("POST", "/api/collections/ledger_accounts/records", `{"balance":1}`, "")
				loginBody := `{"identity":"fixture@example.invalid","password":"SYNTHETIC_NATIVE_PASSWORD_123"}`
				status, raw := send("POST", "/api/collections/_superusers/auth-with-password", loginBody, "")
				var auth struct{ Token string }
				if status != 200 || json.Unmarshal(raw, &auth) != nil || auth.Token == "" {
					t.Error("actual native login unavailable")
					return
				}
				nativeBody := `{"account_id":"fake-native","balance":"10","enabled":true}`
				if status, _ := send("POST", "/api/collections/ledger_accounts/records", nativeBody, auth.Token); status != 200 {
					t.Error("actual native create unavailable")
				}
				if status, _ := send("POST", "/api/collections/ledger_accounts/records", nativeBody, auth.Token); status != 400 {
					t.Error("native refusal unavailable")
				}
				id := app.OnRecordAuthWithPasswordRequest().BindFunc(func(*core.RecordAuthWithPasswordRequestEvent) error { return errors.New(sentinel) })
				send("POST", "/api/collections/_superusers/auth-with-password", loginBody, "")
				app.OnRecordAuthWithPasswordRequest().Unbind(id)
				var stored int
				if app.DB().NewQuery("SELECT count(*) FROM _authOrigins").Row(&stored) != nil || stored != 0 {
					t.Error("native origins persisted")
				}
				if app.AuxDB().NewQuery("SELECT count(*) FROM _logs").Row(&stored) != nil || stored != 0 {
					t.Error("native logs persisted")
				}
				closeHook := app.OnTerminate().BindFunc(func(e *core.TerminateEvent) error {
					if err := e.Next(); err != nil {
						return err
					}
					return errors.New(sentinel)
				})
				if s.CloseObserved(r) == nil {
					t.Error("actual closure fault unavailable")
				}
				app.OnTerminate().Unbind(closeHook)
				ctx, report := ledger.WithFailureReport(context.Background())
				_, cleanupErr := s.Cleanup(ctx, false)
				ledger.ObserveRunFailures(r, report, cleanupErr, failuretype.MaintenanceUnknown)
				if cleanupErr == nil {
					t.Error("actual cleanup failure unavailable")
				}
				for i := 1; i <= 300; i++ {
					clock.Store(epoch.Add(time.Duration(i) * time.Second).UnixNano())
					tick()
				}
				var wire []byte
				select {
				case wire = <-body:
				case <-time.After(5 * time.Second):
					t.Error("integrated wire absent")
					return
				}
				r.Shutdown() // joins a failed attempt too; no retry or final flush
				if attempts.Load() != 1 || len(body) != 0 || len(wire) >= 16*1024 {
					t.Error("integrated wire replayed or exceeded cap")
				}
				for _, forbidden := range []string{sentinel, key, credentials.Password, auth.Token, "account_id", "balance", "reservation_id", "idempotency_key"} {
					if bytes.Contains(wire, []byte(forbidden)) {
						t.Error("owner data escaped to wire")
					}
				}
				var decoded collector.ExportMetricsServiceRequest
				if proto.Unmarshal(wire, &decoded) != nil || len(decoded.ResourceMetrics) != 1 || len(decoded.ResourceMetrics[0].ScopeMetrics) != 1 {
					t.Error("integrated protobuf unavailable")
					return
				}
				metrics := decoded.ResourceMetrics[0].ScopeMetrics[0].Metrics
				if len(metrics) != 3 {
					t.Error("failed process source suppressed valid owners or native success added instruments")
					return
				}
				want := map[string]int64{failuretype.RequestInvalid.Label(): 2, failuretype.AuthenticationRefused.Label(): 2, failuretype.PolicyRefused.Label(): 1, failuretype.InitializationUnknown.Label(): 1, failuretype.StorageCloseUnknown.Label(): 1, failuretype.NativeAuthUnknown.Label(): 1, failuretype.NotReady.Label(): 1}
				for _, m := range metrics {
					if m.GetGauge() == nil {
						t.Error("nongauge owner export")
						continue
					}
					switch m.Name {
					case "possums.intake.interface.errors":
						if len(m.GetGauge().DataPoints) != 39 {
							t.Error("error population not complete")
						}
						for _, p := range m.GetGauge().DataPoints {
							attrs := ownerWireAttributes(t, p.Attributes)
							if len(attrs) != 1 || p.GetAsInt() != want[attrs["error_type"]] {
								t.Error("actual owner error count differs")
							}
						}
					case "possums.intake.http.requests":
						for _, p := range m.GetGauge().DataPoints {
							attrs := ownerWireAttributes(t, p.Attributes)
							wantCount := int64(0)
							if attrs["status_class"] == "2xx" || attrs["status_class"] == "4xx" {
								wantCount = 1
							}
							if p.GetAsInt() != wantCount {
								t.Error("native traffic expanded custom terminal population")
							}
						}
					case "possums.intake.http.latency.bucket":
						var sum int64
						for _, p := range m.GetGauge().DataPoints {
							sum += p.GetAsInt()
						}
						if sum != 2 {
							t.Error("latency population changed")
						}
					default:
						t.Error("unexpected owner instrument")
					}
				}
				r.ObserveFailure(failuretype.MaintenanceUnknown)
				if attempts.Load() != 1 {
					t.Error("closed owner observation initiated export")
				}
			})
		})
	}
}
