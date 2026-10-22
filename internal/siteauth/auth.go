// Package siteauth adapts PocketBase authentication for private Go callers.
// Tokens and principals must never be passed to Lisp, HTML or diagnostics.
package siteauth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/router"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

const (
	authBudget  = 5 * time.Second
	maxResponse = 64 << 10
	maxToken    = 16 << 10
)

// Principal is a private binding, not a customer identity/product contract.
type Principal struct{ RecordID, CollectionID, AccountID string }

// Adapter owns no listener, session store, credentials or application lifecycle.
type Adapter struct {
	app                      core.App
	collection, collectionID string
	mux                      http.Handler
	customer                 bool
}
type resultKey struct{}
type dispatchResult struct{ failure error }

// New prepares a supported SDK router for one explicitly supplied non-superuser
// auth collection. The owner must already enforce logging/AuthAlert off.
func New(app core.App, collection string) (a *Adapter, err error) {
	defer func() {
		if recover() != nil {
			a = nil
			err = unknown("site_auth_setup")
		}
	}()
	if app == nil || collection == "" || strings.ContainsAny(collection, "/\\?#%") {
		return nil, refused("SITE_AUTH_CONFIG", "site_auth_setup", "fixed_customer_auth_collection", failuretype.PolicyRefused)
	}
	c, e := app.FindCollectionByNameOrId(collection)
	if e != nil {
		return nil, unknown("site_auth_setup")
	}
	if c.Name != collection || !c.IsAuth() || c.Name == core.CollectionNameSuperusers {
		return nil, refused("SITE_AUTH_CONFIG", "site_auth_setup", "fixed_customer_auth_collection", failuretype.PolicyRefused)
	}
	a = &Adapter{app: app, collection: collection, collectionID: c.Id}
	if _, e = a.config(); e != nil {
		return nil, e
	}
	r, e := apis.NewRouter(app)
	if e != nil {
		return nil, unknown("site_auth_setup")
	}
	r.Unbind(apis.DefaultActivityLoggerMiddlewareId, apis.DefaultPanicRecoverMiddlewareId)
	r.Bind(&hook.Handler[*core.RequestEvent]{Id: "siteAuthClosedErrors", Priority: apis.DefaultPanicRecoverMiddlewarePriority - 1, Func: func(e *core.RequestEvent) (err error) {
		state := e.Request.Context().Value(resultKey{}).(*dispatchResult)
		defer func() {
			if recover() != nil {
				state.failure = unknown("site_auth_dispatch")
				err = nil
			}
		}()
		cause := e.Next()
		if cause == nil {
			return nil
		}
		if errors.Is(cause, apis.ErrMFA) {
			state.failure = refused("SITE_AUTH_MFA_UNSUPPORTED", "site_auth_login", "completed_password_only_auth", failuretype.PolicyRefused)
			return nil
		}
		var api *router.ApiError
		if errors.As(cause, &api) && (api.Status == 400 || api.Status == 401 || api.Status == 403) {
			state.failure = refused("SITE_AUTH_REFUSED", "site_auth_login", "sdk_credentials_and_auth_rule", failuretype.AuthenticationRefused)
		} else {
			state.failure = classified(unknown("site_auth_dispatch"), cause)
		}
		// Never return an SDK error to router.ErrorHandler (which may log).
		return nil
	}})
	a.mux, e = r.BuildMux()
	if e != nil {
		return nil, unknown("site_auth_setup")
	}
	return a, nil
}

func (a *Adapter) config() (*core.Collection, error) {
	c, err := a.app.FindCollectionByNameOrId(a.collectionID)
	if err != nil {
		return nil, unknown("site_auth_config")
	}
	if c.Name != a.collection || !c.IsAuth() || c.Name == core.CollectionNameSuperusers || c.AuthAlert.Enabled || a.app.Settings().Logs.MaxDays != 0 {
		return nil, refused("SITE_AUTH_CONFIG", "site_auth_config", "quiet_fixed_customer_collection", failuretype.PolicyRefused)
	}
	if a.customer {
		if !customerSchema(c) {
			return nil, refused("SITE_AUTH_CONFIG", "site_auth_config", "fixed_customer_key_schema", failuretype.PolicyRefused)
		}
		if c.MFA.Enabled {
			return nil, refused("SITE_AUTH_MFA_UNSUPPORTED", "site_auth_config", "supported_customer_auth_configuration", failuretype.PolicyRefused)
		}
	}
	return c, nil
}

// Login dispatches only an internally constructed bounded password request; no
// client headers, collection selectors or SDK routes are forwarded. The final
// token is private Go data. No browser session is created here.
func (a *Adapter) Login(ctx context.Context, identity, password string) (token string, principal Principal, err error) {
	defer func() {
		if recover() != nil {
			token = ""
			principal = Principal{}
			err = unknown("site_auth_login")
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, authBudget)
	defer cancel()
	if e := ctx.Err(); e != nil {
		return "", Principal{}, interrupted(e, "site_auth_login")
	}
	if identity == "" || password == "" || len(identity) > 255 || len(password) > 255 || !utf8.ValidString(identity) || !utf8.ValidString(password) {
		return "", Principal{}, refused("SITE_AUTH_INPUT", "site_auth_login", "bounded_identity_and_password", failuretype.RequestInvalid)
	}
	if _, e := a.config(); e != nil {
		return "", Principal{}, e
	}
	body, _ := json.Marshal(struct {
		Identity string `json:"identity"`
		Password string `json:"password"`
	}{identity, password})
	state := &dispatchResult{}
	ctx = context.WithValue(ctx, resultKey{}, state)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/api/collections/"+url.PathEscape(a.collection)+"/auth-with-password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	response := &privateResponse{header: make(http.Header)}
	a.mux.ServeHTTP(response, req)
	if state.failure != nil {
		return "", Principal{}, state.failure
	}
	if e := ctx.Err(); e != nil {
		return "", Principal{}, interrupted(e, "site_auth_login")
	}
	if response.overflow || response.status != http.StatusOK {
		return "", Principal{}, unknown("site_auth_final_response")
	}
	var final struct {
		Token  string `json:"token"`
		Record struct {
			ID           string `json:"id"`
			CollectionID string `json:"collectionId"`
		} `json:"record"`
	}
	decoder := json.NewDecoder(bytes.NewReader(response.body.Bytes()))
	if decoder.Decode(&final) != nil || decoder.Decode(new(any)) != io.EOF || final.Token == "" {
		return "", Principal{}, unknown("site_auth_final_response")
	}
	p, e := a.Validate(ctx, final.Token)
	if e != nil {
		return "", Principal{}, e
	}
	if p.RecordID != final.Record.ID || p.CollectionID != final.Record.CollectionID {
		return "", Principal{}, unknown("site_auth_final_response")
	}
	c, e := a.config()
	if e != nil {
		return "", Principal{}, e
	}
	if c.MFA.Enabled {
		return "", Principal{}, refused("SITE_AUTH_MFA_UNSUPPORTED", "site_auth_login", "supported_customer_auth_configuration", failuretype.PolicyRefused)
	}
	return final.Token, p, nil
}

// Validate uses current SDK token/existence checks and current AuthRule with
// RequestInfo equivalent to SDK auth-refresh: POST, authenticated record, empty
// body/query, default context. It does not refresh or issue a token. Rules that
// require password-request-only data will therefore deny protected access.
func (a *Adapter) Validate(ctx context.Context, token string) (principal Principal, err error) {
	defer func() {
		if recover() != nil {
			principal = Principal{}
			err = unknown("site_auth_validate")
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, authBudget)
	defer cancel()
	if e := ctx.Err(); e != nil {
		return Principal{}, interrupted(e, "site_auth_validate")
	}
	if token == "" || len(token) > maxToken {
		return Principal{}, refused("SITE_AUTH_INVALID", "site_auth_validate", "current_sdk_auth_token", failuretype.AuthenticationRefused)
	}
	if _, e := a.config(); e != nil {
		return Principal{}, e
	}
	record, e := a.app.FindAuthRecordByToken(token, core.TokenTypeAuth)
	if e != nil {
		// PB also returns untyped errors for some claim/type failures. Do not
		// mistake an unexpected storage/SDK error for proven bad credentials.
		if errors.Is(e, sql.ErrNoRows) || errors.Is(e, jwt.ErrTokenMalformed) || errors.Is(e, jwt.ErrTokenSignatureInvalid) || errors.Is(e, jwt.ErrTokenExpired) || errors.Is(e, jwt.ErrTokenInvalidClaims) || errors.Is(e, jwt.ErrTokenUnverifiable) {
			return Principal{}, refused("SITE_AUTH_INVALID", "site_auth_validate", "current_sdk_auth_token", failuretype.AuthenticationRefused)
		}
		d := unknown("site_auth_token_lookup")
		return Principal{}, classified(d, e)
	}
	if record.IsSuperuser() || record.Collection().Id != a.collectionID || record.Collection().Name != a.collection {
		return Principal{}, refused("SITE_AUTH_INVALID", "site_auth_validate", "expected_customer_collection", failuretype.AuthenticationRefused)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/api/collections/"+url.PathEscape(a.collection)+"/auth-refresh", nil)
	req.Header.Set("Authorization", token)
	event := &core.RequestEvent{App: a.app, Auth: record}
	event.Request = req
	info, e := event.RequestInfo()
	if e != nil {
		return Principal{}, unknown("site_auth_validate")
	}
	allowed, e := a.app.CanAccessRecord(record, info, record.Collection().AuthRule)
	if e != nil {
		return Principal{}, classified(unknown("site_auth_rule"), e)
	}
	if !allowed {
		return Principal{}, refused("SITE_AUTH_INVALID", "site_auth_validate", "current_auth_rule", failuretype.PolicyRefused)
	}
	if e := ctx.Err(); e != nil {
		return Principal{}, interrupted(e, "site_auth_validate")
	}
	principal = Principal{RecordID: record.Id, CollectionID: record.Collection().Id}
	if a.customer || a.collection == CustomerCollection {
		principal.AccountID = record.Id
	}
	return principal, nil
}

// The writer retains only bounded private response bytes, including challenges;
// SDK errors and panics never become public HTTP or stdio output here.
type privateResponse struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (w *privateResponse) Header() http.Header { return w.header }
func (w *privateResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *privateResponse) Write(p []byte) (int, error) {
	w.WriteHeader(200)
	if w.overflow || len(p) > maxResponse-w.body.Len() {
		w.overflow = true
		return 0, io.ErrShortWrite
	}
	return w.body.Write(p)
}

func refused(code, stage, constraint string, kind failuretype.Type) *ledger.Diagnostic {
	d := ledger.CanonicalFailure(&ledger.Diagnostic{}, kind)
	d.Code = ledger.Code(code)
	d.Stage = stage
	d.Constraint = constraint
	d.Outcome = "no_customer_session_created"
	d.NextStep = "Check credentials and supported customer authentication settings privately; reload the sign-in form. Do not share credentials."
	return d
}
func unknown(stage string) *ledger.Diagnostic {
	d := refused("SITE_AUTH_UNAVAILABLE", stage, "sdk_completion_cause_unknown", failuretype.NativeAuthUnknown)
	d.NextStep = "Underlying cause unknown. No customer session was created by this adapter; SDK auth-side effects may have occurred. Inspect authentication privately before another attempt."
	return d
}

// Reuse supported failure categories without retaining or exposing the cause.
func classified(d *ledger.Diagnostic, cause error) *ledger.Diagnostic {
	clean := ledger.CanonicalFailure(cause, failuretype.NativeAuthUnknown)
	clean.Code = d.Code
	clean.Stage = d.Stage
	clean.Constraint = d.Constraint
	clean.Outcome = d.Outcome
	clean.NextStep = d.NextStep
	return clean
}

func interrupted(e error, stage string) *ledger.Diagnostic {
	kind := failuretype.Cancelled
	if e == context.DeadlineExceeded {
		kind = failuretype.DeadlineExceeded
	}
	return refused("SITE_AUTH_INTERRUPTED", stage, "caller_or_auth_budget", kind)
}
