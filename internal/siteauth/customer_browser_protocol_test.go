package siteauth_test

import (
	"strings"
	"testing"
)

// Only this exact authored child protocol can select a reported stage. Native
// Node/OS/Playwright errors, values, or extra output never cross this boundary.
func customerBrowserStage(output []byte) string {
	stages := map[string]string{
		"fixture_inputs":                "public_fixture_origin_and_pinned_spki",
		"browser_launch":                "installed_chromium_launch",
		"browser_context":               "isolated_browser_context_navigation",
		"persistence":                   "page_memory_only_storage",
		"route_policy_external":         "fixture_origin_only",
		"route_policy_page_errors":      "no_page_exceptions",
		"route_policy_console_errors":   "no_unexpected_console_errors",
		"route_policy_console_warnings": "no_console_warnings",
		"route_policy_refusal_errors":   "matched_expected_resource_errors",
		"route_policy_request_failures": "no_fixture_request_failure",
		"route_policy_http_errors":      "no_unexpected_http_failure",
	}
	for _, stage := range []string{"stock_login", "collection_open", "editor_fill", "create_submit", "create_response", "create_listed", "mismatch_fill", "mismatch_submit", "missing_fill", "missing_submit", "editor_close", "refusals_saved_nothing", "done"} {
		stages[stage] = "stock_native_editor_flow"
	}
	for stage, constraint := range stages {
		line := "CUSTOMER_NATIVE_EDITOR_UNAVAILABLE stage=" + stage + " constraint=" + constraint + " cause=unknown; inspect the isolated synthetic fixture privately; no automatic retry and no acceptance claimed\n"
		if string(output) == line {
			return stage
		}
	}
	return "stock_editor"
}

func TestCustomerBrowserProtocolIsClosed(t *testing.T) {
	line := "CUSTOMER_NATIVE_EDITOR_UNAVAILABLE stage=create_submit constraint=stock_native_editor_flow cause=unknown; inspect the isolated synthetic fixture privately; no automatic retry and no acceptance claimed\n"
	if customerBrowserStage([]byte(line)) != "create_submit" {
		t.Fatal("known browser failure stage lost")
	}
	for _, hostile := range []string{line + "SYNTHETIC_SECRET_SENTINEL", strings.ReplaceAll(line, "create_submit", "SYNTHETIC_SECRET_SENTINEL"), strings.ReplaceAll(line, "stock_native_editor_flow", "SYNTHETIC_SECRET_SENTINEL"), "SYNTHETIC_SECRET_SENTINEL"} {
		if customerBrowserStage([]byte(hostile)) != "stock_editor" {
			t.Fatal("hostile child output classified or retained")
		}
	}
}
