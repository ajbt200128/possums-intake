package ledger

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"possums-intake/internal/failuretype"
)

func TestFailureReportPrimaryTracksFinalRunResult(t *testing.T) {
	for _, fixture := range []struct {
		name       string
		refusal    bool
		hooks      int
		panicAfter bool
	}{
		{"success", false, 0, false},
		{"refusal", true, 0, false},
		{"commit_then_hook_error", false, 1, false},
		{"commit_then_hook_panic", false, 0, true},
		{"refusal_then_hook_error", true, 1, false},
		{"refusal_then_hook_panic", true, 0, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			s, _, _ := testService(t)
			original := s.app
			s.app = completionHooksApp{App: original, count: fixture.hooks, panicAfter: fixture.panicAfter}
			defer func() { s.app = original }()
			ctx, report := WithFailureReport(background)
			err := s.run(ctx, func(*transaction) error {
				if fixture.refusal {
					return diagnostic(CodeAccount)
				}
				return nil
			}, false)
			kinds, n, valid, primary := report.TakeWithPrimary()
			if !report.WasUsed() || !valid || primary != (err != nil) || primary != fixture.refusal {
				t.Fatal("final returned run result and primary role disagree")
			}
			var want []failuretype.Type
			if fixture.refusal {
				want = append(want, failuretype.AccountMissing)
			}
			for range fixture.hooks {
				want = append(want, failuretype.TransactionAfterCompletionUnknown)
			}
			if fixture.panicAfter {
				want = append(want, failuretype.TransactionAfterCompletionUnknown)
			}
			if n != len(want) {
				t.Fatal("role metadata changed occurrence count")
			}
			for i, kind := range want {
				if kinds[i] != kind {
					t.Fatal("role metadata changed observed category")
				}
			}
			if _, _, valid := report.Take(); valid {
				t.Fatal("legacy Take recounted role-aware consumption")
			}
		})
	}
	// An admission error has a primary even without entering a transaction.
	s, _, _ := testService(t)
	s.ready.Store(false)
	ctx, report := WithFailureReport(background)
	err := s.run(ctx, func(*transaction) error { t.Fatal("closed service entered callback"); return nil }, false)
	kinds, n, valid, primary := report.TakeWithPrimary()
	if err == nil || !valid || !primary || n != 1 || kinds[0] != failuretype.NotReady {
		t.Fatal("pre-transaction run primary role lost")
	}
}

func TestFailureReportPrimaryAPICompatibilityAndPrivacy(t *testing.T) {
	for _, primary := range []bool{false, true} {
		for _, legacyFirst := range []bool{false, true} {
			_, report := WithFailureReport(background)
			batch := transactionFailures{primary: primary}
			batch.add(failuretype.PolicyRefused)
			batch.add(failuretype.PolicyRefused) // distinct occurrences, same enum
			report.publish(batch)
			if legacyFirst {
				takeKinds(t, report, failuretype.PolicyRefused, failuretype.PolicyRefused)
				if kinds, n, valid, role := report.TakeWithPrimary(); valid || role || n != 0 || kinds != ([3]failuretype.Type{}) {
					t.Fatal("role API recounted legacy consumption")
				}
			} else {
				kinds, n, valid, role := report.TakeWithPrimary()
				if !valid || role != primary || n != 2 || kinds[0] != failuretype.PolicyRefused || kinds[1] != failuretype.PolicyRefused {
					t.Fatal("explicit primary metadata was inferred from count/category")
				}
				if _, _, valid := report.Take(); valid {
					t.Fatal("legacy API recounted role-aware consumption")
				}
			}
			for _, value := range []any{report, batch} {
				wire, err := json.Marshal(value)
				if err != nil || string(wire) != "{}" {
					t.Fatal("private report role reached JSON")
				}
			}
		}
	}
	typ := reflect.TypeFor[transactionFailures]()
	for i := range typ.NumField() {
		field := typ.Field(i)
		if field.IsExported() || (field.Type.Kind() != reflect.Array && field.Type.Kind() != reflect.Int && field.Type.Kind() != reflect.Bool) {
			t.Fatal("report acquired exported or nonnumeric data")
		}
	}
	field, exists := typ.FieldByName("primary")
	if !exists || field.Type.Kind() != reflect.Bool {
		t.Fatal("primary role is not one private boolean")
	}
	var absent *FailureReport
	if _, _, valid, primary := absent.TakeWithPrimary(); valid || primary {
		t.Fatal("absent report claimed primary evidence")
	}
	for _, fault := range []string{"premature", "reuse", "invalid", "overflow"} {
		_, report := WithFailureReport(background)
		batch := transactionFailures{primary: true}
		batch.add(failuretype.AccountMissing)
		switch fault {
		case "premature":
			_, _, _, _ = report.TakeWithPrimary()
		case "reuse":
			report.publish(batch)
		case "invalid":
			batch.add(0)
		case "overflow":
			for range 3 {
				batch.add(failuretype.PolicyRefused)
			}
		}
		report.publish(batch)
		if kinds, n, valid, primary := report.TakeWithPrimary(); valid || primary || n != 0 || kinds != ([3]failuretype.Type{}) {
			t.Fatal("invalid report exposed partial role/count evidence")
		}
	}
}

func TestFailureReportPrimaryAtomicMixedConsumeAndPublication(t *testing.T) {
	for _, primary := range []bool{false, true} {
		for range 100 {
			_, report := WithFailureReport(background)
			batch := transactionFailures{primary: primary}
			batch.add(failuretype.PolicyRefused)
			var wg sync.WaitGroup
			start := make(chan struct{})
			results := make(chan bool, 8)
			wg.Go(func() { <-start; report.publish(batch) })
			for i := range 8 {
				wg.Go(func() {
					<-start
					var kinds [3]failuretype.Type
					var n int
					var valid bool
					if i%2 == 0 {
						var role bool
						kinds, n, valid, role = report.TakeWithPrimary()
						if (valid && role != primary) || (!valid && role) {
							t.Error("primary role detached from atomic report evidence")
						}
					} else {
						kinds, n, valid = report.Take()
					}
					if valid && (n != 1 || kinds[0] != failuretype.PolicyRefused) {
						t.Error("mixed consumers corrupted report")
					}
					results <- valid
				})
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
				t.Fatal("mixed consumers recounted report")
			}
		}
	}
}
