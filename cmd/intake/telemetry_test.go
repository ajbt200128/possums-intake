package main

import (
	"os"
	"strings"
	"testing"
)

// Direct lifecycle tests must not export even under inherited credentials.
func TestMain(m *testing.M) {
	_ = os.Setenv("OTEL_SDK_DISABLED", "true")
	os.Exit(m.Run())
}

func TestHelperEnvironmentCannotExport(t *testing.T) {
	for _, name := range []string{"INTAKE_METRICS_ENDPOINT", "INTAKE_METRICS_HONEYCOMB_API_KEY", "INTAKE_METRICS_ENVIRONMENT", "INTAKE_METRICS_SLOT", "OTEL_RESOURCE_ATTRIBUTES", "OTEL_EXPORTER_OTLP_HEADERS"} {
		t.Setenv(name, "SYNTHETIC_PRIVATE_SENTINEL")
	}
	t.Setenv("OTEL_SDK_DISABLED", "false")
	off := 0
	for _, value := range helperEnv(syntheticAdminKey) {
		if value == "OTEL_SDK_DISABLED=true" {
			off++
			continue
		}
		if strings.HasPrefix(value, "INTAKE_METRICS_") || strings.HasPrefix(value, "OTEL_") {
			t.Fatal("subprocess inherited telemetry configuration")
		}
	}
	if off != 1 {
		t.Fatal("subprocess missing unconditional off switch")
	}
}
