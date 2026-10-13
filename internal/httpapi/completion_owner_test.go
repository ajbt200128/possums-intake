package httpapi

import (
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

// Source-injection seam only: Service intentionally exposes no production app
// replacement/testing knob. Rebind its existing app while no calls are active,
// retaining the real pinned PocketBase transaction and completion callbacks.
// This qualifies HTTP report consumption, not an naturally induced SDK fault.
func injectHTTPCompletionApp(t *testing.T, s *ledger.Service, count int, panicAfter bool) {
	t.Helper()
	injectHTTPCompletionFaultApp(t, s, httpCompletionApp{count: count, panicAfter: panicAfter})
}

func injectHTTPCompletionFaultApp(t *testing.T, s *ledger.Service, replacement httpCompletionApp) {
	t.Helper()
	field := reflect.ValueOf(s).Elem().FieldByName("app")
	if field.Type() != reflect.TypeFor[core.App]() || !field.CanAddr() {
		t.Fatal("synthetic app seam no longer matches Service layout")
	}
	slot := (*core.App)(unsafe.Pointer(field.UnsafeAddr()))
	original := *slot
	replacement.App = original
	*slot = replacement
	t.Cleanup(func() { *slot = original })
}

type httpCompletionApp struct {
	core.App
	count      int
	panicAfter bool
	cause      error
	delayWork  bool
}

func (a httpCompletionApp) RunInTransaction(fn func(core.App) error) error {
	return a.App.RunInTransaction(func(tx core.App) error {
		if a.delayWork {
			// Synthetic scheduling seam: let the actual Service work budget expire
			// while the caller context and real PocketBase transaction remain active.
			time.Sleep(ledger.WorkBudget + 20*time.Millisecond)
		}
		err := fn(tx)
		for range a.count {
			tx.TxInfo().OnComplete(func(error) error {
				if a.cause != nil {
					return a.cause
				}
				return errors.New("SYNTHETIC_PRIVATE_SENTINEL completion")
			})
		}
		if a.panicAfter {
			tx.TxInfo().OnComplete(func(error) error { panic("SYNTHETIC_PRIVATE_SENTINEL completion panic") })
		}
		return err
	})
}

func TestHTTPActualCompletionHooksKnownCommitAndPrimaryOnce(t *testing.T) {
	for _, fixture := range []struct {
		primary    bool
		hooks      int
		panicAfter bool
	}{
		{false, 1, false}, {false, 3, false}, {false, 0, true}, {true, 2, false}, {true, 0, true},
	} {
		t.Run("completion", func(t *testing.T) {
			s := newTestLedger(t)
			provision(t, newTestHandler(t, s, nil), "a")
			injectHTTPCompletionApp(t, s, fixture.hooks, fixture.panicAfter)
			spy := &failureSpy{}
			h := observedHandler(t, s, spy)
			var want []failuretype.Type
			if fixture.primary {
				out := postJSON(t, h, "/balance", `{"account_id":"missing"}`)
				if fixture.panicAfter {
					requireStatus(t, out, 503)
				} else {
					requireStatus(t, out, 404)
				}
				closedDiagnostic(t, out)
				want = append(want, failuretype.AccountMissing)
			} else {
				out := postJSON(t, h, "/accounts/provision", `{"account_id":"committed"}`)
				requireStatus(t, out, 200)
				row := decodeMap(t, out)["account_balance"].([]any)[0].(map[string]any)
				if row["account_id"] != "committed" || !s.Ready() {
					t.Fatal("known commit erased by hook fault")
				}
			}
			for range fixture.hooks {
				want = append(want, failuretype.TransactionAfterCompletionUnknown)
			}
			if fixture.panicAfter {
				want = append(want, failuretype.TransactionAfterCompletionUnknown)
			}
			expectFailures(t, spy, want...)
		})
	}
}

func TestHTTPActualReportMaximumSlotsAndOverflow(t *testing.T) {
	for _, hooks := range []int{2, 3} {
		t.Run("maximum", func(t *testing.T) {
			s := newTestLedger(t)
			injectHTTPCompletionApp(t, s, hooks, false)
			spy := &failureSpy{}
			h := observedHandler(t, s, spy)
			// The encoder itself is a synthetic source seam; the primary and
			// completion faults are actual Service.run/PocketBase reports.
			h.routes["/balance"] = func(w http.ResponseWriter, r *http.Request) {
				h.balance(w, r)
				writeJSON(w, 200, syntheticMarshaler{})
			}
			h.ServeHTTP(&failedWriter{header: http.Header{}, mode: "short"}, failureRequest("/balance", `{"account_id":"missing"}`))
			if hooks == 2 {
				expectFailures(t, spy, failuretype.ResponseEncodingUnknown, failuretype.AccountMissing, failuretype.TransactionAfterCompletionUnknown, failuretype.TransactionAfterCompletionUnknown, failuretype.ResponseDeliveryUnknown)
			} else if len(spy.kinds) != 0 || spy.invalidFailures != 1 || spy.invalidRequests != 0 {
				t.Fatal("overflowed real report became partial counts or affected terminal integrity")
			}
		})
	}
}
