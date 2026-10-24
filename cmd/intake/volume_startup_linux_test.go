//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"possums-intake/internal/ledger"
)

// Pass the actual exchange helper's Diagnostic through the startup projection.
// No socket, mount, managed secret or real volume is accessed by these fixtures.
func TestVolumeStartupActualExchangeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, contextMode, stage, constraint string
		conn                                 fakeVolumeConn
		sourceCode, projectedCode            ledger.Code
		writes, reads                        int
	}{
		{"no_bound", "none", "volume_exchange", "finite_deadline_required", fakeVolumeConn{writeN: -1}, codeVolumeExchange, "LEDGER_VOLUME_EXCHANGE_BOUND_UNAVAILABLE", 0, 0},
		{"excess_bound", "long", "volume_exchange", "finite_deadline_required", fakeVolumeConn{writeN: -1}, codeVolumeExchange, "LEDGER_VOLUME_EXCHANGE_BOUND_UNAVAILABLE", 0, 0},
		{"deadline_setup", "", "volume_exchange", "deadline_setup_cause_unknown", fakeVolumeConn{writeN: -1, deadlineErr: errors.New(startupSentinel)}, codeVolumeExchange, "LEDGER_VOLUME_EXCHANGE_BOUND_UNAVAILABLE", 0, 0},
		{"short_write", "", "volume_send", "single_complete_packet_cause_unknown", fakeVolumeConn{writeN: 1}, codeVolumeExchange, "LEDGER_VOLUME_SEND_UNCONFIRMED", 1, 0},
		{"write_failure", "", "volume_send", "single_complete_packet_cause_unknown", fakeVolumeConn{writeN: -1, writeErr: errors.New(startupSentinel)}, codeVolumeExchange, "LEDGER_VOLUME_SEND_UNCONFIRMED", 1, 0},
		{"read_failure", "", "volume_response", "bounded_complete_response_cause_unknown", fakeVolumeConn{writeN: -1, readErr: errors.New(startupSentinel)}, codeVolumeExchange, "LEDGER_VOLUME_RESPONSE_UNAVAILABLE", 1, 1},
		{"truncated", "", "volume_response", "bounded_complete_response_cause_unknown", fakeVolumeConn{writeN: -1, response: "ok", flags: unix.MSG_TRUNC}, codeVolumeExchange, "LEDGER_VOLUME_RESPONSE_UNAVAILABLE", 1, 1},
		{"control_truncated", "", "volume_response", "bounded_complete_response_cause_unknown", fakeVolumeConn{writeN: -1, response: "ok", flags: unix.MSG_CTRUNC}, codeVolumeExchange, "LEDGER_VOLUME_RESPONSE_UNAVAILABLE", 1, 1},
		{"excess_response", "", "volume_response", "bounded_complete_response_cause_unknown", fakeVolumeConn{writeN: -1, response: "okHOSTILE"}, codeVolumeExchange, "LEDGER_VOLUME_RESPONSE_UNAVAILABLE", 1, 1},
		{"platform_failed", "", "volume_response", "platform_operation_failed_cause_unknown", fakeVolumeConn{writeN: -1, response: "failed"}, codeVolumeExchange, "LEDGER_VOLUME_PLATFORM_FAILED", 1, 1},
		{"unrecognized", "", "volume_response", "exact_response_required_cause_unknown", fakeVolumeConn{writeN: -1, response: "HOSTILE"}, codeVolumeExchange, "LEDGER_VOLUME_RESPONSE_UNRECOGNIZED", 1, 1},
		{"empty_response", "", "volume_response", "exact_response_required_cause_unknown", fakeVolumeConn{writeN: -1}, codeVolumeExchange, "LEDGER_VOLUME_RESPONSE_UNRECOGNIZED", 1, 1},
		{"rejected", "", "volume_response", "control_request_rejected", fakeVolumeConn{writeN: -1, response: "rejected"}, codeVolumeRejected, codeVolumeRejected, 1, 1},
		{"cancelled", "cancel", "volume_exchange", "operation_cancelled", fakeVolumeConn{writeN: -1}, codeVolumeCancelled, codeVolumeCancelled, 0, 0},
		{"deadline_exceeded", "", "volume_send", "operation_deadline_exceeded", fakeVolumeConn{writeN: -1, writeErr: os.ErrDeadlineExceeded}, codeVolumeDeadline, codeVolumeDeadline, 1, 0},
		{"ok_then_cancelled", "after_read", "volume_response", "operation_cancelled", fakeVolumeConn{writeN: -1, response: "ok"}, codeVolumeCancelled, codeVolumeCancelled, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, closeFailure := range []bool{false, true} {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				if tc.contextMode == "none" {
					cancel()
					ctx, cancel = context.WithCancel(context.Background())
				} else if tc.contextMode == "long" {
					cancel()
					ctx, cancel = context.WithTimeout(context.Background(), 2*volumeTimeout)
				} else if tc.contextMode == "cancel" {
					cancel()
				}
				c := tc.conn
				if tc.contextMode == "after_read" {
					c.afterRead = cancel
				}
				if closeFailure {
					c.closeErr = errors.New(startupSentinel)
				}
				p := &volumeProgress{}
				err := exchangeVolumePacket(ctx, &c, make([]byte, 34), p)
				cancel()
				d, ok := err.(*ledger.Diagnostic)
				if !ok || d.Code != tc.sourceCode || d.Stage != tc.stage || d.Constraint != tc.constraint || c.writes != tc.writes || c.reads != tc.reads || c.closes != 1 {
					t.Fatal("VOLUME_STARTUP_ACTUAL_EXCHANGE_BOUNDARY")
				}
				r := startupRecord{1, "failed", phaseVolumePrepare, startupFailureCode(phaseVolumePrepare, d)}
				if r.Code != tc.projectedCode {
					t.Fatal("VOLUME_STARTUP_ACTUAL_EXCHANGE_PROJECTION")
				}
				wantOutcome := "no_volume_request_sent"
				if tc.writes != 0 {
					wantOutcome = "volume_outcome_unknown"
				}
				if tc.contextMode == "after_read" {
					wantOutcome = "volume_ok_ledger_not_started"
				}
				if d.Outcome != wantOutcome || p.sent != (tc.writes != 0) || p.ok != (tc.contextMode == "after_read") {
					t.Fatal("VOLUME_STARTUP_PRIMARY_PROGRESS_CHANGED")
				}
				wire, marshalErr := json.Marshal(d)
				if marshalErr != nil || bytes.Contains(wire, []byte(startupSentinel)) {
					t.Fatal("VOLUME_STARTUP_ACTUAL_EXCHANGE_PRIVATE_FIELDS")
				}
				checkVolumeStartupWire(t, r, startupHealthDiagnostic(r))
			}
		})
	}
}
