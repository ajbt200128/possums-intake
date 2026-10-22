package operator

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"possums-intake/internal/failuretype"
)

type panicBody struct{ read *bool }

func (b panicBody) Read([]byte) (int, error) { *b.read = true; panic("SYNTHETIC_PRIVATE_SENTINEL") }
func (panicBody) Close() error               { return nil }

type diagnosticHeaderPanic struct {
	header http.Header
	read   *bool
}

func (w *diagnosticHeaderPanic) Header() http.Header {
	if *w.read {
		panic("SYNTHETIC_PRIVATE_SENTINEL independent presentation fault")
	}
	return w.header
}
func (*diagnosticHeaderPanic) WriteHeader(int)               {}
func (*diagnosticHeaderPanic) Write(raw []byte) (int, error) { return len(raw), nil }

func TestNativeSDKBodyPanicAndDiagnosticDeliveryPanicBothObserved(t *testing.T) {
	s, app, base := fixture(t)
	token := login(t, base)
	o := &nativeObserver{}
	h := observedNative(t, s, app, o)
	req := httptest.NewRequest("POST", accountRecords, nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)
	read := false
	req.Body = panicBody{&read}
	req.ContentLength = -1
	abortNative(t, h, &diagnosticHeaderPanic{http.Header{}, &read}, req)
	// The same body fault is now observed by the pre-form key adapter, before
	// the SDK financial write stage; the independent delivery fault stays owned.
	o.want(t, failuretype.NativeAuthUnknown, failuretype.ResponseDeliveryUnknown)
	if !s.Ready() {
		t.Fatal("presentation faults changed readiness")
	}
}
