// Package operator is an opt-in trusted native control-plane surface.
// It never receives the accounting key from the server or bypasses httpapi.
package operator

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
	"possums-intake/internal/siteauth"
)

//go:embed ui/dist
var nativeAssets embed.FS

const authPrefix = "/api/collections/_superusers/"

// NewHandler invokes neither OnServe nor apis.Serve (installer/cron/second listener).
func NewHandler(accounting http.Handler, app core.App, service *ledger.Service) (http.Handler, error) {
	return NewHandlerObserved(accounting, app, service, nil)
}

func NewHandlerObserved(accounting http.Handler, app core.App, service *ledger.Service, observer failureowner.Observer) (handler http.Handler, result error) {
	fallback := failuretype.RuntimeSetupUnknown
	defer func() {
		if recover() != nil {
			handler = nil
			result = ledger.CanonicalFailure(&ledger.Diagnostic{}, fallback)
		}
		if result != nil {
			result = ledger.CanonicalFailure(result, fallback)
			failureowner.Observe(observer, ledger.FailureKind(result, fallback))
		}
	}()
	r, err := apis.NewRouter(app)
	if err != nil {
		return nil, err
	}
	r.Unbind(apis.DefaultActivityLoggerMiddlewareId, apis.DefaultPanicRecoverMiddlewareId)
	r.Bind(siteauth.CustomerKeyMiddleware())
	// The stock restore controller returns before its background operation.
	// Own errors at the supported restore hook, not as HTTP success/activity.
	app.OnBackupRestore().Bind(&hook.Handler[*core.BackupEvent]{
		Id: "intakeClosedBackupRestore", Priority: -1000,
		Func: func(e *core.BackupEvent) (result error) {
			defer func() {
				if recover() != nil {
					result = &ledger.Diagnostic{}
				}
				if result != nil {
					result = ledger.CanonicalFailure(result, failuretype.NativeWriteUnknown)
					failureowner.Observe(observer, ledger.FailureKind(result, failuretype.NativeWriteUnknown))
				}
			}()
			return e.Next()
		},
	})
	r.Bind(&hook.Handler[*core.RequestEvent]{Id: "intakeClosedNativeErrors", Priority: apis.DefaultPanicRecoverMiddlewarePriority - 1, Func: closedNativeErrors})
	// SDK authentication, collection rules, body limits and query/file tokens
	// remain native. Trusted writes deliberately bypass the accounting Service.
	fallback = failuretype.NativeAssetUnknown
	assets, err := fs.Sub(nativeAssets, "ui/dist")
	if err != nil {
		return nil, err
	}
	fallback = failuretype.RuntimeSetupUnknown
	// The stock extension entrypoint is installed only by OnServe. No extension
	// framework, reinitialization, cron or second listener is started here.
	r.GET("/_/extensions.js", func(e *core.RequestEvent) error {
		e.Response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, err := e.Response.Write([]byte("// Native UI extensions disabled.\n"))
		return err
	})
	r.GET("/_/{path...}", apis.Static(assets, false))
	mux, err := r.BuildMux()
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := req.URL.Path
		if !strings.HasPrefix(path, "/api") && !strings.HasPrefix(path, "/_") {
			accounting.ServeHTTP(w, req)
			return
		}
		o := &nativeOwner{observer: observer, stage: nativeRequestKind(req)}
		req = req.WithContext(context.WithValue(req.Context(), nativeOwnerKey{}, o))
		tracked := &nativeWriter{ResponseWriter: w, owner: o}
		w = tracked
		defer func() {
			defer o.publish()
			abort := false
			if value := recover(); value != nil {
				err, _ := value.(error)
				abort = expected(err) || tracked.written
				if !expected(err) {
					o.ObserveFailure(o.stage)
					if !tracked.written {
						failure(w, 500, nativeStage(path), "completed_native_handler_cause_unknown")
					}
				}
			}
			if !abort {
				tracked.finish()
			}
			if abort {
				panic(http.ErrAbortHandler)
			}
		}()
		headers(w, path)
		mux.ServeHTTP(w, req)
	}), nil
}

func headers(w http.ResponseWriter, path string) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	// Upstream UI sets inline styles; scripts/requests stay same-origin.
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; connect-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
}

func nativeStage(path string) string {
	switch path {
	case authPrefix + "auth-with-password":
		return "native_password_auth"
	case authPrefix + "auth-refresh":
		return "native_auth_refresh"
	case authPrefix + "auth-methods":
		return "native_auth_methods"
	}
	if _, account := accountRoute(path); account {
		return "native_account_request"
	}
	if strings.HasPrefix(path, "/_/") {
		return "native_asset"
	}
	return "native_request"
}

// Consume before router.ErrorHandler can serialize upstream causes or log a
// serialization failure. Partial responses cannot be replaced with receipts.
func closedNativeErrors(e *core.RequestEvent) (result error) {
	fallback := nativeRequestKind(e.Request)
	defer func() {
		if recovered := recover(); recovered != nil {
			err, _ := recovered.(error)
			if expected(err) {
				panic(http.ErrAbortHandler)
			}
			if o := requestOwner(e.Request); o != nil {
				fallback = o.stage
			}
			own(e.Request, fallback)
			if e.Written() {
				panic(http.ErrAbortHandler)
			}
			failure(e.Response, 500, nativeStage(e.Request.URL.Path), "completed_native_handler_cause_unknown")
			result = nil
		}
	}()
	err := e.Next()
	if err == nil || safeIs(err, apis.ErrMFA) {
		// Stock MFA challenge is protocol data, not an error or auth bypass.
		return nil
	}
	if expected(err) {
		panic(http.ErrAbortHandler)
	}
	if o := requestOwner(e.Request); o != nil {
		fallback = o.stage
	}
	status, kind := nativeError(err, fallback)
	if o := requestOwner(e.Request); o == nil || !o.deliveryObserved {
		own(e.Request, kind)
	}
	if e.Written() {
		panic(http.ErrAbortHandler)
	}
	if customerKeyFailure(e.Response, status, err) || websitePublicationFailure(e.Response, status, err) {
		return nil
	}
	failure(e.Response, status, nativeStage(e.Request.URL.Path), "native_operation_cause_unknown")
	return nil
}

func failure(w http.ResponseWriter, status int, stage, constraint string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Outcome is not inferred from HTTP status: stock writes may already commit.
	_ = json.NewEncoder(w).Encode(struct {
		Code       string            `json:"code"`
		Stage      string            `json:"stage"`
		Constraint string            `json:"constraint"`
		Outcome    string            `json:"outcome"`
		Status     int               `json:"status"`
		Message    string            `json:"message"`
		Data       map[string]string `json:"data"`
		NextStep   string            `json:"next_step"`
	}{"OPERATOR_NATIVE_REQUEST_UNAVAILABLE", stage, constraint, "unknown", status, "Native operator request unavailable; underlying cause unknown.", map[string]string{}, "Check SDK requirements and native login privately. Stock write outcome may be unknown; inspect state before another attempt. Do not report credentials or raw errors."})
}
