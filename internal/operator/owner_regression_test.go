package operator

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"possums-intake/internal/failuretype"
)

type panicBody struct{}

func (panicBody) Read([]byte) (int, error) { panic("SYNTHETIC_PRIVATE_SENTINEL") }
func (panicBody) Close() error             { return nil }

type secondHeaderPanic struct {
	header http.Header
	calls  int
}

func (w *secondHeaderPanic) Header() http.Header {
	w.calls++
	if w.calls > 1 {
		panic("SYNTHETIC_PRIVATE_SENTINEL independent presentation fault")
	}
	return w.header
}
func (*secondHeaderPanic) WriteHeader(int)               {}
func (*secondHeaderPanic) Write(raw []byte) (int, error) { return len(raw), nil }

func TestNativeReadPanicAndDiagnosticDeliveryPanicBothObserved(t *testing.T) {
	s, app, _ := fixture(t)
	o := &nativeObserver{}
	h := observedNative(t, s, app, o)
	req := httptest.NewRequest("POST", accountRecords, nil)
	req.Header.Set("Content-Type", "application/json")
	req.Body = panicBody{}
	abortNative(t, h, &secondHeaderPanic{header: http.Header{}}, req)
	o.want(t, failuretype.RequestReadUnknown, failuretype.ResponseDeliveryUnknown)
	if !s.Ready() {
		t.Fatal("presentation faults changed readiness")
	}
}
