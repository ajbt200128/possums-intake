package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

func TestManagedStartupDiagnosticsSelectionAndParsedScope(t *testing.T) {
	for _, tc := range []struct {
		name, managed, legacy string
		args                  []string
		selected, armed       bool
	}{
		{"exact_managed", "true", "", []string{"serve", "--state=/synthetic/ledger", "--tinfoil-volume=ledger"}, true, true},
		{"separate_value", "true", "", []string{"serve", "--state", "/synthetic/ledger", "--tinfoil-volume", "ledger"}, true, true},
		{"listener_validation", "true", "", []string{"serve", "--state=/synthetic/ledger", "--tinfoil-volume=ledger", "--listen=invalid"}, true, true},
		{"explicit_false_init", "true", "", []string{"serve", "--state=/synthetic/ledger", "--tinfoil-volume=ledger", "--initialize-volume=false"}, true, true},
		{"off", "", "", []string{"serve", "--state=/synthetic/ledger", "--tinfoil-volume=ledger"}, false, false},
		{"uppercase", "TRUE", "", []string{"serve", "--state=/synthetic/ledger", "--tinfoil-volume=ledger"}, false, false},
		{"numeric", "1", "", []string{"serve", "--state=/synthetic/ledger", "--tinfoil-volume=ledger"}, false, false},
		{"whitespace", "true ", "", []string{"serve", "--state=/synthetic/ledger", "--tinfoil-volume=ledger"}, false, false},
		{"legacy_not_managed", "", "true", []string{"serve", "--state=/synthetic/ledger", "--tinfoil-volume=ledger"}, false, false},
		{"default_mode", "true", "true", []string{"--state=/synthetic/ledger", "--tinfoil-volume=ledger"}, false, false},
		{"no_args", "true", "true", nil, false, false},
		{"init", "true", "true", []string{"init", "--state=/synthetic/ledger", "--tinfoil-volume=ledger", "--listen=127.0.0.1:8090"}, false, false},
		{"init_volume", "true", "true", []string{"init", "--state=/synthetic/ledger", "--tinfoil-volume=ledger", "--initialize-volume", "--listen=127.0.0.1:8090"}, false, false},
		{"unmanaged", "true", "", []string{"serve", "--state=/synthetic/ledger"}, true, false},
		{"empty_volume", "true", "", []string{"serve", "--state=/synthetic/ledger", "--tinfoil-volume="}, true, false},
		{"last_parsed_value_empty", "true", "", []string{"serve", "--state=/synthetic/ledger", "--tinfoil-volume=ledger", "--tinfoil-volume="}, true, false},
		{"value_not_option", "true", "", []string{"serve", "--state=/synthetic/ledger", "--listen", "--tinfoil-volume=ledger"}, true, false},
		{"parse_failure", "true", "", []string{"serve", "--state=/synthetic/ledger", "--tinfoil-volume=ledger", "--unknown"}, true, false},
		{"missing_value", "true", "", []string{"serve", "--state=/synthetic/ledger", "--tinfoil-volume"}, true, false},
		{"positional", "true", "", []string{"serve", "--state=/synthetic/ledger", "--tinfoil-volume=ledger", "unexpected"}, true, false},
		{"relative_state", "true", "", []string{"serve", "--state=relative", "--tinfoil-volume=ledger"}, true, false},
		{"serve_cannot_init", "true", "", []string{"serve", "--state=/synthetic/ledger", "--tinfoil-volume=ledger", "--initialize-volume"}, true, false},
		{"managed_not_synthetic", "true", "", []string{"synthetic-serve", "--listen=invalid"}, false, false},
		{"legacy_synthetic", "", "true", []string{"synthetic-serve", "--listen=invalid"}, true, true},
		{"legacy_exact_only", "true", "TRUE", []string{"synthetic-serve", "--listen=invalid"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LEDGER_MANAGED_STARTUP_DIAGNOSTICS", tc.managed)
			t.Setenv("LEDGER_SYNTHETIC_STARTUP_DIAGNOSTICS", tc.legacy)
			t.Setenv("LEDGER_ADMIN_KEY", "")
			t.Setenv(volumeKeyEnv, "")
			if os.Unsetenv(volumeKeyEnv) != nil {
				t.Fatal("MANAGED_DIAGNOSTIC_ENV_FIXTURE")
			}
			r := startupRecorderForArgs(tc.args)
			if (r != nil) != tc.selected {
				t.Fatal("MANAGED_DIAGNOSTIC_SELECTION")
			}
			var records []startupRecord
			var stderr bytes.Buffer
			timers := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if r != nil {
				r.stderr = &stderr
				r.write = func(record startupRecord) *ledger.Diagnostic {
					records = append(records, record)
					return nil
				}
				r.timer = func(d time.Duration) *time.Timer {
					timers++
					if d != startupHold {
						t.Error("MANAGED_DIAGNOSTIC_HOLD_BOUND")
					}
					cancel()
					return time.NewTimer(startupHold)
				}
			}
			// Every fixture stops at validation, before state or volume work.
			err := executeRecorded(ctx, tc.args, nil, r)
			if err == nil || stderr.Len() != 0 {
				t.Fatal("MANAGED_DIAGNOSTIC_VALIDATION_BOUNDARY")
			}
			if tc.armed {
				if len(records) != 2 || timers != 1 || records[0] != (startupRecord{1, "starting", phaseValidation, ""}) || records[1].State != "failed" || r.pendingManaged {
					t.Fatal("MANAGED_DIAGNOSTIC_PARSED_ARMING")
				}
			} else if len(records) != 0 || timers != 0 {
				t.Fatal("MANAGED_DIAGNOSTIC_INACTIVE_SIDE_EFFECT")
			}
		})
	}
}

func TestManagedStartupDiagnosticsInvalidKeyPrebindAndOwnership(t *testing.T) {
	for _, recordFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "recorded", true: "record_unavailable"}[recordFailure], func(t *testing.T) {
			t.Setenv("LEDGER_MANAGED_STARTUP_DIAGNOSTICS", "true")
			t.Setenv("LEDGER_SYNTHETIC_STARTUP_DIAGNOSTICS", "")
			t.Setenv("LEDGER_ADMIN_KEY", syntheticAdminKey)
			t.Setenv(volumeKeyEnv, startupSentinel) // Fails decoding before any socket or state access.
			args := []string{"serve", "--state=/synthetic/ledger", "--tinfoil-volume=ledger"}
			r := startupRecorderForArgs(args)
			var records []startupRecord
			var stderr bytes.Buffer
			r.stderr = &stderr
			r.write = func(record startupRecord) *ledger.Diagnostic {
				records = append(records, record)
				if recordFailure {
					return &ledger.Diagnostic{Constraint: startupSentinel, NextStep: startupSentinel}
				}
				return nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			timers := 0
			r.timer = func(d time.Duration) *time.Timer {
				timers++
				if d != 120*time.Second || r.bound || r.phase != phaseVolumePrepare {
					t.Error("MANAGED_DIAGNOSTIC_PREBIND_HOLD")
				}
				if _, present := os.LookupEnv(volumeKeyEnv); present {
					t.Error("MANAGED_DIAGNOSTIC_HOLD_BEFORE_VOLUME_RETURN")
				}
				cancel()
				return time.NewTimer(startupHold)
			}
			o := &runtimeObserver{}
			err := executeRecorded(ctx, args, o, r)
			if err == nil || ledger.Failure(err).Code != codeVolumeKey || timers != 1 || r.pendingManaged {
				t.Fatal("MANAGED_DIAGNOSTIC_PRIMARY_KEY_FAILURE")
			}
			if recordFailure {
				if len(records) != 1 || !r.failed {
					t.Fatal("MANAGED_DIAGNOSTIC_RECORD_RETRY")
				}
				checkStartupRecordingReport(t, stderr.Bytes(), "recording_boundary")
				o.want(t, failuretype.RuntimeSetupUnknown, failuretype.RequestInvalid)
			} else {
				want := []startupRecord{{1, "starting", phaseValidation, ""}, {1, "starting", phaseVolumePrepare, ""}, {1, "failed", phaseVolumePrepare, codeVolumeKey}}
				if len(records) != len(want) || stderr.Len() != 0 {
					t.Fatal("MANAGED_DIAGNOSTIC_VOLUME_RECORDS")
				}
				for i := range want {
					if records[i] != want[i] {
						t.Fatal("MANAGED_DIAGNOSTIC_VOLUME_PHASE")
					}
				}
				o.want(t, failuretype.RequestInvalid)
			}
			wire, _ := json.Marshal(ledger.Failure(err))
			if bytes.Contains(wire, []byte(startupSentinel)) {
				t.Fatal("MANAGED_DIAGNOSTIC_PRIMARY_PRIVATE_INPUT")
			}
		})
	}
}

func TestManagedStartupVolumeCatalogAndStrictSchema(t *testing.T) {
	codes := []ledger.Code{codeVolumeInput, codeVolumeKey, codeVolumeRejected, codeVolumeCancelled,
		codeVolumeDeadline, codeVolumeSocket, codeVolumeExchange, codeVolumeMount,
		codeVolumeCleanup, codeVolumeUnknown, codeVolumePlatform}
	parent := t.TempDir()
	for _, code := range codes {
		cause := &ledger.Diagnostic{Code: code, Stage: startupSentinel, Constraint: startupSentinel, Outcome: startupSentinel, NextStep: startupSentinel}
		r := startupRecord{1, "failed", phaseVolumePrepare, startupFailureCode(phaseVolumePrepare, cause)}
		if r.Code != code || writeStartupRecord(parent, r) != nil {
			t.Fatal("MANAGED_DIAGNOSTIC_VOLUME_CATALOG")
		}
		got, ok := readStartupRecord(parent)
		if !ok || got != r {
			t.Fatal("MANAGED_DIAGNOSTIC_VOLUME_ROUNDTRIP")
		}
		wire, _ := json.Marshal(r)
		var fields map[string]json.RawMessage
		if json.Unmarshal(wire, &fields) != nil || len(fields) != 4 || fields["version"] == nil || fields["state"] == nil || fields["phase"] == nil || fields["code"] == nil || len(wire) > startupMaxBytes || bytes.Contains(wire, []byte(startupSentinel)) {
			t.Fatal("MANAGED_DIAGNOSTIC_FIXED_RECORD_SCHEMA")
		}
		d := startupHealthDiagnostic(r)
		wire, _ = json.Marshal(d)
		if d.Code != code || d.Stage != string(phaseVolumePrepare) || d.Outcome != "not_ready" || !d.Kind().Valid() || bytes.Contains(wire, []byte(startupSentinel)) || !strings.Contains(d.NextStep, "volume operation and cleanup outcomes may be unknown") || !strings.Contains(d.NextStep, "Preserve the original disk/key") || !strings.Contains(d.NextStep, "do not retry initialization, reset or restore") {
			t.Fatal("MANAGED_DIAGNOSTIC_VOLUME_READINESS_PROJECTION")
		}
		if code == codeVolumeKey && d.Constraint != "canonical_standard_base64_32_bytes" || code == codeVolumeRejected && !strings.Contains(d.NextStep, "rejection reason is unknown") {
			t.Fatal("MANAGED_DIAGNOSTIC_VOLUME_CONSTRAINT_EXPLANATION")
		}
		for _, phase := range []startupPhase{phaseValidation, phaseScratch, phaseInit, phaseOpen, phaseHandler, phaseWebsite, phaseBind} {
			if startupCodeAllowed(phase, code) {
				t.Fatal("MANAGED_DIAGNOSTIC_VOLUME_WRONG_PHASE")
			}
		}
	}
	for _, cause := range []error{hostileStartupError{}, errors.New(startupSentinel), (*ledger.Diagnostic)(nil),
		&ledger.Diagnostic{Code: codeVolumeOptions}, &ledger.Diagnostic{Code: codeAdminKey}, &ledger.Diagnostic{Code: ledger.CodeMissing}, &ledger.Diagnostic{Code: ledger.Code(startupSentinel)}} {
		code := startupFailureCode(phaseVolumePrepare, cause)
		d := startupHealthDiagnostic(startupRecord{1, "failed", phaseVolumePrepare, code})
		wire, _ := json.Marshal(d)
		if code != codeVolumeUnknown || !strings.Contains(d.NextStep, "cause unknown") || bytes.Contains(wire, []byte(startupSentinel)) {
			t.Fatal("MANAGED_DIAGNOSTIC_VOLUME_UNKNOWN_SANITIZER")
		}
	}
	for _, r := range []startupRecord{{1, "failed", phaseVolumePrepare, codeVolumeOptions}, {1, "failed", phaseVolumePrepare, codeAdminKey}, {1, "bound", phaseVolumePrepare, ""}} {
		if validStartupRecord(r) || writeStartupRecord(parent, r) == nil {
			t.Fatal("MANAGED_DIAGNOSTIC_VOLUME_SCHEMA_REFUSAL")
		}
	}
}

func TestManagedStartupPendingAndArmedLifecycle(t *testing.T) {
	for _, state := range []string{"pending", "bound", "success", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			writes, timers := 0, 0
			r := &startupRecorder{pendingManaged: true, write: func(startupRecord) *ledger.Diagnostic { writes++; return nil }, timer: func(d time.Duration) *time.Timer {
				timers++
				if d != startupHold {
					t.Error("MANAGED_DIAGNOSTIC_LIFECYCLE_BOUND")
				}
				cancel()
				return time.NewTimer(startupHold)
			}}
			r.checkpoint(phaseValidation)
			err := error(hostileStartupError{})
			if state != "pending" {
				r.pendingManaged = false
			}
			if state == "bound" {
				r.listenerBound()
			} else if state == "success" {
				err = nil
			}
			r.finish(ctx, err)
			want := 0
			if state == "cancelled" {
				want = 1
			}
			if timers != want || state == "pending" && writes != 0 {
				t.Fatal("MANAGED_DIAGNOSTIC_LIFECYCLE_INVARIANT")
			}
		})
	}
}
