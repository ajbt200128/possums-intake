package main

import (
	"testing"

	"possums-intake/internal/ledger"
)

func TestFixtureInitFailureCodeIsClosed(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want ledger.Code
	}{
		{`{"code":"LEDGER_WORK_DEADLINE","next_step":"SYNTHETIC_PRIVATE_SENTINEL"}`, "SYNTHETIC_INIT_COMPLETION_UNKNOWN"},
		{`{"code":"SYNTHETIC_PRIVATE_SENTINEL"}`, "SYNTHETIC_INIT_COMPLETION_UNKNOWN"},
		{`SYNTHETIC_PRIVATE_SENTINEL`, "SYNTHETIC_INIT_COMPLETION_UNKNOWN"},
		{`{"code":"SITE_CUSTOMER_SCHEMA_UNAVAILABLE","stage":"SYNTHETIC_PRIVATE_SENTINEL"}`, "SITE_CUSTOMER_SCHEMA_UNAVAILABLE"},
		{`{"code":"SITE_BUNDLE_UNAVAILABLE","outcome":"SYNTHETIC_PRIVATE_SENTINEL"}`, "SITE_BUNDLE_UNAVAILABLE"},
	} {
		if fixtureInitFailureCode(tc.raw) != tc.want {
			t.Fatal("FIXTURE_INIT_DIAGNOSTIC_ALLOWLIST")
		}
	}
}
