// Package httpapi owns the bounded custom HTTP surface for the ledger. It
// parses/validates wire input, authenticates one shared runtime key and maps
// closed ledger diagnostics to pinned statuses. It makes no accounting
// decisions, performs no SQL, retries or scheduling, and never
// treats HTTP delivery as proof of a monetary effect.
package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"possums-intake/internal/ledger"
	"possums-intake/internal/telemetry"
)

// MinAdminKeyBytes is the required decoded length of the raw runtime admin key.
// The accepted wire encoding is a raw string of at least this many bytes.
const MinAdminKeyBytes = 32

// MaxBodyBytes bounds every accounting request body. Longer bodies are rejected,
// not truncated.
const MaxBodyBytes = 4 * 1024

type diagnostic struct {
	Code       string `json:"code"`
	Stage      string `json:"stage"`
	Constraint string `json:"constraint"`
	Outcome    string `json:"outcome"`
	NextStep   string `json:"next_step"`
}

type accountJSON struct {
	AccountID string `json:"account_id"`
	Balance   string `json:"balance"`
	Enabled   bool   `json:"enabled"`
}

type reservationJSON struct {
	ReservationID  string  `json:"reservation_id"`
	IdempotencyKey string  `json:"idempotency_key"`
	AccountID      string  `json:"account_id"`
	Maximum        string  `json:"maximum"`
	TimeoutSeconds int64   `json:"timeout_seconds"`
	ExpiresAt      string  `json:"expires_at"`
	State          string  `json:"state"`
	ChargedAmount  *string `json:"charged_amount"`
	TerminalAt     *string `json:"terminal_at"`
	RetainUntil    *string `json:"retain_until"`
}

type creditJSON struct {
	IdempotencyKey string `json:"idempotency_key"`
	AccountID      string `json:"account_id"`
	Amount         string `json:"amount"`
	BalanceAfter   string `json:"balance_after"`
	CommittedAt    string `json:"committed_at"`
	RetainUntil    string `json:"retain_until"`
}

type accountResponse struct {
	AccountBalance []accountJSON `json:"account_balance"`
}

type creditResponse struct {
	Credit creditJSON `json:"credit"`
}

type reservationResponse struct {
	Reservation reservationJSON `json:"reservation"`
}

type balanceResponse struct {
	AccountBalance      []accountJSON     `json:"account_balance"`
	PendingReservations []reservationJSON `json:"pending_reservations"`
	Available           string            `json:"available"`
}

// expiredSettlementResponse preserves the authoritative expired reservation
// alongside the closed settlement diagnostic. The reservation is not the
// requested effect; callers must inspect its state.
type expiredSettlementResponse struct {
	Code        string          `json:"code"`
	Stage       string          `json:"stage"`
	Constraint  string          `json:"constraint"`
	Outcome     string          `json:"outcome"`
	NextStep    string          `json:"next_step"`
	Reservation reservationJSON `json:"reservation"`
}

// TerminalObserver receives only closed HTTP classes and transient durations,
// outside accounting transactions. It is not financial-effect evidence.
type TerminalObserver interface {
	ObserveTerminal(telemetry.StatusClass, time.Duration)
	InvalidateRequests()
}

type handler struct {
	accounting ledger.Accounting
	ready      func() bool
	key        []byte
	observer   TerminalObserver
	routes     map[string]http.HandlerFunc
}

// NewHandler builds the fixed custom-only mux. accounting performs all
// accounting; ready reports cached readiness without accounting work; adminKey
// is the raw shared runtime key. An invalid key returns a locally authored,
// content-free diagnostic.
func NewHandler(accounting ledger.Accounting, ready func() bool, adminKey string, observer TerminalObserver) (http.Handler, error) {
	if accounting == nil {
		return nil, errors.New("LEDGER_HTTP_DEPENDENCY_INVALID")
	}
	if len(adminKey) < MinAdminKeyBytes {
		return nil, errors.New("LEDGER_RUNTIME_KEY_INVALID")
	}
	h := &handler{
		accounting: accounting,
		ready:      ready,
		key:        []byte(adminKey),
		observer:   observer,
	}
	h.routes = map[string]http.HandlerFunc{
		"/accounts/provision":  h.provision,
		"/credits":             h.credit,
		"/reservations":        h.reserve,
		"/reservations/settle": h.settle,
		"/reservations/refund": h.refund,
		"/reservations/lookup": h.lookup,
		"/balance":             h.balance,
	}
	return h, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Buffer the complete closed response so an unexpected panic inside a route
	// can be replaced by a safe unknown-outcome status instead of a partial or
	// framework-rendered error. No raw exception text ever reaches the client.
	buffered := &responseBuffer{header: http.Header{}}
	_, custom := h.routes[r.URL.Path]
	custom = custom && r.Method == http.MethodPost
	complete := terminalCompletion(h.observer, time.Now())
	defer func() {
		panicked := recover() != nil
		if panicked {
			buffered.reset()
			writeDiagnostic(buffered, http.StatusServiceUnavailable, diagUnknown)
		}
		classified := buffered.wroteHeader && len(buffered.body) > 0
		if !classified {
			buffered.reset()
			writeDiagnostic(buffered, http.StatusServiceUnavailable, diagUnknown)
			if custom {
				invalidateRequests(h.observer)
			}
		}
		delivered := buffered.flush(w)
		if custom {
			class := telemetry.Unknown
			if classified && !panicked && delivered && r.Context().Err() == nil {
				class = statusClass(buffered.status)
			}
			complete(class)
		}
	}()
	h.route(buffered, r)
}

func (h *handler) route(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if r.URL.Path == "/healthz" {
			h.health(w, r)
			return
		}
	case http.MethodPost:
		if route, ok := h.routes[r.URL.Path]; ok {
			h.accountingRequest(route).ServeHTTP(w, r)
			return
		}
	}
	h.notFound(w)
}

// accountingRequest applies the shared authentication and body contract before
// the route handler runs. Every custom accounting route, including reads,
// requires the same bearer key.
func (h *handler) accountingRequest(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if headerValues(r.Header, "Authorization") > 1 || headerValues(r.Header, "Content-Type") > 1 || headerValues(r.Header, "Content-Encoding") > 1 || headerValues(r.Header, "Idempotency-Key") > 1 {
			writeDiagnostic(w, http.StatusBadRequest, diagDuplicateHeader)
			return
		}
		if !h.authorized(r) {
			writeDiagnostic(w, http.StatusUnauthorized, diagUnauthorized)
			return
		}
		if r.URL.RawQuery != "" {
			writeDiagnostic(w, http.StatusBadRequest, diagQuery)
			return
		}
		if encoding := headerValue(r.Header, "Content-Encoding"); encoding != "" && encoding != "identity" {
			writeDiagnostic(w, http.StatusBadRequest, diagContentEncoding)
			return
		}
		if headerValue(r.Header, "Content-Type") != "application/json" {
			writeDiagnostic(w, http.StatusBadRequest, diagContentType)
			return
		}
		next(w, r)
	})
}

func (h *handler) authorized(r *http.Request) bool {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return false
	}
	prefix := "Bearer "
	if len(values[0]) <= len(prefix) || values[0][:len(prefix)] != prefix {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(values[0][len(prefix):]), h.key) == 1
}

// health reports fixed cached readiness. It performs no accounting work and
// exposes no detail, identifiers or framework state.
func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	if h.ready != nil && !h.ready() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// notFound is the closed response for unknown paths, wrong methods and any
// native/framework route. It never reveals installer/dashboard/CRUD surfaces.
func (h *handler) notFound(w http.ResponseWriter) {
	writeDiagnostic(w, http.StatusNotFound, diagNotFound)
}

func (h *handler) provision(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccountID string `json:"account_id"`
	}
	if !h.decode(w, r, &req, true) {
		return
	}
	account, err := h.accounting.Provision(r.Context(), req.AccountID)
	if err != nil {
		h.writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, accountResponse{AccountBalance: []accountJSON{toAccount(account)}})
}

func (h *handler) credit(w http.ResponseWriter, r *http.Request) {
	if headerValues(r.Header, "Idempotency-Key") != 1 {
		writeDiagnostic(w, http.StatusBadRequest, diagIdempotency)
		return
	}
	key := headerValue(r.Header, "Idempotency-Key")
	var req struct {
		AccountID string `json:"account_id"`
		Amount    string `json:"amount"`
	}
	if !h.decode(w, r, &req, true) {
		return
	}
	amount, err := ledger.ParseMoney(req.Amount)
	if err != nil || amount == "0" {
		writeDiagnostic(w, http.StatusBadRequest, diagInput)
		return
	}
	credit, err := h.accounting.Credit(r.Context(), ledger.CreditInput{
		AccountID:      req.AccountID,
		Amount:         amount,
		IdempotencyKey: key,
	})
	if err != nil {
		h.writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, creditResponse{Credit: toCredit(credit)})
}

func (h *handler) reserve(w http.ResponseWriter, r *http.Request) {
	if headerValues(r.Header, "Idempotency-Key") != 1 {
		writeDiagnostic(w, http.StatusBadRequest, diagIdempotency)
		return
	}
	key := headerValue(r.Header, "Idempotency-Key")
	var req struct {
		AccountID      string `json:"account_id"`
		Maximum        string `json:"maximum"`
		TimeoutSeconds *int64 `json:"timeout_seconds"`
	}
	if !h.decode(w, r, &req, true) {
		return
	}
	if req.TimeoutSeconds == nil || *req.TimeoutSeconds < 1 || *req.TimeoutSeconds > 1800 {
		writeDiagnostic(w, http.StatusBadRequest, diagInput)
		return
	}
	maximum, err := ledger.ParseMoney(req.Maximum)
	if err != nil || maximum == "0" {
		writeDiagnostic(w, http.StatusBadRequest, diagInput)
		return
	}
	reservation, err := h.accounting.Reserve(r.Context(), ledger.ReserveInput{
		AccountID:      req.AccountID,
		Maximum:        maximum,
		TimeoutSeconds: *req.TimeoutSeconds,
		IdempotencyKey: key,
	})
	if err != nil {
		h.writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reservationResponse{Reservation: toReservation(reservation)})
}

func (h *handler) settle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ReservationID string `json:"reservation_id"`
		ChargedAmount string `json:"charged_amount"`
	}
	if !h.decode(w, r, &req, true) {
		return
	}
	charged, err := ledger.ParseMoney(req.ChargedAmount)
	if err != nil {
		writeDiagnostic(w, http.StatusBadRequest, diagInput)
		return
	}
	reservation, err := h.accounting.Settle(r.Context(), ledger.SettleInput{
		ReservationID: req.ReservationID,
		ChargedAmount: charged,
	})
	if d, ok := err.(*ledger.Diagnostic); ok && d.Code == ledger.CodeExpired {
		// Authoritative expired row is preserved separately from the diagnostic.
		writeJSON(w, statusFor(d.Code), expiredSettlementResponse{
			Code: string(d.Code), Stage: d.Stage, Constraint: d.Constraint,
			Outcome: d.Outcome, NextStep: d.NextStep, Reservation: toReservation(reservation),
		})
		return
	}
	if err != nil {
		h.writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reservationResponse{Reservation: toReservation(reservation)})
}

func (h *handler) refund(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ReservationID string `json:"reservation_id"`
	}
	if !h.decode(w, r, &req, true) {
		return
	}
	reservation, err := h.accounting.Refund(r.Context(), req.ReservationID)
	if err != nil {
		h.writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reservationResponse{Reservation: toReservation(reservation)})
}

func (h *handler) lookup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ReservationID string `json:"reservation_id"`
	}
	if !h.decode(w, r, &req, true) {
		return
	}
	reservation, err := h.accounting.Lookup(r.Context(), req.ReservationID)
	if err != nil {
		h.writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reservationResponse{Reservation: toReservation(reservation)})
}

func (h *handler) balance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccountID string `json:"account_id"`
	}
	if !h.decode(w, r, &req, true) {
		return
	}
	snapshot, err := h.accounting.Balance(r.Context(), req.AccountID)
	if err != nil {
		h.writeFailure(w, err)
		return
	}
	// The core returns one account object; the wire contract is a list of at
	// most one account. Pending rows are always present, never null.
	response := balanceResponse{
		AccountBalance:      []accountJSON{toAccount(snapshot.AccountBalance)},
		PendingReservations: make([]reservationJSON, 0, len(snapshot.PendingReservations)),
		Available:           string(snapshot.Available),
	}
	for _, pending := range snapshot.PendingReservations {
		response.PendingReservations = append(response.PendingReservations, toReservation(pending))
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *handler) writeFailure(w http.ResponseWriter, err error) {
	d, ok := err.(*ledger.Diagnostic)
	if !ok {
		writeDiagnostic(w, http.StatusServiceUnavailable, diagUnknown)
		return
	}
	writeJSON(w, statusFor(d.Code), diagnostic{
		Code: string(d.Code), Stage: d.Stage, Constraint: d.Constraint,
		Outcome: d.Outcome, NextStep: d.NextStep,
	})
}

func headerValue(header http.Header, name string) string {
	values := header.Values(name)
	if len(values) != 1 {
		return ""
	}
	return values[0]
}

func headerValues(header http.Header, name string) int {
	return len(header.Values(name))
}

func toAccount(a ledger.Account) accountJSON {
	return accountJSON{AccountID: a.AccountID, Balance: string(a.Balance), Enabled: a.Enabled}
}

func toCredit(c ledger.Credit) creditJSON {
	return creditJSON{
		IdempotencyKey: c.IdempotencyKey,
		AccountID:      c.AccountID,
		Amount:         string(c.Amount),
		BalanceAfter:   string(c.BalanceAfter),
		CommittedAt:    formatMillis(c.CommittedAt),
		RetainUntil:    formatMillis(c.RetainUntil),
	}
}

func toReservation(r ledger.Reservation) reservationJSON {
	out := reservationJSON{
		ReservationID:  r.ReservationID,
		IdempotencyKey: r.IdempotencyKey,
		AccountID:      r.AccountID,
		Maximum:        string(r.Maximum),
		TimeoutSeconds: r.TimeoutSeconds,
		ExpiresAt:      formatMillis(r.ExpiresAt),
		State:          string(r.State),
	}
	if r.ChargedAmount != nil {
		charged := string(*r.ChargedAmount)
		out.ChargedAmount = &charged
	}
	if r.TerminalAt != nil {
		terminal := formatMillis(*r.TerminalAt)
		out.TerminalAt = &terminal
	}
	if r.RetainUntil != nil {
		retain := formatMillis(*r.RetainUntil)
		out.RetainUntil = &retain
	}
	return out
}

func formatMillis(m ledger.Millis) string {
	return time.UnixMilli(int64(m)).UTC().Format("2006-01-02T15:04:05.000Z")
}

func writeDiagnostic(w http.ResponseWriter, status int, d diagnostic) {
	writeJSON(w, status, d)
}

// responseBuffer captures the complete closed response before it is sent. It
// lets the top-level recover replace a partial response after a panic.
type responseBuffer struct {
	header      http.Header
	status      int
	body        []byte
	wroteHeader bool
}

func (b *responseBuffer) Header() http.Header { return b.header }

func (b *responseBuffer) WriteHeader(status int) {
	// First write wins; a route never emits two conflicting statuses.
	if !b.wroteHeader {
		b.status = status
		b.wroteHeader = true
	}
}

func (b *responseBuffer) Write(payload []byte) (int, error) {
	b.body = append(b.body, payload...)
	return len(payload), nil
}

// reset discards a partial response so a panic can be replaced by one safe,
// complete diagnostic.
func (b *responseBuffer) reset() {
	b.header = http.Header{}
	b.status = 0
	b.body = nil
	b.wroteHeader = false
}

func (b *responseBuffer) flush(w http.ResponseWriter) (delivered bool) {
	// A writer can panic as well as return an error. Neither is a receipt, and
	// neither may escape to net/http's raw panic reporter.
	defer func() {
		if recover() != nil {
			delivered = false
		}
	}()
	for key, values := range b.header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(b.status)
	n, err := w.Write(b.body)
	if err != nil || n != len(b.body) {
		return false
	}
	// net/http can buffer small JSON writes. Surface a supported final flush
	// error before observation; even a successful flush is not a peer receipt.
	err = http.NewResponseController(w).Flush()
	return err == nil || errors.Is(err, http.ErrNotSupported)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		// Locally authored values always marshal; never emit a partial or raw
		// error. This is an unexpected local failure, not an accounting outcome.
		body = []byte(`{"code":"LEDGER_HTTP_RESPONSE_UNAVAILABLE","stage":"response","constraint":"serializable_closed_response","outcome":"unavailable","next_step":"Response construction failed; cause unknown. Delivery is not an accounting receipt."}`)
		status = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
