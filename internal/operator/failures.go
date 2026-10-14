package operator

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	validation "github.com/pocketbase/ozzo-validation/v4"
	"github.com/pocketbase/pocketbase/tools/router"
	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

type nativeOwnerKey struct{}

// One native request; numeric evidence only, discarded on return.
// No generic CRUD/activity or financial-success hooks are installed.
type nativeOwner struct {
	observer         failureowner.Observer
	kinds            [8]failuretype.Type
	count            int
	invalid          bool
	stage            failuretype.Type
	deliveryObserved bool
}

func (o *nativeOwner) ObserveFailure(kind failuretype.Type) {
	if !kind.Valid() || o.count == len(o.kinds) {
		o.invalid = true
		return
	}
	o.kinds[o.count] = kind
	o.count++
}
func (o *nativeOwner) InvalidateFailures() { o.invalid = true }
func (o *nativeOwner) publish() {
	if o.invalid {
		failureowner.Invalidate(o.observer)
		return
	}
	for _, kind := range o.kinds[:o.count] {
		failureowner.Observe(o.observer, kind)
	}
}
func requestOwner(r *http.Request) *nativeOwner {
	o, _ := r.Context().Value(nativeOwnerKey{}).(*nativeOwner)
	return o
}
func own(r *http.Request, kind failuretype.Type) {
	if o := requestOwner(r); o != nil {
		o.ObserveFailure(kind)
	}
}
func nativeRequestKind(r *http.Request) failuretype.Type {
	path := r.URL.Path
	if strings.HasPrefix(path, "/_/") {
		return failuretype.NativeAssetUnknown
	}
	if strings.HasPrefix(path, "/api/collections/") {
		part := path[strings.LastIndex(path, "/")+1:]
		if strings.HasPrefix(part, "auth-") || strings.HasPrefix(part, "request-") || strings.HasPrefix(part, "confirm-") {
			return failuretype.NativeAuthUnknown
		}
	}
	switch r.Method {
	case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
		return failuretype.NativeWriteUnknown
	}
	return failuretype.NativeReadUnknown
}
func safeIs(err, target error) (yes bool) {
	defer func() { _ = recover() }()
	return errors.Is(err, target)
}
func expected(err error) bool {
	return safeIs(err, http.ErrAbortHandler) || safeIs(err, http.ErrServerClosed)
}

// ApiError.RawData is the pinned SDK's supported cause surface. Cause-free
// 401/403 report only the SDK authority refusal, not its root cause or effects.
func nativeError(err error, fallback failuretype.Type) (status int, kind failuretype.Type) {
	status, kind = 500, ledger.FailureKind(err, fallback)
	defer func() {
		if recover() != nil {
			status, kind = 500, fallback
		}
	}()
	var api *router.ApiError
	if errors.As(err, &api) && api != nil {
		if api.Status >= 400 && api.Status <= 599 {
			status = api.Status
		}
		if api.RawData() == nil {
			switch status {
			case http.StatusUnauthorized:
				kind = failuretype.AuthenticationRefused
			case http.StatusForbidden:
				kind = failuretype.PolicyRefused
			case http.StatusRequestEntityTooLarge:
				kind = failuretype.RequestInvalid
			}
		}
		if cause, ok := api.RawData().(error); ok {
			kind = ledger.FailureKind(cause, fallback)
			var syntax *json.SyntaxError
			var value *json.UnmarshalTypeError
			var limit *http.MaxBytesError
			var fields validation.Errors
			if errors.As(cause, &syntax) || errors.As(cause, &value) || errors.As(cause, &limit) || errors.As(cause, &fields) {
				kind = failuretype.RequestInvalid
			}
		}
	}
	return
}

func (o *nativeOwner) delivery(err error) {
	if expected(err) {
		return
	}
	kind := ledger.FailureKind(err, failuretype.ResponseDeliveryUnknown)
	if kind != failuretype.Cancelled && kind != failuretype.DeadlineExceeded {
		kind = failuretype.ResponseDeliveryUnknown
	}
	if !o.deliveryObserved {
		o.ObserveFailure(kind)
	}
	o.deliveryObserved = true
}

// Preserve the native SDK writer and static-file behavior. Own errors/short
// writes/panics once; propagation through e.Next must not count them again.
type nativeWriter struct {
	http.ResponseWriter
	owner             *nativeOwner
	written           bool
	discardAssetError bool
}

func (w *nativeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *nativeWriter) Header() http.Header {
	defer w.recoverDelivery()
	return w.ResponseWriter.Header()
}
func (w *nativeWriter) WriteHeader(status int) {
	defer w.recoverDelivery()
	// ServeContent and mux fallbacks may write an error and return nil.
	// Preserve stock MFA challenge data; all other unowned error bodies close.
	if status >= 400 && w.owner.count == 0 && !(status == 401 && w.owner.stage == failuretype.NativeAuthUnknown) {
		w.owner.ObserveFailure(w.owner.stage)
		w.Header().Del("Content-Length")
		w.Header().Del("Content-Encoding")
		w.Header().Del("Content-Range")
		stage := "native_request"
		if w.owner.stage == failuretype.NativeAssetUnknown {
			stage = "native_asset"
		}
		failure(w, status, stage, "native_protocol_cause_unknown")
		w.discardAssetError = true
		return
	}
	w.written = true
	w.ResponseWriter.WriteHeader(status)
}
func (w *nativeWriter) recoverDelivery() {
	if value := recover(); value != nil {
		err, _ := value.(error)
		w.owner.delivery(err)
		panic(http.ErrAbortHandler)
	}
}
func (w *nativeWriter) Write(raw []byte) (n int, result error) {
	if w.discardAssetError {
		return len(raw), nil
	}
	defer func() {
		if value := recover(); value != nil {
			err, _ := value.(error)
			w.owner.delivery(err)
			panic(http.ErrAbortHandler)
		}
	}()
	w.written = true
	n, result = w.ResponseWriter.Write(raw)
	if result != nil || n != len(raw) {
		w.owner.delivery(result)
	}
	return
}
func (w *nativeWriter) finish() {
	if w.owner.deliveryObserved {
		return
	} // do not attempt more I/O after failed delivery
	defer func() {
		if value := recover(); value != nil {
			err, _ := value.(error)
			w.owner.delivery(err)
		}
	}()
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if err != nil && !safeIs(err, http.ErrNotSupported) {
		w.owner.delivery(err)
	}
}
