package operator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"possums-intake/internal/ledger"
)

const accountRecords = "/api/collections/ledger_accounts/records"

func createNative(t *testing.T, h http.Handler, token string) string {
	t.Helper()
	rec := request(t, h, "POST", accountRecords, `{"account_id":"native-synthetic","balance":"18446744073709551615","enabled":true,"id":""}`, token)
	var result struct {
		ID           string
		Balance      string
		CollectionID string
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil || !recordID.MatchString(result.ID) || result.Balance != "18446744073709551615" || result.CollectionID != ledger.AccountCollectionID {
		t.Fatal("native account create response mismatch; output withheld")
	}
	return result.ID
}

func TestNativeSDKQueriesAndStockInput(t *testing.T) {
	_, _, h := fixture(t)
	for _, query := range []string{"", "?fields=password", "?%66ields=mfa,otp,password,oauth2", "?fields=password&expand=anything"} {
		if request(t, h, "GET", authPrefix+"auth-methods"+query, "", "").Code != 200 {
			t.Fatal("SDK auth methods projection rejected")
		}
	}
	token := login(t, h)
	// SDK coercion, caller-selected native identity and additive fields are no
	// longer replaced with the project's strict financial JSON parser.
	rec := request(t, h, "POST", accountRecords, `{"id":"aaaaaaaaaaaaaaa","account_id":"stock-input","balance":10,"enabled":true,"unknown":"`+hostile+`"}`, token)
	if rec.Code != 200 {
		t.Fatal("stock record input refused")
	}
	for _, query := range []string{"?fields=id,balance&filter=account_id='stock-input'", "?perPage=1000", "?page=1&page=1", "?%70age=%31", "?sort=-@rowid&expand=id"} {
		if request(t, h, "GET", accountRecords+query, "", token).Code != 200 {
			t.Fatal("stock query refused")
		}
	}
	for _, body := range []string{`{"balance":"01"}`, `{"balance":"18446744073709551616"}`} {
		rec = request(t, h, "PATCH", accountRecords+"/aaaaaaaaaaaaaaa", body, token)
		if rec.Code != 400 || strings.Contains(rec.Body.String(), hostile) {
			t.Fatal("intrinsic money constraint lost or leaked")
		}
	}
}

func TestNativeStockAccountBypassesHeldBalanceAndIdentityGuard(t *testing.T) {
	s, app, h := fixture(t)
	token := login(t, h)
	for _, auth := range []string{"", testKey, "Bearer " + testKey} {
		expectClosed(t, request(t, h, "POST", accountRecords, `{"account_id":"a","balance":"1","enabled":true}`, auth), 403, "native_account_request")
	}
	id := createNative(t, h, token)
	path := accountRecords + "/" + id
	if request(t, h, "PATCH", path, `{"account_id":"native-renamed"}`, token).Code != 200 {
		t.Fatal("stock identity edit refused")
	}
	if request(t, h, "PATCH", path, `{"account_id":"native-synthetic"}`, token).Code != 200 {
		t.Fatal("stock identity edit reversal refused")
	}
	key := fmt.Sprintf("v1.%013d.%032x", time.Now().UnixMilli(), 51)
	r, err := s.Reserve(context.Background(), ledger.ReserveInput{AccountID: "native-synthetic", Maximum: "10", TimeoutSeconds: 600, IdempotencyKey: key})
	if err != nil {
		t.Fatal("reserve fixture failed")
	}
	if request(t, h, "PATCH", path, `{"balance":"20","enabled":true}`, token).Code != 200 {
		t.Fatal("trusted held-balance edit refused")
	}
	if snapshot, err := s.Balance(context.Background(), "native-synthetic"); err != nil || snapshot.Available != "10" {
		t.Fatal("stock stored total did not reach custom snapshot")
	}
	// SQL FK constraints still apply; policy removal does not drop constraints.
	if request(t, h, "DELETE", path, "", token).Code != 400 {
		t.Fatal("referenced account SQL constraint lost")
	}
	if _, err = s.Settle(context.Background(), ledger.SettleInput{ReservationID: r.ReservationID, ChargedAmount: "5"}); err != nil {
		t.Fatal("custom settlement changed")
	}
	var count int
	if app.AuxDB().NewQuery("SELECT count(*) FROM _logs").Row(&count) != nil || count != 0 {
		t.Fatal("native write persisted activity")
	}
	// An unreferenced account can be deleted using stock CRUD.
	if request(t, h, "POST", accountRecords, `{"id":"bbbbbbbbbbbbbbb","account_id":"delete-stock","balance":"1","enabled":true}`, token).Code != 200 || request(t, h, "DELETE", accountRecords+"/bbbbbbbbbbbbbbb", "", token).Code != 204 {
		t.Fatal("stock create/delete refused")
	}
}

type brokenNativeDelivery struct{ header http.Header }

func (w *brokenNativeDelivery) Header() http.Header       { return w.header }
func (w *brokenNativeDelivery) WriteHeader(int)           {}
func (w *brokenNativeDelivery) Write([]byte) (int, error) { return 0, errors.New(hostile) }

func TestNativePostCommitDeliveryFailureDoesNotResubmitOrEraseCommit(t *testing.T) {
	s, _, h := fixture(t)
	token := login(t, h)
	req := httptest.NewRequest("POST", accountRecords, strings.NewReader(`{"account_id":"lost-native-delivery","balance":"10","enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)
	aborted := false
	func() {
		defer func() {
			if v := recover(); v != nil {
				if v != http.ErrAbortHandler {
					t.Fatal("raw delivery panic escaped")
				}
				aborted = true
			}
		}()
		h.ServeHTTP(&brokenNativeDelivery{make(http.Header)}, req)
	}()
	if !aborted {
		t.Fatal("failed native delivery was not aborted")
	}
	if snapshot, err := s.Balance(context.Background(), "lost-native-delivery"); err != nil || snapshot.Available != "10" {
		t.Fatal("stock commit erased by delivery failure")
	}
}
