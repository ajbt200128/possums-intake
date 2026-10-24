package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"possums-intake/internal/ledger"
)

func TestVolumeStartupExchangeExactTemplates(t *testing.T) {
	const guidance = " Underlying cause unknown. Readiness is unconfirmed; volume effects and cleanup outcomes are unknown. Preserve the original disk/key and primary diagnostic; inspect platform state privately. Do not retry the volume operation, initialize, reset or restore state."
	for _, tc := range []struct {
		name, stage, sourceConstraint string
		code                          ledger.Code
		constraint, observed          string
	}{
		{"connect", "volume_connect", "connect_cause_unknown", "LEDGER_VOLUME_CONNECT_UNAVAILABLE", "completed_volume_control_connection", "The local volume control connection did not complete."},
		{"bound_refused", "volume_exchange", "finite_deadline_required", "LEDGER_VOLUME_EXCHANGE_BOUND_UNAVAILABLE", "finite_exchange_deadline_configured", "The local bounded exchange deadline was refused or could not be configured."},
		{"deadline_setup", "volume_exchange", "deadline_setup_cause_unknown", "LEDGER_VOLUME_EXCHANGE_BOUND_UNAVAILABLE", "finite_exchange_deadline_configured", "The local bounded exchange deadline was refused or could not be configured."},
		{"send", "volume_send", "single_complete_packet_cause_unknown", "LEDGER_VOLUME_SEND_UNCONFIRMED", "single_complete_volume_packet", "A single complete control packet write was not confirmed."},
		{"receive", "volume_response", "bounded_complete_response_cause_unknown", "LEDGER_VOLUME_RESPONSE_UNAVAILABLE", "bounded_complete_volume_response", "A complete bounded control response was not received; receive failure, truncation or excess length was observed."},
		{"platform_failed", "volume_response", "platform_operation_failed_cause_unknown", "LEDGER_VOLUME_PLATFORM_FAILED", "successful_platform_volume_operation", "The control endpoint returned the exact failed response; the platform reported an operation failure. This does not establish a wrong disk key."},
		{"unrecognized", "volume_response", "exact_response_required_cause_unknown", "LEDGER_VOLUME_RESPONSE_UNRECOGNIZED", "recognized_exact_volume_response", "The control endpoint returned a response that did not match an accepted exact protocol response."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &ledger.Diagnostic{Code: codeVolumeExchange, Stage: tc.stage, Constraint: tc.sourceConstraint, Outcome: startupSentinel, NextStep: startupSentinel}
			r := startupRecord{1, "failed", phaseVolumePrepare, startupFailureCode(phaseVolumePrepare, source)}
			if r.Code != tc.code {
				t.Fatal("VOLUME_STARTUP_EXACT_SOURCE_TUPLE")
			}
			want := &ledger.Diagnostic{Code: tc.code, Stage: "volume_prepare", Constraint: tc.constraint, Outcome: "not_ready", NextStep: tc.observed + guidance}
			checkVolumeStartupWire(t, r, want)
			if (&ledger.Diagnostic{Code: tc.code}).Kind().Valid() {
				t.Fatal("VOLUME_STARTUP_CODE_BECAME_METRIC_CATEGORY")
			}
			// Projected codes are accepted only in records, never as source diagnostics.
			for _, phase := range []startupPhase{phaseValidation, phaseVolumePrepare, phaseScratch, phaseInit, phaseOpen, phaseHandler, phaseWebsite, phaseBind} {
				if startupFailureCode(phase, &ledger.Diagnostic{Code: tc.code, Stage: tc.stage, Constraint: tc.sourceConstraint}) != startupUnknown(phase) {
					t.Fatal("VOLUME_STARTUP_PROJECTED_CODE_ACCEPTED_AS_SOURCE")
				}
				if phase == phaseVolumePrepare {
					continue
				}
				if startupCodeAllowed(phase, tc.code) || validStartupRecord(startupRecord{1, "failed", phase, tc.code}) || startupFailureCode(phase, source) != startupUnknown(phase) {
					t.Fatal("VOLUME_STARTUP_WRONG_PHASE_ACCEPTED")
				}
			}
		})
	}
}

func checkVolumeStartupWire(t *testing.T, r startupRecord, want *ledger.Diagnostic) {
	t.Helper()
	parent := t.TempDir()
	if writeStartupRecord(parent, r) != nil {
		t.Fatal("VOLUME_STARTUP_RECORD_WRITE")
	}
	wire, err := os.ReadFile(filepath.Join(parent, startupDirectory, startupFilename))
	var fields map[string]json.RawMessage
	canonical, marshalErr := json.Marshal(r)
	got, valid := readStartupRecord(parent)
	if err != nil || marshalErr != nil || !valid || got != r || !bytes.Equal(wire, canonical) || len(wire) > 512 || json.Unmarshal(wire, &fields) != nil || len(fields) != 4 || bytes.Contains(wire, []byte(startupSentinel)) {
		t.Fatal("VOLUME_STARTUP_PRIVATE_FOUR_FIELD_RECORD")
	}
	view := startupHealthDiagnostic(got)
	wire, err = json.Marshal(view)
	expected, marshalErr := json.Marshal(want)
	fields = nil
	if err != nil || marshalErr != nil || !bytes.Equal(wire, expected) || !view.Kind().Valid() || bytes.Contains(wire, []byte(startupSentinel)) || json.Unmarshal(wire, &fields) != nil || len(fields) != 5 {
		t.Fatal("VOLUME_STARTUP_EXACT_FIVE_FIELD_HEALTH")
	}
}

func TestVolumeStartupExchangeUnknownBoundaries(t *testing.T) {
	for _, tc := range []struct {
		stage, constraint string
	}{
		{"volume_exchange", "context_unavailable"},
		{"volume_response", "context_unavailable"},
		{"volume_prepare", "context_unavailable"},
		{"", ""},
		{startupSentinel, "connect_cause_unknown"},
		{"volume_connect", startupSentinel},
		{startupSentinel, startupSentinel},
		{"volume_response", "connect_cause_unknown"},
		{"volume_send", "platform_operation_failed_cause_unknown"},
		{"volume_connect suffix", "connect_cause_unknown"},
		{"volume_connect", "connect_cause_unknown suffix"},
	} {
		source := &ledger.Diagnostic{Code: codeVolumeExchange, Stage: tc.stage, Constraint: tc.constraint, Outcome: startupSentinel, NextStep: startupSentinel}
		r := startupRecord{1, "failed", phaseVolumePrepare, startupFailureCode(phaseVolumePrepare, source)}
		if r.Code != codeVolumeExchange {
			t.Fatal("VOLUME_STARTUP_UNKNOWN_BOUNDARY_REFINED")
		}
		checkVolumeStartupWire(t, r, &ledger.Diagnostic{Code: codeVolumeExchange, Stage: "volume_prepare", Constraint: "completed_volume_control_exchange", Outcome: "not_ready", NextStep: "The bounded control exchange did not complete as required; cause unknown. Inspect the primary diagnostic and platform state privately. Readiness is unconfirmed; volume operation and cleanup outcomes may be unknown. Preserve the original disk/key and primary diagnostic; do not retry initialization, reset or restore state."})
	}
	for _, source := range []*ledger.Diagnostic{
		{Code: ledger.Code(startupSentinel), Stage: "volume_connect", Constraint: "connect_cause_unknown", NextStep: startupSentinel},
		{Code: codeVolumeMount, Stage: "volume_connect", Constraint: "connect_cause_unknown", NextStep: startupSentinel},
		{Code: codeVolumeRejected, Stage: "volume_response", Constraint: "platform_operation_failed_cause_unknown", NextStep: startupSentinel},
	} {
		code := startupFailureCode(phaseVolumePrepare, source)
		wantCode := source.Code
		if source.Code == ledger.Code(startupSentinel) {
			wantCode = codeVolumeUnknown
		}
		if code != wantCode {
			t.Fatal("VOLUME_STARTUP_WRONG_SOURCE_CODE_REFINED")
		}
		want := startupHealthDiagnostic(startupRecord{1, "failed", phaseVolumePrepare, wantCode})
		checkVolumeStartupWire(t, startupRecord{1, "failed", phaseVolumePrepare, code}, want)
	}
}

func TestVolumeStartupExistingTemplatesUnchanged(t *testing.T) {
	const guidance = " Readiness is unconfirmed; volume operation and cleanup outcomes may be unknown. Preserve the original disk/key and primary diagnostic; do not retry initialization, reset or restore state."
	for _, tc := range []struct {
		code             ledger.Code
		constraint, next string
	}{
		{codeVolumeRejected, "accepted_volume_control_response", "The control endpoint rejected the operation; the rejection reason is unknown. Inspect platform state privately."},
		{codeVolumeCancelled, "operation_cancelled", "Volume preparation observed cancellation before readiness. Inspect the primary diagnostic and platform state privately."},
		{codeVolumeDeadline, "operation_deadline_exceeded", "Volume preparation exceeded its deadline before readiness. Inspect the primary diagnostic and platform state privately."},
		{codeVolumeMount, "verified_dedicated_writable_mount", "The dedicated writable volume mount was not verified; cause unknown. Inspect the original mount and state path privately."},
	} {
		// Even an otherwise refinable boundary cannot override another code.
		source := &ledger.Diagnostic{Code: tc.code, Stage: "volume_response", Constraint: "platform_operation_failed_cause_unknown", NextStep: startupSentinel}
		r := startupRecord{1, "failed", phaseVolumePrepare, startupFailureCode(phaseVolumePrepare, source)}
		checkVolumeStartupWire(t, r, &ledger.Diagnostic{Code: tc.code, Stage: "volume_prepare", Constraint: tc.constraint, Outcome: "not_ready", NextStep: tc.next + guidance})
	}
	checkVolumeStartupWire(t, startupRecord{1, "bound", phaseBind, ""}, &ledger.Diagnostic{Code: "SYNTHETIC_HTTP_NOT_READY", Stage: "listener_bind", Constraint: "confirmed_http_readiness", Outcome: "not_ready", NextStep: "The listener was bound, but HTTP readiness is unconfirmed; cause unknown. Inspect the synthetic container privately."})
}
