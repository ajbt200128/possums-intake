package website

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/alexedwards/scs/v2"
	"github.com/alexedwards/scs/v2/memstore"
	"github.com/golang-jwt/jwt/v5"
	"github.com/justinas/nosurf"
	"possums-intake/internal/failureowner"
	"possums-intake/internal/ledger"
	"possums-intake/internal/siteauth"
)

type customerAuth interface {
	Login(context.Context, string) (string, siteauth.Principal, error)
	Validate(context.Context, string) (siteauth.Principal, error)
}

// Config supplies website session and transport policy.
type Config struct {
	Lifetime, IdleTimeout time.Duration
	ConcurrentRequests    int
	FailureObserver       failureowner.Observer
	Payments              TopupActions  // optional synthetic-only host capability
	Balances              BalanceReader // optional; never supplied by Lisp/browser input
	TLSOffloaded          bool          // owner guarantees an HTTPS-only ingress; never inferred from client headers
}
type Host struct {
	// admission coordinates commit/swap with snapshots and entered POST effects.
	admission    sync.RWMutex
	publish      sync.Mutex
	revision     *Revision
	previousCSS  string
	auth         customerAuth
	balances     BalanceReader
	payments     TopupActions
	fallback     http.Handler
	sessions     *scs.SessionManager
	store        *memstore.MemStore
	pages        http.Handler
	slots        chan struct{}
	observer     failureowner.Observer
	tlsOffloaded bool
}

// NewFixture uses stock volatile sessions without a cleanup worker. Expired
// entries are inaccessible but retained until this disposable host is released.
// Stop the listener and join requests before dropping the host. Restart loses
// sessions. This is not a long-running or production memory-bound contract.
func NewFixture(r *Revision, auth customerAuth, fallback http.Handler, c Config) (*Host, error) {
	if r == nil {
		return nil, bundleFault()
	}
	return newHost(r, auth, fallback, c, 0)
}

// New owns one stock memory-only session cleanup worker for the lifetime of the
// intake process. It starts closed; BindPublication must load persisted code.
// Restart loses sessions. There is no joined in-process teardown contract: the
// pinned memstore's StopCleanup is not used. One host is constructed per process.
func New(auth customerAuth, fallback http.Handler, c Config) (*Host, error) {
	return newHost(nil, auth, fallback, c, time.Minute)
}

func newHost(r *Revision, auth customerAuth, fallback http.Handler, c Config, cleanup time.Duration) (*Host, error) {
	if r != nil && !r.Ready() || auth == nil || fallback == nil || c.Lifetime <= 0 || c.IdleTimeout <= 0 || c.IdleTimeout > c.Lifetime || c.ConcurrentRequests <= 0 {
		return nil, bundleFault()
	}
	store := memstore.NewWithCleanupInterval(cleanup)
	sessions := &scs.SessionManager{Store: store, Codec: scs.GobCodec{}, Lifetime: c.Lifetime, IdleTimeout: c.IdleTimeout, Cookie: scs.SessionCookie{Name: "__Host-possums", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Persist: false}}
	h := &Host{revision: r, auth: auth, balances: c.Balances, payments: c.Payments, fallback: fallback, sessions: sessions, store: store, slots: make(chan struct{}, c.ConcurrentRequests), observer: c.FailureObserver, tlsOffloaded: c.TLSOffloaded}
	sessions.ErrorFunc = h.sessionError
	csrf := nosurf.New(http.HandlerFunc(h.route))
	csrf.SetIsTLSFunc(func(r *http.Request) bool { return h.tlsOffloaded || r.TLS != nil })
	csrf.SetBaseCookie(http.Cookie{Name: "__Host-possums-csrf", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 0})
	csrf.SetFailureHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.message(w, r, 403, httpFault("SITE_CSRF_REFUSED"))
	}))
	h.pages = sessions.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionBoundary(r, "commit")
		csrf.ServeHTTP(w, r)
	}))
	return h, nil
}
func customerHeaders(w http.ResponseWriter) {
	headers := w.Header()
	headers.Set("Cache-Control", "no-store")
	headers.Set("X-Content-Type-Options", "nosniff")
	headers.Set("Referrer-Policy", "same-origin")
	headers.Set("Content-Security-Policy", "default-src 'none'; script-src 'none'; connect-src 'none'; style-src 'self'; font-src 'self'; img-src 'none'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
}
func (h *Host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Native/accounting failures retain their existing independent owners.
	if reserved(r.URL.Path) && !topupPath(r.URL.Path) && !strings.HasPrefix(r.URL.Path, "/_site/") {
		h.fallback.ServeHTTP(w, r)
		return
	}
	state, r := h.ownResponse(w, r)
	w = state
	defer h.recoverHTTP(state, r)
	customerHeaders(w)
	refuse := func(status int, code string) { h.plain(w, status, h.report(r, httpFault(code))) }
	if r.URL.RawPath != "" || r.URL.RawQuery != "" || !strings.HasPrefix(r.URL.Path, "/") {
		refuse(400, "SITE_ROUTE_REFUSED")
		return
	}
	if r.TLS == nil && !h.tlsOffloaded {
		refuse(400, "SITE_HTTPS_REQUIRED")
		return
	}
	h.admission.RLock()
	revision, previousCSS := h.revision, h.previousCSS
	h.admission.RUnlock()
	if revision == nil {
		h.plain(w, 503, h.report(r, publicationFault("website_admission")))
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), revisionKey{}, revision))
	css := revision.CSS()
	if previousCSS != "" && r.URL.Path == cssPath(previousCSS) {
		css = previousCSS
	}
	if r.URL.Path == cssPath(css) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.Header().Set("Allow", "GET, HEAD")
			refuse(405, "SITE_METHOD_REFUSED")
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		if r.Method == "GET" {
			_, _ = io.WriteString(w, css)
		}
		return
	}
	if strings.HasPrefix(r.URL.Path, "/_site/") {
		h.asset(w, r)
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" && r.Method != "POST" {
		w.Header().Set("Allow", "GET, HEAD, POST")
		refuse(405, "SITE_METHOD_REFUSED")
		return
	}
	if r.Method == "POST" {
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			refuse(415, "SITE_FORM_REFUSED")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil {
			refuse(400, "SITE_FORM_REFUSED")
			return
		}
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		refuse(503, "SITE_BUSY")
		return
	}
	panicStage(r, "SITE_SESSION_UNAVAILABLE")
	sessionBoundary(r, "load")
	h.pages.ServeHTTP(w, r)
}
func (h *Host) route(w http.ResponseWriter, r *http.Request) {
	route, found := Route{}, false
	pathExists := false
	method := r.Method
	if method == "HEAD" {
		method = "GET"
	}
	if topupPath(r.URL.Path) {
		pathExists = true
		route, found = fixedTopupRoute(r.URL.Path, method)
	}
	for _, candidate := range requestRevision(r).Routes {
		if candidate.Path == r.URL.Path {
			pathExists = true
			if candidate.Method == method {
				route, found = candidate, true
				break
			}
		}
	}
	if !found {
		if pathExists {
			w.Header().Set("Allow", "GET, HEAD, POST")
			h.plain(w, 405, h.report(r, httpFault("SITE_METHOD_REFUSED")))
		} else {
			h.plain(w, 404, h.report(r, httpFault("SITE_PAGE_MISSING")))
		}
		return
	}
	if method == "POST" {
		// Publication waits only for already-entered POST effects. Stale forms
		// refuse before SDK validation or session mutation as well as login/logout.
		h.admission.RLock()
		defer h.admission.RUnlock()
		if h.revision != requestRevision(r) || r.PostForm.Get("revision") != requestRevision(r).Digest {
			h.message(w, r, 409, httpFault("SITE_REVISION_STALE"))
			return
		}
	}
	signed := false
	token := h.sessions.GetString(r.Context(), "credential")
	if token != "" {
		panicStage(r, "SITE_AUTH_UNAVAILABLE")
		if principal, err := h.auth.Validate(r.Context(), token); err == nil && principal.AccountID == h.sessions.GetString(r.Context(), "account_binding") && (h.balances == nil || principal.AccountID != "") {
			signed = true
			r = r.WithContext(context.WithValue(r.Context(), principalKey{}, principal))
		} else {
			d := httpFault("SITE_AUTH_REQUIRED")
			if err != nil {
				d = h.authFailure(err, "SITE_AUTH_UNAVAILABLE")
			}
			h.report(r, d)
			panicStage(r, "SITE_SESSION_UNAVAILABLE")
			setOutcome(r, "session_invalidation_unconfirmed")
			sessionBoundary(r, "delete")
			if e := h.sessions.Destroy(r.Context()); e != nil {
				h.message(w, r, 503, sessionFault(r, e))
				return
			}
			setOutcome(r, "session_destroyed_credential_revalidation_refused")
			if d.Code != "SITE_AUTH_REQUIRED" {
				h.messageObserved(w, r, 503, d)
				return
			}
		}
	}
	panicStage(r, "SITE_SESSION_UNAVAILABLE")
	if route.Policy == "session" && !signed {
		h.redirect(w, r, loginRouteFor(requestRevision(r)).Path)
		return
	}
	if topupPath(r.URL.Path) {
		h.topup(w, r)
		return
	}
	if method == "GET" {
		h.page(w, r, route, 200, "")
		return
	}
	allowed := map[string]bool{"csrf_token": true, "revision": true}
	if route.Action == "auth/login" {
		allowed["key"] = true
	}
	for key, values := range r.PostForm {
		if !allowed[key] || len(values) != 1 {
			h.message(w, r, 400, httpFault("SITE_FORM_REFUSED"))
			return
		}
	}
	destination, _ := requestRevision(r).Named(route.Success)
	switch route.Action {
	case "auth/login":
		key := r.PostForm.Get("key")
		if key == "" || len(key) > 255 || !utf8.ValidString(key) {
			h.message(w, r, 400, httpFault("SITE_FORM_REFUSED"))
			return
		}
		// Entered SDK work can have effects even when a hostile adapter panics.
		setOutcome(r, "sdk_auth_completion_unknown_no_session_granted")
		panicStage(r, "SITE_AUTH_UNAVAILABLE")
		token, principal, err := h.auth.Login(r.Context(), key)
		if err != nil {
			d := h.authFailure(err, "SITE_AUTH_UNAVAILABLE")
			status := 503
			if d.Code == "SITE_AUTH_REFUSED" {
				status = 401
			}
			if d.Code == "SITE_FORM_REFUSED" {
				status = 400
			}
			if d.Code == "SITE_AUTH_MFA_UNSUPPORTED" {
				status = 403
			}
			page, _ := requestRevision(r).Named(route.Error)
			notice := diagnosticText(h.report(r, d))
			if d.Code == "SITE_AUTH_REFUSED" {
				notice = "Invalid credentials. Please try again."
			}
			h.page(w, r, page, status, notice)
			return
		}
		setOutcome(r, "sdk_auth_completed_website_session_unconfirmed")
		// Expiry is read only after SDK verification; this does not verify a JWT.
		claims := jwt.MapClaims{}
		_, _, err = jwt.NewParser().ParseUnverified(token, claims)
		expiry, e := claims.GetExpirationTime()
		if err != nil || e != nil || expiry == nil || !expiry.After(time.Now()) {
			h.message(w, r, 503, httpFault("SITE_AUTH_UNAVAILABLE"))
			return
		}
		if h.balances != nil {
			if principal.AccountID == "" {
				h.message(w, r, 503, httpFault("SITE_AUTH_CONFIG"))
				return
			}
			// Establish the bound account before granting a website session.
			if _, d := h.accountSnapshot(r.Context(), principal.AccountID); d != nil {
				h.accountReadFailure(w, r, d)
				return
			}
		}
		panicStage(r, "SITE_SESSION_UNAVAILABLE")
		sessionBoundary(r, "renew")
		if err = h.sessions.RenewToken(r.Context()); err != nil {
			h.message(w, r, 503, sessionFault(r, err))
			return
		}
		if expiry.Before(h.sessions.Deadline(r.Context())) {
			h.sessions.SetDeadline(r.Context(), expiry.Time)
		}
		h.sessions.Put(r.Context(), "credential", token)
		h.sessions.Put(r.Context(), "account_binding", principal.AccountID)
	case "auth/logout":
		// This capability is protected independently of trusted Lisp page policy.
		if !signed {
			h.message(w, r, 401, httpFault("SITE_AUTH_REQUIRED"))
			return
		}
		setOutcome(r, "logout_completion_unknown")
		sessionBoundary(r, "delete")
		if err := h.sessions.Destroy(r.Context()); err != nil {
			h.message(w, r, 503, sessionFault(r, err))
			return
		}
		setOutcome(r, "session_destroyed_not_credential_revocation")
	default:
		h.message(w, r, 403, httpFault("SITE_ACTION_REFUSED"))
		return
	}
	h.redirect(w, r, destination.Path)
}

// Only locally authored SDK codes select messages. Preserve numeric categories,
// never upstream diagnostic strings/fields. This owner observes propagation once.
func (h *Host) authFailure(err error, fallback string) (result *ledger.Diagnostic) {
	defer func() {
		if recover() != nil {
			result = httpFault(fallback)
		}
	}()
	code := fallback
	var d *ledger.Diagnostic
	if errors.As(err, &d) {
		switch d.Code {
		case "SITE_AUTH_REFUSED":
			code = "SITE_AUTH_REFUSED"
		case "SITE_AUTH_INVALID":
			code = "SITE_AUTH_REQUIRED"
		case "SITE_AUTH_MFA_UNSUPPORTED":
			code = "SITE_AUTH_MFA_UNSUPPORTED"
		case "SITE_AUTH_CONFIG":
			code = "SITE_AUTH_CONFIG"
		case "SITE_AUTH_INTERRUPTED":
			code = "SITE_AUTH_INTERRUPTED"
		case "SITE_AUTH_INPUT":
			code = "SITE_FORM_REFUSED"
		}
	}
	local := httpFault(code)
	if d != nil {
		switch d.Stage {
		case "site_auth_config", "site_auth_dispatch", "site_auth_final_response", "site_auth_login", "site_auth_rule", "site_auth_setup", "site_auth_token_lookup", "site_auth_validate":
			local.Stage = d.Stage
		}
	}
	clean := ledger.CanonicalFailure(err, local.Kind())
	clean.Code, clean.Stage, clean.Constraint, clean.Outcome, clean.NextStep = local.Code, local.Stage, local.Constraint, local.Outcome, local.NextStep
	return clean
}
func (h *Host) loginRoute() Route { return loginRouteFor(h.revision) }
func loginRouteFor(revision *Revision) Route {
	for _, route := range revision.Routes {
		if route.Action == "auth/login" {
			page, _ := revision.Named(route.Error)
			return page
		}
	}
	return Route{Path: "/"}
}
func (h *Host) redirect(w http.ResponseWriter, r *http.Request, path string) {
	sessionBoundary(r, "commit")
	w.Header().Set("Location", path)
	w.WriteHeader(http.StatusSeeOther)
}
func (h *Host) page(w http.ResponseWriter, r *http.Request, route Route, status int, notice string) {
	panicStage(r, "SITE_RENDER_UNAVAILABLE")
	var view map[string]string
	if h.balances != nil && route.Policy == "session" {
		principal, ok := r.Context().Value(principalKey{}).(siteauth.Principal)
		if !ok || principal.AccountID == "" {
			h.plain(w, 401, h.report(r, httpFault("SITE_AUTH_REQUIRED")))
			return
		}
		setOutcome(r, "account_snapshot_completion_unknown")
		snapshot, d := h.accountSnapshot(r.Context(), principal.AccountID)
		if d != nil {
			h.accountReadFailure(w, r, d)
			return
		}
		setOutcome(r, "account_snapshot_received_display_unconfirmed")
		var err error
		view, err = snapshotView(principal.AccountID, snapshot)
		if err != nil {
			h.renderFailure(w, r, err)
			return
		}
		setOutcome(r, "account_snapshot_confirmed_display_unconfirmed")
	}
	page, err := requestRevision(r).PageWithView(r.Context(), route.Entry, view)
	if err != nil {
		if view != nil && renderHTTPFault(err).Code == "SITE_PAGE_TEXT_LIMIT" {
			err = accountViewLimit()
		}
		h.renderFailure(w, r, err)
		return
	}
	body, err := requestRevision(r).render(page, nosurf.Token(r), notice, hostBindings{paymentsEnabled: h.payments != nil})
	if err != nil {
		h.renderFailure(w, r, err)
		return
	}
	panicStage(r, "SITE_SESSION_UNAVAILABLE")
	sessionBoundary(r, "commit")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if r.Method != "HEAD" {
		_, _ = w.Write(body)
	}
}
func (h *Host) renderFailure(w http.ResponseWriter, r *http.Request, err error) {
	d := h.report(r, renderHTTPFault(err))
	panicStage(r, "SITE_SESSION_UNAVAILABLE")
	sessionBoundary(r, "commit")
	h.plain(w, 503, d)
}
func (h *Host) message(w http.ResponseWriter, r *http.Request, status int, d *ledger.Diagnostic) {
	h.messageObserved(w, r, status, h.report(r, d))
}
func (h *Host) messageObserved(w http.ResponseWriter, r *http.Request, status int, d *ledger.Diagnostic) {
	copy := *d
	if s := responseFor(r); s != nil {
		copy.Outcome = s.outcome
	}
	h.page(w, r, loginRouteFor(requestRevision(r)), status, diagnosticText(&copy))
}
func (h *Host) plain(w http.ResponseWriter, status int, d *ledger.Diagnostic) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, diagnosticText(d)+"\n")
}
