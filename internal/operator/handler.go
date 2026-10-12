// Package operator is an opt-in, synthetic-only native control-plane surface.
// It never receives the accounting key from the server or bypasses httpapi.
package operator

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/router"
	"possums-intake/internal/ledger"
)

//go:embed ui/dist
var nativeAssets embed.FS

const authPrefix = "/api/collections/_superusers/"

var recordID = regexp.MustCompile(`^[a-z0-9]{15}$`)

func readField(name string) bool {
	switch name {
	case "id", "name", "type", "created", "updated", "account_id", "balance", "enabled", "email", "emailVisibility", "verified", "collectionId", "collectionName":
		return true
	}
	return false
}

func readFields(value string) bool {
	seen := map[string]bool{}
	for _, field := range strings.Split(value, ",") {
		if !readField(field) || seen[field] {
			return false
		}
		seen[field] = true
	}
	return true
}

// NewHandler builds a restricted router around the existing bootstrapped app.
// It invokes neither OnServe nor apis.Serve (installer/cron/second listener).
func NewHandler(accounting http.Handler, app core.App, service *ledger.Service) (http.Handler, error) {
	bindAccountRequests(app, service)
	r, err := apis.NewRouter(app)
	if err != nil {
		return nil, err
	}
	r.Unbind(apis.DefaultActivityLoggerMiddlewareId, apis.DefaultPanicRecoverMiddlewareId, apis.DefaultBodyLimitMiddlewareId)
	bodyLimit := apis.BodyLimit(4096)
	bodyLimit.Id = "intakeNativeBodyLimit"
	r.Bind(bodyLimit)
	r.Bind(&hook.Handler[*core.RequestEvent]{Id: "intakeClosedNativeErrors", Priority: apis.DefaultPanicRecoverMiddlewarePriority - 1, Func: closedNativeErrors})
	// Native authentication, loaded by PocketBase, is required even for reads
	// whose generic collection rules might otherwise allow guest access.
	r.Bind(&hook.Handler[*core.RequestEvent]{Id: "intakeNativeReadAuthority", Priority: apis.DefaultLoadAuthTokenMiddlewarePriority + 1, Func: func(e *core.RequestEvent) error {
		if strings.HasPrefix(e.Request.URL.Path, "/api/") && e.Request.URL.Path != authPrefix+"auth-methods" && e.Request.URL.Path != authPrefix+"auth-with-password" {
			if e.Auth == nil || !e.Auth.IsSuperuser() {
				return router.NewUnauthorizedError("", nil)
			}
		}
		return e.Next()
	}})
	assets, err := fs.Sub(nativeAssets, "ui/dist")
	if err != nil {
		return nil, err
	}
	// Upstream's extension route is installed only by OnServe. Supply an empty
	// module to satisfy its UI startup without enabling the extension framework.
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
		headers(w, path)
		if req.URL.EscapedPath() != path || req.URL.ForceQuery || len(req.Header.Values("Authorization")) > 1 {
			failure(w, 400, "native_gate", "canonical_path_and_single_authorization")
			return
		}
		if !allowed(req, assets) {
			failure(w, 403, "native_gate", "explicit_method_path_and_query_allowlist")
			return
		}
		if _, account := accountRoute(path); account && (req.Method == http.MethodPost || req.Method == http.MethodPatch) {
			input, ok := strictAccountBody(req)
			if !ok {
				failure(w, 400, "native_account_input", "strict_bounded_typed_account_json")
				return
			}
			req = withNativeInput(req, input)
		}
		// Never enter the ServeMux for a denied/uncanonical path: mux redirects and
		// method fallbacks must not create aliases around the gate.
		mux.ServeHTTP(w, req)
	}), nil
}

func allowed(req *http.Request, assets fs.FS) bool {
	path := req.URL.Path
	if req.Method == http.MethodGet && strings.HasPrefix(path, "/_/") {
		if req.URL.RawQuery != "" {
			return false
		}
		if path == "/_/extensions.js" {
			return true
		}
		name := strings.TrimPrefix(path, "/_/")
		if name == "" {
			name = "index.html"
		}
		if !fs.ValidPath(name) || name == "index.html" && path != "/_/" {
			return false
		}
		stat, err := fs.Stat(assets, name)
		return err == nil && stat.Mode().IsRegular()
	}
	if id, account := accountRoute(path); account {
		switch req.Method {
		case http.MethodPost:
			return id == "" && req.URL.RawQuery == ""
		case http.MethodPatch:
			return id != "" && req.URL.RawQuery == ""
		case http.MethodGet:
			if id != "" {
				return req.URL.RawQuery == ""
			}
			return listQuery(req.URL.RawQuery)
		default:
			return false
		}
	}
	if req.Method == http.MethodPost {
		return req.URL.RawQuery == "" && (path == authPrefix+"auth-with-password" || path == authPrefix+"auth-refresh")
	}
	if req.Method != http.MethodGet {
		return false
	}
	switch path {
	case authPrefix + "auth-methods":
		// Exact SDK 0.28.1 default projection, not a query bypass.
		return req.URL.RawQuery == "" || req.URL.RawQuery == "fields=mfa%2Cotp%2Cpassword%2Coauth2"
	case "/api/collections/" + ledger.AccountCollectionID, "/api/collections/" + ledger.AccountCollectionName, "/api/collections/_superusers", "/api/settings", "/api/collections/meta/scaffolds", "/api/collections/meta/oauth2-providers":
		return req.URL.RawQuery == ""
	case "/api/collections":
		return listQuery(req.URL.RawQuery, true)
	case authPrefix + "records":
		return listQuery(req.URL.RawQuery)
	default:
		if strings.HasPrefix(path, authPrefix+"records/") {
			return recordID.MatchString(strings.TrimPrefix(path, authPrefix+"records/")) && req.URL.RawQuery == ""
		}
	}
	return false
}

// Native record/schema lists aren't accounting snapshots. Filters, expansions,
// relation sorts and aliases are deliberately not part of this local packet.
func listQuery(raw string, metadata ...bool) bool {
	if len(raw) > 1024 {
		return false
	}
	for _, part := range strings.Split(raw, "&") {
		if part == "" {
			if raw == "" {
				continue
			}
			return false
		}
		pair := strings.SplitN(part, "=", 2)
		if len(pair) != 2 {
			return false
		}
		value, err := url.QueryUnescape(pair[1])
		if err != nil || url.QueryEscape(value) != pair[1] {
			return false
		}
		// No encoded key aliases, redundant encodings or missing '='.
		if strings.Contains(pair[0], "%") || strings.Contains(pair[0], "+") {
			return false
		}
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return false
	}
	for key, list := range values {
		if len(list) != 1 {
			return false
		}
		value := list[0]
		switch key {
		case "page", "perPage":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || (n > 500 && !(key == "perPage" && n == 1000 && len(metadata) == 1 && metadata[0])) || strconv.Itoa(n) != value {
				return false
			}
		case "skipTotal":
			if value != "1" && value != "0" {
				return false
			}
		case "filter":
			if value != "" {
				return false
			}
		case "sort":
			for _, field := range strings.Split(value, ",") {
				if field == "-@rowid" {
					continue
				}
				if field == "" {
					continue
				}
				if field[0] == '-' || field[0] == '+' {
					field = field[1:]
				}
				if !readField(field) {
					return false
				}
			}
		case "fields":
			if value != "" && !readFields(value) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func headers(w http.ResponseWriter, path string) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	// Upstream UI sets inline styles; scripts/requests stay same-origin.
	policy := "default-src 'none'; script-src 'self'; connect-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
	h.Set("Content-Security-Policy", policy)
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
	return "native_read"
}

// Consume before router.ErrorHandler can serialize upstream causes or log a
// serialization failure. A written/partial response cannot be replaced with a
// fabricated failure or success; abort delivery without dumping panic payloads.
func closedNativeErrors(e *core.RequestEvent) (result error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if e.Written() {
				panic(http.ErrAbortHandler)
			}
			failure(e.Response, 500, nativeStage(e.Request.URL.Path), "completed_native_handler_cause_unknown")
			result = nil
		}
	}()
	err := e.Next()
	if err == nil {
		return nil
	}
	if e.Written() {
		panic(http.ErrAbortHandler)
	}
	status := 500
	var apiErr *router.ApiError
	if errors.As(err, &apiErr) && apiErr.Status >= 400 && apiErr.Status <= 599 {
		status = apiErr.Status
	}
	failure(e.Response, status, nativeStage(e.Request.URL.Path), "native_operation_cause_unknown")
	return nil
}

func failure(w http.ResponseWriter, status int, stage, constraint string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// PB-compatible message/data plus useful closed diagnostic fields. No causes,
	// values, request identifiers or keys ever reach this boundary.
	_ = json.NewEncoder(w).Encode(struct {
		Code       string            `json:"code"`
		Stage      string            `json:"stage"`
		Constraint string            `json:"constraint"`
		Status     int               `json:"status"`
		Message    string            `json:"message"`
		Data       map[string]string `json:"data"`
		NextStep   string            `json:"next_step"`
	}{"OPERATOR_NATIVE_REQUEST_UNAVAILABLE", stage, constraint, status, "Restricted native operator request unavailable; underlying cause unknown.", map[string]string{}, "Check the approved route, runtime initialization and native login privately. Native write outcome may be unknown; refresh state before another absolute edit. Do not report credentials or raw errors."})
}
