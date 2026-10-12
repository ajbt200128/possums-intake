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
		ID           string `json:"id"`
		Balance      string `json:"balance"`
		CollectionID string `json:"collectionId"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil || !recordID.MatchString(result.ID) || result.Balance != "18446744073709551615" || result.CollectionID != ledger.AccountCollectionID {
		t.Fatal("native account create response mismatch; output withheld")
	}
	return result.ID
}

func TestNativeSDKExactReadQueries(t *testing.T) {
	_, _, h := fixture(t)
	for _, query := range []string{"", "?fields=mfa%2Cotp%2Cpassword%2Coauth2"} {
		if request(t, h, "GET", authPrefix+"auth-methods"+query, "", "").Code != 200 {
			t.Fatal("actual native SDK auth methods projection rejected")
		}
	}
	for _, query := range []string{"?fields=mfa,otp,password,oauth2", "?fields=password", "?fields=mfa%2Cotp%2Cpassword%2Coauth2&fields=mfa%2Cotp%2Cpassword%2Coauth2", "?%66ields=mfa%2Cotp%2Cpassword%2Coauth2", "?fields=mfa%2cotp%2cpassword%2coauth2", "?fields=mfa%2Cotp%2Cpassword%2Coauth2&expand=anything"} {
		expectClosed(t, request(t, h, "GET", authPrefix+"auth-methods"+query, "", ""), 403, "native_gate")
	}
	token := login(t, h)
	// Observed in the actual browser: full collection list, native rowid default
	// sort, total count and account collection-ID getOne. No alias broadening.
	for _, path := range []string{"/api/collections?page=1&perPage=1000&skipTotal=1", accountRecords + "?page=1&perPage=40&skipTotal=1&filter=&sort=-%40rowid", accountRecords + "?page=1&perPage=1&filter=&fields=id", "/api/collections/ledgeraccounts1", "/api/collections/ledger_accounts"} {
		if request(t, h, "GET", path, "", token).Code != 200 {
			t.Fatal("qualified native editor read denied")
		}
	}
	for _, query := range []string{"?page=01", "?page=1&page=1", "?%70age=1", "?page=%31", "?perPage=1000", "?sort=--id", "?sort=-%40rowid&expand=id", "?fields=%40request.auth", "?filter=account_id%3D%27x%27", "?page=1&", "?page"} {
		expectClosed(t, request(t, h, "GET", accountRecords+query, "", token), 403, "native_gate")
	}
}

func TestNativeAccountStrictInputBeforeFieldCoercion(t *testing.T) {
	s, _, h := fixture(t)
	token := login(t, h)
	for _, body := range []string{
		`{"account_id":"native-synthetic","balance":1,"enabled":true}`,
		`{"account_id":42,"balance":"1","enabled":true}`,
		`{"account_id":"native-synthetic","balance":"1","enabled":1}`,
		`{"account_id":"native-synthetic","balance":"1","enabled":"true"}`,
		`{"account_id":"native-synthetic","balance":"1","enabled":true,"balance":"2"}`,
		`{"account_id":"native-synthetic","balance":"1","enabled":true,"balance+":"2"}`,
		`{"account_id":"native-synthetic","balance":"1","enabled":true,"account_id:autogenerate":true}`,
		`{"account_id":"native-synthetic","balance":"1","enabled":true,"unknown":"SYNTHETIC_PRIVATE_SENTINEL"}`,
		`{"account_id":"native-synthetic","balance":"1","enabled":true,"collectionName":"_superusers"}`,
		`{"account_id":"native-synthetic","balance":"1","enabled":true,"id":"aaaaaaaaaaaaaaa"}`,
		`{"account_id":"native-synthetic","balance":"01","enabled":true}`,
		`{"account_id":"native-synthetic","balance":"18446744073709551616","enabled":true}`,
		`{"account_id":"\ud800","balance":"1","enabled":true}`,
		`{"account_id":"\udfff","balance":"1","enabled":true}`,
		`{"account_id":"native-synthetic","balance":null,"enabled":true}`,
		`{"account_id":"native-synthetic","balance":"1","enabled":true} {}`,
		"{\"account_id\":\"\xff\",\"balance\":\"1\",\"enabled\":true}",
		`{}`, `[]`, strings.Repeat("x", 4097),
	} {
		expectClosed(t, request(t, h, "POST", accountRecords, body, token), 400, "native_account_input")
	}
	for _, id := range []string{strings.Repeat("猫", 43), strings.Repeat("a", 129)} {
		body, _ := json.Marshal(map[string]any{"account_id": id, "balance": "1", "enabled": false})
		rec := request(t, h, "POST", accountRecords, string(body), token)
		if rec.Code != 400 || strings.Contains(rec.Body.String(), id) {
			t.Fatal("oversized identity invalid or leaked")
		}
	}
	_, err := s.Balance(context.Background(), "native-synthetic")
	if ledger.Failure(err).Code != ledger.CodeAccount {
		t.Fatal("invalid native input created account")
	}
	for _, header := range []struct{ name, value string }{{"Content-Type", "application/json; charset=utf-8"}, {"Content-Encoding", "gzip"}} {
		req := httptest.NewRequest("POST", accountRecords, strings.NewReader(`{"account_id":"native-synthetic","balance":"1","enabled":true}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", token)
		req.Header.Set(header.name, header.value)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		expectClosed(t, rec, 400, "native_account_input")
	}
}

func TestNativeAccountAuthorizedCreateUpdateStoredTotalAndDenials(t *testing.T) {
	s, app, h := fixture(t)
	token := login(t, h)
	for _, auth := range []string{"", testKey, "Bearer " + testKey} {
		expectClosed(t, request(t, h, "POST", accountRecords, `{"account_id":"a","balance":"1","enabled":true}`, auth), 401, "native_account_request")
	}
	id := createNative(t, h, token)
	for _, name := range []string{ledger.AccountCollectionName, ledger.AccountCollectionID} {
		path := "/api/collections/" + name + "/records/" + id
		if request(t, h, "GET", path, "", token).Code != 200 {
			t.Fatal("account ID/name read failed")
		}
		expectClosed(t, request(t, h, "GET", path, "", ""), 401, "native_account_request")
		for _, method := range []string{"DELETE", "PUT", "POST"} {
			expectClosed(t, request(t, h, method, path, `{}`, token), 403, "native_gate")
		}
		if request(t, h, "PATCH", path, `{"enabled":false,"collectionName":"ledger_accounts","collectionId":"ledgeraccounts1"}`, token).Code != 200 {
			t.Fatal("native disabled account edit failed")
		}
	}
	for _, body := range []string{`{"account_id":"different"}`, `{"id":"aaaaaaaaaaaaaaa"}`} {
		rec := request(t, h, "PATCH", accountRecords+"/"+id, body, token)
		if rec.Code != 400 {
			t.Fatal("native immutable identity allowed")
		}
	}
	if request(t, h, "PATCH", accountRecords+"/"+id, `{"enabled":true}`, token).Code != 200 {
		t.Fatal("enable edit failed")
	}
	now := time.Now().UnixMilli()
	key := fmt.Sprintf("v1.%013d.%032x", now, 51)
	r, err := s.Reserve(context.Background(), ledger.ReserveInput{AccountID: "native-synthetic", Maximum: "10", TimeoutSeconds: 600, IdempotencyKey: key})
	if err != nil {
		t.Fatal("reserve fixture failed")
	}
	if request(t, h, "PATCH", accountRecords+"/"+id, `{"balance":"20"}`, token).Code != 409 {
		t.Fatal("held native balance changed")
	}
	read := request(t, h, "GET", accountRecords+"/"+id, "", token)
	if !strings.Contains(read.Body.String(), `"balance":"18446744073709551615"`) {
		t.Fatal("native read was not stored total")
	}
	if _, err := s.Settle(context.Background(), ledger.SettleInput{ReservationID: r.ReservationID, ChargedAmount: "5"}); err != nil {
		t.Fatal("settle fixture failed")
	}
	if request(t, h, "PATCH", accountRecords+"/"+id, `{"balance":"20"}`, token).Code != 200 {
		t.Fatal("terminal hold blocked edit")
	}
	if snapshot, err := s.Balance(context.Background(), "native-synthetic"); err != nil || snapshot.Available != "20" {
		t.Fatal("custom/native ledger storage diverged")
	}
	var count int
	if app.AuxDB().NewQuery("SELECT count(*) FROM _logs").Row(&count) != nil || count != 0 {
		t.Fatal("native write persisted activity")
	}
	if app.DB().NewQuery("SELECT count(*) FROM _authOrigins").Row(&count) != nil || count != 0 {
		t.Fatal("native write persisted origins")
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
	snapshot, err := s.Balance(context.Background(), "lost-native-delivery")
	if err != nil || snapshot.Available != "10" {
		t.Fatal("native known commit erased by delivery failure")
	}
}
