package ledger

import (
	"context"
	"sync"
	"testing"

	"possums-intake/internal/failuretype"
)

func takeKinds(t *testing.T, report *FailureReport, want ...failuretype.Type) {
	t.Helper()
	kinds, n, valid := report.Take()
	if !valid || n != len(want) {
		t.Fatal("numeric report unavailable or wrong count")
	}
	for i, kind := range want {
		if kinds[i] != kind {
			t.Fatalf("classification slot %d: got %d; want %d", i, kinds[i], kind)
		}
	}
	for _, kind := range kinds[n:] {
		if kind != 0 {
			t.Fatal("unused report slot was populated")
		}
	}
}

func TestFailureReportFixedSlotsAndIntegrity(t *testing.T) {
	_, report := WithFailureReport(background)
	var batch transactionFailures
	// Equal categories can be distinct failures, not deduplicated labels.
	for range 3 {
		batch.add(failuretype.TransactionAfterCompletionUnknown)
	}
	report.publish(batch)
	takeKinds(t, report, failuretype.TransactionAfterCompletionUnknown, failuretype.TransactionAfterCompletionUnknown, failuretype.TransactionAfterCompletionUnknown)
	if kinds, n, valid := report.Take(); valid || n != 0 || kinds != ([3]failuretype.Type{}) {
		t.Fatal("duplicate consume supplied observations")
	}
	for _, invalid := range []failuretype.Type{0, 40, 255} {
		_, report := WithFailureReport(background)
		var batch transactionFailures
		batch.add(failuretype.Cancelled)
		batch.add(invalid)
		report.publish(batch)
		if kinds, n, valid := report.Take(); valid || n != 0 || kinds != ([3]failuretype.Type{}) {
			t.Fatal("invalid enum supplied partial or healthy evidence")
		}
	}
	_, report = WithFailureReport(background)
	batch.add(failuretype.Cancelled)
	if batch.count != 3 || batch.kinds[0] != failuretype.TransactionAfterCompletionUnknown {
		t.Fatal("overflow silently overwrote a slot")
	}
	report.publish(batch)
	if _, _, valid := report.Take(); valid {
		t.Fatal("overflow was not invalidated")
	}
	_, report = WithFailureReport(background)
	report.publish(transactionFailures{})
	report.publish(transactionFailures{})
	if _, _, valid := report.Take(); valid {
		t.Fatal("context reuse silently overwrote published evidence")
	}
	_, report = WithFailureReport(background)
	if _, _, valid := report.Take(); valid {
		t.Fatal("premature consume established healthy zeros")
	}
	report.publish(transactionFailures{})
	if _, _, valid := report.Take(); valid {
		t.Fatal("publication repaired premature consume")
	}
	var absent *FailureReport
	if _, _, valid := absent.Take(); valid {
		t.Fatal("absent report was healthy evidence")
	}
}

func TestFailureReportConcurrentConsumeAndPublish(t *testing.T) {
	for range 100 {
		_, report := WithFailureReport(background)
		var batch transactionFailures
		batch.add(failuretype.SQLiteBusy)
		var wg sync.WaitGroup
		wg.Add(9)
		start := make(chan struct{})
		results := make(chan bool, 8)
		go func() { defer wg.Done(); <-start; report.publish(batch) }()
		for range 8 {
			go func() {
				defer wg.Done()
				<-start
				kinds, n, valid := report.Take()
				if valid && (n != 1 || kinds[0] != failuretype.SQLiteBusy) {
					t.Error("concurrent consume corrupted numeric evidence")
				}
				results <- valid
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		validCount := 0
		for valid := range results {
			if valid {
				validCount++
			}
		}
		if validCount > 1 {
			t.Fatal("concurrent consumers recounted observations")
		}
	}
}

type hostileReportContext struct {
	context.Context
	value      any
	panicValue bool
}

func (c hostileReportContext) Value(key any) any {
	if _, ok := key.(failureReportKey); ok {
		if c.panicValue {
			panic("SYNTHETIC_CONTEXT_VALUE")
		}
		return c.value
	}
	return c.Context.Value(key)
}

type hostileReportInterface struct{}

func (hostileReportInterface) Take() ([3]failuretype.Type, int, bool) {
	panic("SYNTHETIC_REPORT_INTERFACE")
}

func TestFailureReportHostileContextCannotAffectAccounting(t *testing.T) {
	s, _, _ := testService(t)
	for _, value := range []any{hostileReportInterface{}, (*FailureReport)(nil), 1} {
		ctx := hostileReportContext{Context: background, value: value}
		err := s.run(ctx, func(*transaction) error { return diagnostic(CodeInput) }, false)
		codeIs(t, err, CodeInput)
		if Failure(err).Kind() != failuretype.RequestInvalid || !s.Ready() {
			t.Fatal("report misuse affected classification or readiness")
		}
	}
	ctx, report := WithFailureReport(background)
	ctx = hostileReportContext{Context: ctx, panicValue: true}
	err := s.run(ctx, func(*transaction) error { return diagnostic(CodeInput) }, false)
	codeIs(t, err, CodeInput)
	if _, _, valid := report.Take(); valid {
		t.Fatal("hidden report established observation completeness")
	}
}

func TestFailureReportPublishedOnlyAfterTransactionAndGateRelease(t *testing.T) {
	s, _, _ := testService(t)
	ctx, report := WithFailureReport(background)
	report.mu.Lock() // data-only observer; no SDK/telemetry callback
	done := make(chan error, 1)
	completed := make(chan struct{})
	original := s.app
	s.app = completionSignalApp{App: original, completed: completed}
	go func() {
		done <- s.run(ctx, func(*transaction) error { return diagnostic(CodeInput) }, false)
	}()
	<-completed // actual supported transaction has finished
	// run may not have reached gate release yet. Acquiring the gate establishes
	// that it did; publication still cannot finish while its data mutex is held.
	s.gate <- struct{}{}
	if report.published {
		t.Fatal("numeric report published while gate was owned")
	}
	<-s.gate
	report.mu.Unlock()
	codeIs(t, <-done, CodeInput)
	takeKinds(t, report, failuretype.RequestInvalid)
	s.app = original
}
