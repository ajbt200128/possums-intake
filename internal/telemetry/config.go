// Package telemetry releases only complete, bounded aggregate metrics. It never
// accepts account data, request attributes, raw errors, or accounting callbacks.
package telemetry

import (
	"os"
	"regexp"
	"strings"
)

const honeycombEndpoint = "https://api.honeycomb.io/v1/metrics"

// State is a closed, content-free startup category. It is not a metric label.
type State string

const (
	Enabled        State = "telemetry_enabled"
	Disabled       State = "telemetry_disabled"
	InvalidConfig  State = "telemetry_config_invalid"
	SDKUnavailable State = "telemetry_sdk_unavailable"
)

type config struct{ environment, slot, key string }

var slotPattern = regexp.MustCompile(`^intake-[0-9]{2}$`)
var keyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

// Runtime configuration is deliberately separate from generic OTel configuration.
// Inspect names only; never retain or report rejected environment values.
func loadConfig(environ []string, get func(string) string) (config, State) {
	off := get("OTEL_SDK_DISABLED")
	if strings.EqualFold(off, "true") {
		return config{}, Disabled
	}
	if off != "" && off != "false" {
		return config{}, InvalidConfig
	}
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "OTEL_") && name != "OTEL_SDK_DISABLED" {
			return config{}, InvalidConfig
		}
	}
	endpoint := get("INTAKE_METRICS_ENDPOINT")
	c := config{environment: get("INTAKE_METRICS_ENVIRONMENT"), slot: get("INTAKE_METRICS_SLOT"), key: get("INTAKE_METRICS_HONEYCOMB_API_KEY")}
	if endpoint == "" && c.environment == "" && c.slot == "" && c.key == "" {
		return config{}, Disabled
	}
	if endpoint != honeycombEndpoint || (c.environment != "development" && c.environment != "production") || !slotPattern.MatchString(c.slot) || !keyPattern.MatchString(c.key) {
		return config{}, InvalidConfig
	}
	return c, Enabled
}

func runtimeConfig() (config, State) { return loadConfig(os.Environ(), os.Getenv) }
