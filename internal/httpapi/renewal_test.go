package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"possums-intake/internal/ledger"
)

func renewalFixture(t *testing.T) (*ledger.Service, http.Handler, ledger.Reservation) {
	t.Helper()
	s := newTestLedger(t)
	h := newTestHandler(t, s, s.Ready)
	provision(t, h, "renewal-account")
	credit(t, h, "renewal-account", "100", 1)
	r, err := s.Reserve(testContext, ledger.ReserveInput{AccountID: "renewal-account", Maximum: "40", TimeoutSeconds: 1800, IdempotencyKey: key(2)})
	if err != nil {
		t.Fatal("synthetic renewal reserve unavailable")
	}
	return s, h, r
}

func advanceRenewalClock(t *testing.T, s *ledger.Service, at ledger.Millis) {
	t.Helper()
	app, err := s.CustomerAuthApp()
	if err != nil {
		t.Fatal("synthetic renewal clock app unavailable")
	}
	if _, err := app.DB().NewQuery("UPDATE ledger_meta SET clock_high_water_ms={:now} WHERE singleton=1").Bind(dbx.Params{"now": int64(at)}).Execute(); err != nil {
		t.Fatal("synthetic renewal clock unavailable")
	}
}

func renewBody(r ledger.Reservation, expected ledger.Millis) string {
	return fmt.Sprintf(`{"reservation_id":%q,"expected_expires_at":%q}`, r.ReservationID, formatMillis(expected))
}

func TestRenewalHTTPConditionalCurrentSnapshotAndTerminals(t *testing.T) {
	s, h, original := renewalFixture(t)
	advanceRenewalClock(t, s, original.ExpiresAt-1_200_000)
	out := postJSON(t, h, "/reservations/renew", renewBody(original, original.ExpiresAt))
	requireStatus(t, out, 200)
	body := decodeMap(t, out)
	exactKeys(t, body, "reservation", "observed_at")
	row := body["reservation"].(map[string]any)
	exactKeys(t, row, "reservation_id", "idempotency_key", "account_id", "maximum", "timeout_seconds", "initial_expires_at", "expires_at", "state", "charged_amount", "terminal_at", "retain_until")
	if row["initial_expires_at"] != formatMillis(original.ExpiresAt) || row["state"] != "active" || row["maximum"] != "40" || row["idempotency_key"] != original.IdempotencyKey {
		t.Fatal("renewal changed original binding")
	}
	first, err := s.Lookup(testContext, original.ReservationID)
	if err != nil || first.ExpiresAt != original.ExpiresAt+600_000 || body["observed_at"] != formatMillis(first.ExpiresAt-1_800_000) {
		t.Fatal("renewal exceeded authoritative lease interval")
	}
	advanceRenewalClock(t, s, first.ExpiresAt-1_200_000)
	out = postJSON(t, h, "/reservations/renew", renewBody(first, first.ExpiresAt))
	requireStatus(t, out, 200)
	newest := decodeMap(t, out)["reservation"].(map[string]any)
	for range 3 {
		out = postJSON(t, h, "/reservations/renew", renewBody(original, original.ExpiresAt))
		requireStatus(t, out, 200)
		if decodeMap(t, out)["reservation"].(map[string]any)["expires_at"] != newest["expires_at"] {
			t.Fatal("delayed duplicate changed latest lease")
		}
	}
	latest, err := s.Lookup(testContext, original.ReservationID)
	if err != nil {
		t.Fatal("latest reservation unavailable")
	}
	out = postJSON(t, h, "/reservations/renew", renewBody(latest, latest.ExpiresAt+1))
	requireStatus(t, out, 409)
	if decodeMap(t, out)["code"] != string(ledger.CodeRenewalPrecondition) {
		t.Fatal("future expectation did not preserve precondition code")
	}
	out = postJSON(t, h, "/reservations/renew", renewBody(latest, latest.InitialExpiresAt-1))
	requireStatus(t, out, 400)
	app, _ := s.CustomerAuthApp()
	if _, err := app.DB().NewQuery("UPDATE ledger_accounts SET enabled=0").Execute(); err != nil {
		t.Fatal("synthetic disable unavailable")
	}
	out = postJSON(t, h, "/reservations/renew", renewBody(original, original.ExpiresAt))
	requireStatus(t, out, 409)
	if decodeMap(t, out)["code"] != string(ledger.CodeDisabled) || decodeMap(t, out)["reservation"].(map[string]any)["state"] != "active" {
		t.Fatal("disabled duplicate changed or confirmed renewal")
	}
	balance, err := s.Balance(testContext, original.AccountID)
	if err != nil || balance.AccountBalance.Balance != "100" || balance.Available != "60" {
		t.Fatal("renewal changed funds")
	}
	out = postJSON(t, h, "/reservations/settle", fmt.Sprintf(`{"reservation_id":%q,"charged_amount":"17"}`, original.ReservationID))
	requireStatus(t, out, 200)
	out = postJSON(t, h, "/reservations/renew", renewBody(latest, latest.ExpiresAt))
	requireStatus(t, out, 409)
	body = decodeMap(t, out)
	exactKeys(t, body, "code", "stage", "constraint", "outcome", "next_step", "reservation", "observed_at")
	if body["code"] != string(ledger.CodeRenewalClosed) || body["outcome"] != "terminal" || body["reservation"].(map[string]any)["charged_amount"] != "17" {
		t.Fatal("renewal erased authoritative terminal")
	}
}

func TestRenewalHTTPExactExpiryCommitsAndCannotCollectLate(t *testing.T) {
	s, h, r := renewalFixture(t)
	advanceRenewalClock(t, s, r.ExpiresAt)
	out := postJSON(t, h, "/reservations/renew", renewBody(r, r.ExpiresAt))
	requireStatus(t, out, 409)
	body := decodeMap(t, out)
	if body["code"] != string(ledger.CodeRenewalClosed) || body["reservation"].(map[string]any)["state"] != "expired" {
		t.Fatal("due hold was renewed")
	}
	out = postJSON(t, h, "/reservations/settle", fmt.Sprintf(`{"reservation_id":%q,"charged_amount":"17"}`, r.ReservationID))
	requireStatus(t, out, 409)
	if decodeMap(t, out)["code"] != string(ledger.CodeExpired) {
		t.Fatal("late settlement code changed")
	}
	balance, err := s.Balance(testContext, r.AccountID)
	if err != nil || balance.Available != "100" || balance.AccountBalance.Balance != "100" {
		t.Fatal("expiry failed to release hold without charge")
	}
}

func TestRenewalHTTPStrictTimestampAndPrivacy(t *testing.T) {
	h := newTestHandler(t, panicLedger{}, nil)
	for _, stamp := range []string{"", "SYNTHETIC_PRIVATE_SENTINEL", "2030-01-01T00:00:00Z", "2030-01-01T00:00:00.000+00:00", "2030-01-01T00:00:00,000Z", "2030-01-01T00:00:00.0000Z", "2030-02-30T00:00:00.000Z"} {
		out := postJSON(t, h, "/reservations/renew", fmt.Sprintf(`{"reservation_id":%q,"expected_expires_at":%q}`, strings.Repeat("a", 32), stamp))
		requireStatus(t, out, 400)
		closedDiagnostic(t, out)
	}
	for _, body := range []string{`{}`, `{"reservation_id":"a"}`, `{"reservation_id":"a","expected_expires_at":null}`, `{"reservation_id":"a","expected_expires_at":1}`, `{"reservation_id":"a","expected_expires_at":"x","expected_expires_at":"y"}`} {
		out := postJSON(t, h, "/reservations/renew", body)
		requireStatus(t, out, 400)
		closedDiagnostic(t, out)
	}
	body := `{"reservation_id":"` + strings.Repeat("a", 32) + `","expected_expires_at":"2030-01-01T00:00:00.000Z"}`
	out := postJSON(t, h, "/reservations/renew", body)
	requireStatus(t, out, 503)
	if closedDiagnostic(t, out)["outcome"] != "unknown" {
		t.Fatal("panic fabricated renewal outcome")
	}
}

type lostRenewalReply struct {
	ledger.Accounting
	calls int
}

func (a *lostRenewalReply) Renew(ctx context.Context, input ledger.RenewInput) (ledger.Renewal, error) {
	a.calls++
	if _, err := a.Accounting.Renew(ctx, input); err != nil {
		return ledger.Renewal{}, err
	}
	return ledger.Renewal{}, errors.New("SYNTHETIC_PRIVATE_SENTINEL reply lost")
}

func TestRenewalHTTPUnknownDeliveryNeverRetriesOrExports(t *testing.T) {
	s, _, r := renewalFixture(t)
	advanceRenewalClock(t, s, r.ExpiresAt-1_200_000)
	a := &lostRenewalReply{Accounting: s}
	spy := &failureSpy{}
	h := observedHandler(t, a, spy)
	out := postJSON(t, h, "/reservations/renew", renewBody(r, r.ExpiresAt))
	requireStatus(t, out, 503)
	if closedDiagnostic(t, out)["outcome"] != "unknown" || a.calls != 1 {
		t.Fatal("uncertainty retried or fabricated confirmation")
	}
	actual, err := s.Lookup(testContext, r.ReservationID)
	if err != nil || actual.ExpiresAt != r.ExpiresAt+600_000 {
		t.Fatal("lost reply erased committed renewal")
	}
	if len(spy.classes) != 0 || len(spy.kinds) != 0 || spy.invalidRequests != 0 || spy.invalidFailures != 0 {
		t.Fatal("renewal expanded telemetry population")
	}
	// A real response writer failure likewise cannot erase an extension or retry.
	advanceRenewalClock(t, s, actual.ExpiresAt-1_200_000)
	h = observedHandler(t, s, spy)
	h.ServeHTTP(&failedWriter{header: http.Header{}, mode: "short"}, failureRequest("/reservations/renew", renewBody(actual, actual.ExpiresAt)))
	newest, err := s.Lookup(testContext, r.ReservationID)
	if err != nil || newest.ExpiresAt != actual.ExpiresAt+600_000 {
		t.Fatal("writer failure changed committed lease")
	}
	if len(spy.classes) != 0 || len(spy.kinds) != 0 {
		t.Fatal("renewal delivery failure expanded telemetry")
	}
}

func TestRenewalHTTPNoProgressCarriesCurrentRow(t *testing.T) {
	s, h, r := renewalFixture(t)
	advanceRenewalClock(t, s, r.ExpiresAt-1_200_000)
	out := postJSON(t, h, "/reservations/renew", renewBody(r, r.ExpiresAt))
	requireStatus(t, out, 200)
	latest, err := s.Lookup(testContext, r.ReservationID)
	if err != nil {
		t.Fatal("latest lease unavailable")
	}
	out = postJSON(t, h, "/reservations/renew", renewBody(latest, latest.ExpiresAt))
	requireStatus(t, out, 409)
	if decodeMap(t, out)["code"] != string(ledger.CodeRenewalNoProgress) || decodeMap(t, out)["reservation"].(map[string]any)["expires_at"] != formatMillis(latest.ExpiresAt) {
		t.Fatal("frozen effective clock fabricated progress")
	}
}

func TestRenewalHTTPExtraFieldsIgnored(t *testing.T) {
	s, h, r := renewalFixture(t)
	advanceRenewalClock(t, s, r.ExpiresAt-1_200_000)
	body := strings.TrimSuffix(renewBody(r, r.ExpiresAt), "}") + `,"extra":"SYNTHETIC_PRIVATE_SENTINEL"}`
	out := postJSON(t, h, "/reservations/renew", body)
	requireStatus(t, out, 200)
	if strings.Contains(out.Body.String(), "SYNTHETIC_PRIVATE_SENTINEL") {
		t.Fatal("extra data echoed")
	}
	// The timestamps are fixed UTC milliseconds, not arbitrary RFC3339 forms.
	stamp := decodeMap(t, out)["observed_at"].(string)
	if _, err := time.Parse("2006-01-02T15:04:05.000Z", stamp); err != nil {
		t.Fatal("invalid authoritative clock format")
	}
}
