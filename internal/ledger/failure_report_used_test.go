package ledger

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"possums-intake/internal/failuretype"
)

func TestFailureReportWasUsedDistinguishesPreRunValidation(t *testing.T) {
	s, _, _ := testService(t)
	if _, err := fixtureAccount(s, background, "aaaaaaaaaaaaaaa"); err != nil {
		t.Fatal("fixture creation unavailable")
	}
	ctx, report := WithFailureReport(background)
	if report.WasUsed() {
		t.Fatal("fresh report marked used")
	}
	_, err := s.Balance(ctx, "")
	codeIs(t, err, CodeInput)
	if report.WasUsed() {
		t.Fatal("pre-run validation marked used")
	}
	var absent *FailureReport
	if absent.WasUsed() {
		t.Fatal("nil report marked used")
	}
	ctx, report = WithFailureReport(background)
	_, err = s.Balance(ctx, "aaaaaaaaaaaaaaa")
	if err != nil || !report.WasUsed() {
		t.Fatal("completed run did not mark report")
	}
	takeKinds(t, report)
	if !report.WasUsed() {
		t.Fatal("consumption erased use evidence")
	}
	encoded, err := json.Marshal(report)
	if err != nil || string(encoded) != "{}" {
		t.Fatal("private report metadata reached JSON")
	}
}

type beforeGateContext struct {
	context.Context
	report  *FailureReport
	t       *testing.T
	checked chan struct{}
}

func (c beforeGateContext) Deadline() (deadlineTime time.Time, ok bool) {
	if !c.report.WasUsed() {
		c.t.Error("report not marked before writer wait setup")
	}
	close(c.checked)
	return c.Context.Deadline()
}

func TestFailureReportWasUsedIsLockFreeBeforeGate(t *testing.T) {
	s, _, _ := testService(t)
	base, cancel := context.WithCancel(background)
	cancel()
	ctx, report := WithFailureReport(base)
	checked := make(chan struct{})
	ctx = beforeGateContext{Context: ctx, report: report, t: t, checked: checked}
	// Publication stays blocked, but the used flag must not block admission or
	// actual transaction completion. This preserves the existing mutex test.
	report.mu.Lock()
	// A cancelled call does not enter a callback; use the existing gate-release
	// publication test for actual transaction completion with this mutex held.
	done := make(chan error, 1)
	go func() { done <- s.run(ctx, func(*transaction) error { return nil }, false) }()
	// Deadline() is called after mark-used, before publication. Let the worker
	// reach it without needing the report mutex.
	select {
	case <-checked:
		report.mu.Unlock()
	case <-time.After(2 * time.Second):
		report.mu.Unlock()
		t.Fatal("used marking blocked on publication mutex")
	}
	codeIs(t, <-done, CodeWriter)
	takeKinds(t, report, failuretype.Cancelled)
}
