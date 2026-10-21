// Package ledger owns accounting. Callers must authenticate before invoking it.
// No caller should infer a requested effect from a terminal row of another state.
package ledger

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"time"
	"unicode/utf8"
)

const (
	WriterWait         = 2 * time.Second
	WorkBudget         = time.Second
	BusyWaitMS         = 250
	KeyWindowMS  int64 = 86_400_000
	FutureSkewMS int64 = 60_000
	CleanupLimit       = 128
)

// Money is canonical decimal uint64 microunits. Public inputs are validated again
// by the service; a Go string conversion alone is not validation.
type Money string

var moneyPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,19})$`)

func ParseMoney(s string) (Money, error) {
	if _, err := Money(s).units(); err != nil {
		return "", err
	}
	return Money(s), nil
}
func (m Money) units() (uint64, error) {
	if !moneyPattern.MatchString(string(m)) {
		return 0, diagnostic(CodeInput)
	}
	units, err := strconv.ParseUint(string(m), 10, 64)
	if err != nil {
		return 0, diagnostic(CodeInput)
	}
	return units, nil
}
func add(a, b uint64) (uint64, error) {
	if math.MaxUint64-a < b {
		return 0, diagnostic(CodeOverflow)
	}
	return a + b, nil
}
func money(n uint64) Money { return Money(strconv.FormatUint(n, 10)) }

// Millis is a UTC epoch millisecond on disk, fixed UTC text on the wire.
type Millis int64

func (m Millis) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.UnixMilli(int64(m)).UTC().Format("2006-01-02T15:04:05.000Z"))
}

type State string

const (
	Active   State = "active"
	Settled  State = "settled"
	Refunded State = "refunded"
	Expired  State = "expired"
)

type Account struct {
	AccountID string `db:"account_id" json:"account_id"`
	Balance   Money  `db:"balance" json:"balance"`
	Enabled   bool   `db:"enabled" json:"enabled"`
}
type Reservation struct {
	ReservationID    string  `db:"reservation_id" json:"reservation_id"`
	IdempotencyKey   string  `db:"idempotency_key" json:"idempotency_key"`
	AccountID        string  `db:"account_id" json:"account_id"`
	Maximum          Money   `db:"maximum" json:"maximum"`
	TimeoutSeconds   int64   `db:"timeout_seconds" json:"timeout_seconds"`
	InitialExpiresAt Millis  `db:"initial_expires_at" json:"initial_expires_at"`
	ExpiresAt        Millis  `db:"expires_at" json:"expires_at"`
	State            State   `db:"state" json:"state"`
	ChargedAmount    *Money  `db:"charged_amount" json:"charged_amount"`
	TerminalAt       *Millis `db:"terminal_at" json:"terminal_at"`
	RetainUntil      *Millis `db:"retain_until" json:"retain_until"`
}
type Credit struct {
	IdempotencyKey string `db:"idempotency_key" json:"idempotency_key"`
	AccountID      string `db:"account_id" json:"account_id"`
	Amount         Money  `db:"amount" json:"amount"`
	BalanceAfter   Money  `db:"balance_after" json:"balance_after"`
	CommittedAt    Millis `db:"committed_at" json:"committed_at"`
	RetainUntil    Millis `db:"retain_until" json:"retain_until"`
}
type BalanceSnapshot struct {
	AccountBalance      Account       `json:"account_balance"`
	PendingReservations []Reservation `json:"pending_reservations"`
	Available           Money         `json:"available"`
}
type CreditInput struct {
	AccountID      string
	Amount         Money
	IdempotencyKey string
}
type ReserveInput struct {
	AccountID      string
	Maximum        Money
	TimeoutSeconds int64
	IdempotencyKey string
}
type SettleInput struct {
	ReservationID string
	ChargedAmount Money
}
type RenewInput struct {
	ReservationID     string
	ExpectedExpiresAt Millis
}
type Renewal struct {
	Reservation Reservation `json:"reservation"`
	ObservedAt  Millis      `json:"observed_at"`
}
type CleanupResult struct {
	Expired int
	Pruned  int
}

// Accounting is the route packet's complete core dependency. Errors are closed
// *Diagnostic values. Settle may return an authoritative expired row AND an
// expired-settlement diagnostic. Renew may return a committed snapshot AND a
// known refusal; transaction completion failures return no usable receipt.
type Accounting interface {
	Provision(context.Context, string) (Account, error)
	Credit(context.Context, CreditInput) (Credit, error)
	Reserve(context.Context, ReserveInput) (Reservation, error)
	Renew(context.Context, RenewInput) (Renewal, error)
	Settle(context.Context, SettleInput) (Reservation, error)
	Refund(context.Context, string) (Reservation, error)
	Balance(context.Context, string) (BalanceSnapshot, error)
	Lookup(context.Context, string) (Reservation, error)
	Cleanup(context.Context, bool) (CleanupResult, error)
}

var _ Accounting = (*Service)(nil)

var keyPattern = regexp.MustCompile(`^v1\.[0-9]{13}\.[0-9a-f]{32}$`)
var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func keyIssued(key string) (int64, error) {
	if !keyPattern.MatchString(key) {
		return 0, diagnostic(CodeKey)
	}
	issued, err := strconv.ParseInt(key[3:16], 10, 64)
	if err != nil {
		return 0, diagnostic(CodeKey)
	}
	return issued, nil
}
func validNewKey(issued, now int64) error {
	if issued > now+FutureSkewMS || now >= issued+KeyWindowMS {
		return diagnostic(CodeKeyWindow)
	}
	return nil
}
func validAccount(id string) bool {
	return len(id) >= 1 && len(id) <= 128 && utf8.ValidString(id)
}
func retention(issued, terminal int64) Millis {
	return Millis(max(issued+KeyWindowMS, terminal+KeyWindowMS))
}
