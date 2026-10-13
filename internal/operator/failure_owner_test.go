package operator

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/httpapi"
	"possums-intake/internal/ledger"
)

type nativeObserver struct {
	kinds   []failuretype.Type
	invalid int
}

func (o *nativeObserver) ObserveFailure(kind failuretype.Type) { o.kinds = append(o.kinds, kind) }
func (o *nativeObserver) InvalidateFailures()                  { o.invalid++ }
func (o *nativeObserver) want(t *testing.T, want ...failuretype.Type) {
	t.Helper()
	if o.invalid != 0 || len(o.kinds) != len(want) {
		t.Fatal("native failure population differs")
	}
	for i, kind := range want {
		if o.kinds[i] != kind {
			t.Fatal("native category differs")
		}
	}
	o.kinds = nil
}
func observedNative(t *testing.T, s *ledger.Service, app core.App, o *nativeObserver) http.Handler {
	t.Helper()
	custom, err := httpapi.NewHandler(s, s.Ready, testKey, nil)
	if err != nil {
		t.Fatal("custom fixture unavailable")
	}
	h, err := NewHandlerObserved(custom, app, s, o)
	if err != nil {
		t.Fatal("observed native router unavailable")
	}
	return h
}

func TestNativeActualErrorOnlyOwnerGateAuthAndTypedSDKCauses(t *testing.T) {
	s, app, _ := fixture(t)
	o := &nativeObserver{}
	h := observedNative(t, s, app, o)
	request(t, h, "GET", "/api/settings", "", "")
	o.want(t, failuretype.AuthenticationRefused)
	request(t, h, "GET", "/api/backups", "", "")
	o.want(t, failuretype.PolicyRefused)
	request(t, h, "POST", accountRecords, `{"balance":1}`, "")
	o.want(t, failuretype.RequestInvalid)
	request(t, h, "POST", authPrefix+"auth-with-password", `{"identity":"`+testEmail+`","password":"`+hostile+`"}`, "")
	// The SDK returns an untyped credential exception: HTTP 400 is not proof
	// of its cause. Preserve the auth-stage fallback, not guessed expiry/data.
	o.want(t, failuretype.NativeAuthUnknown)
	for _, fault := range []struct {
		err  error
		kind failuretype.Type
	}{
		{context.Canceled, failuretype.Cancelled},
		{context.DeadlineExceeded, failuretype.DeadlineExceeded},
		{errors.New(hostile), failuretype.NativeAuthUnknown},
		{&ledger.Diagnostic{Code: ledger.CodeInput, Stage: hostile, Constraint: hostile, Outcome: hostile, NextStep: hostile}, failuretype.RequestInvalid},
	} {
		id := app.OnRecordAuthWithPasswordRequest().BindFunc(func(e *core.RecordAuthWithPasswordRequestEvent) error {
			return router.NewApiError(503, hostile, fault.err)
		})
		expectClosed(t, request(t, h, "POST", authPrefix+"auth-with-password", `{"identity":"`+testEmail+`","password":"`+testPassword+`"}`, ""), 503, "native_password_auth")
		app.OnRecordAuthWithPasswordRequest().Unbind(id)
		o.want(t, fault.kind)
	}
	id := app.OnRecordAuthWithPasswordRequest().BindFunc(func(e *core.RecordAuthWithPasswordRequestEvent) error { panic(hostile) })
	expectClosed(t, request(t, h, "POST", authPrefix+"auth-with-password", `{"identity":"`+testEmail+`","password":"`+testPassword+`"}`, ""), 500, "native_password_auth")
	app.OnRecordAuthWithPasswordRequest().Unbind(id)
	o.want(t, failuretype.NativeAuthUnknown)
	login(t, h)
	request(t, h, "GET", "/_/", "", "")
	o.want(t) // no native success/activity/financial population
}

func TestNativeActualUnownedAuthenticationCategoryIsNotDropped(t *testing.T) {
	s, app, _ := fixture(t)
	o := &nativeObserver{}
	h := observedNative(t, s, app, o)
	id := app.OnRecordAuthWithPasswordRequest().BindFunc(func(*core.RecordAuthWithPasswordRequestEvent) error {
		return ledger.CanonicalFailure(&ledger.Diagnostic{}, failuretype.AuthenticationRefused)
	})
	defer app.OnRecordAuthWithPasswordRequest().Unbind(id)
	expectClosed(t, request(t, h, "POST", authPrefix+"auth-with-password", `{"identity":"`+testEmail+`","password":"`+testPassword+`"}`, ""), 500, "native_password_auth")
	o.want(t, failuretype.AuthenticationRefused)
}

type nativeReadFault struct {
	core.App
	fault error
}

func (a nativeReadFault) FindRecordById(collection any, id string, filters ...func(*dbx.SelectQuery) error) (*core.Record, error) {
	if c, ok := collection.(*core.Collection); ok && c.Name == ledger.AccountCollectionName {
		return nil, a.fault
	}
	return a.App.FindRecordById(collection, id, filters...)
}
func TestNativeActualSDKPreliminaryPATCHLookupOwnsNoRowsAndFaultSeparately(t *testing.T) {
	s, app, base := fixture(t)
	token := login(t, base)
	id := createNative(t, base, token)
	for _, fault := range []struct {
		err  error
		kind failuretype.Type
	}{
		{sql.ErrNoRows, failuretype.NativeReadUnknown},
		{context.Canceled, failuretype.Cancelled},
		{errors.New(hostile), failuretype.NativeReadUnknown},
	} {
		o := &nativeObserver{}
		h := observedNative(t, s, nativeReadFault{app, fault.err}, o)
		rec := request(t, h, "PATCH", accountRecords+"/"+id, `{"balance":"20"}`, token)
		if rec.Code != 404 || strings.Contains(rec.Body.String(), hostile) || !s.Ready() {
			t.Fatal("preliminary lookup changed readiness/authority or exposed a cause")
		}
		o.want(t, fault.kind)
		if snapshot, err := s.Balance(context.Background(), "native-synthetic"); err != nil || snapshot.Available != "18446744073709551615" {
			t.Fatal("preliminary lookup reached mutation")
		}
	}
}

type nativeIOFault struct {
	header http.Header
	err    error
}

func (w *nativeIOFault) Header() http.Header       { return w.header }
func (w *nativeIOFault) WriteHeader(int)           {}
func (w *nativeIOFault) Write([]byte) (int, error) { return 0, w.err }
func abortNative(t *testing.T, h http.Handler, w http.ResponseWriter, req *http.Request) {
	t.Helper()
	defer func() {
		if value := recover(); value != nil && value != http.ErrAbortHandler {
			t.Fatal("raw native panic escaped")
		}
	}()
	h.ServeHTTP(w, req)
}
func TestNativeActualCompletionHookAndIndependentDeliveryCancellationCountTwice(t *testing.T) {
	s, app, base := fixture(t)
	token := login(t, base)
	o := &nativeObserver{}
	h := observedNative(t, s, app, o)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hook := app.OnRecordCreate(ledger.AccountCollectionName).BindFunc(func(e *core.RecordEvent) error {
		e.App.TxInfo().OnComplete(func(error) error { cancel(); return context.Canceled })
		return e.Next()
	})
	defer app.OnRecordCreate(ledger.AccountCollectionName).Unbind(hook)
	req := httptest.NewRequest("POST", accountRecords, strings.NewReader(`{"account_id":"native-owner-commit","balance":"10","enabled":true}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)
	abortNative(t, h, &nativeIOFault{http.Header{}, context.Canceled}, req)
	o.want(t, failuretype.Cancelled, failuretype.Cancelled)
	if snapshot, err := s.Balance(context.Background(), "native-owner-commit"); err != nil || snapshot.Available != "10" {
		t.Fatal("hook/delivery fault erased known commit")
	}
}
func TestNativeActualPrimaryRequestCancellationPropagationIsCountedOnce(t *testing.T) {
	s, app, base := fixture(t)
	token := login(t, base)
	o := &nativeObserver{}
	h := observedNative(t, s, app, o)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("POST", accountRecords, strings.NewReader(`{"account_id":"native-owner-no-effect","balance":"10","enabled":true}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)
	abortNative(t, h, &nativeIOFault{http.Header{}, context.Canceled}, req)
	o.want(t, failuretype.Cancelled)
	if _, err := s.Balance(context.Background(), "native-owner-no-effect"); ledger.Failure(err).Code != ledger.CodeAccount {
		t.Fatal("cancelled admission created state")
	}
}

type bodyFault struct{}

func (bodyFault) Read([]byte) (int, error) { return 0, context.DeadlineExceeded }
func (bodyFault) Close() error             { return nil }
func TestNativeActualStrictReadBoundaryKeepsTypedFailure(t *testing.T) {
	s, app, _ := fixture(t)
	o := &nativeObserver{}
	h := observedNative(t, s, app, o)
	req := httptest.NewRequest("POST", accountRecords, nil)
	req.Header.Set("Content-Type", "application/json")
	req.Body = bodyFault{}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatal("strict read boundary drifted")
	}
	o.want(t, failuretype.DeadlineExceeded)
}

func TestNativeActualAssetProtocolStatusIsClosedAndClassified(t *testing.T) {
	s, app, _ := fixture(t)
	o := &nativeObserver{}
	h := observedNative(t, s, app, o)
	req := httptest.NewRequest("GET", "/_/", nil)
	req.Header.Set("Range", hostile)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	expectClosed(t, rec, 416, "native_asset")
	o.want(t, failuretype.NativeAssetUnknown)
}

type swallowedTokenApp struct{ core.App }

func (a swallowedTokenApp) FindAuthRecordByToken(string, ...string) (*core.Record, error) {
	return nil, context.DeadlineExceeded
}
func TestNativeActualSDKSwallowedTokenFailureRemainsExternalLimit(t *testing.T) {
	s, app, base := fixture(t)
	token := login(t, base)
	o := &nativeObserver{}
	h := observedNative(t, s, swallowedTokenApp{app}, o)
	expectClosed(t, request(t, h, "GET", "/api/settings", "", token), 401, "native_read")
	// Supported method injection, actual stock token loader: it swallows the
	// cause. Only the project authority refusal is observable, not deadline.
	o.want(t, failuretype.AuthenticationRefused)
}

// A supported RecordQuery method injection drives the actual stock controller
// through missing identity then an actual invalid-query error in its dummy
// lookup. The SDK discards that second error before any project request hook.
type swallowedDummyApp struct {
	core.App
	queries int
}

func (a *swallowedDummyApp) RecordQuery(collection any) *dbx.SelectQuery {
	q := a.App.RecordQuery(collection)
	a.queries++
	if a.queries == 1 {
		q.AndWhere(dbx.NewExp("1=0"))
	} else {
		q.AndWhere(dbx.NewExp("SYNTHETIC_UNDEFINED_FUNCTION()"))
	}
	return q
}
func TestNativeActualSDKSwallowedDummyQueryFailureRemainsExternalLimit(t *testing.T) {
	s, app, _ := fixture(t)
	o := &nativeObserver{}
	wrapped := &swallowedDummyApp{App: app}
	h := observedNative(t, s, wrapped, o)
	expectClosed(t, request(t, h, "POST", authPrefix+"auth-with-password", `{"identity":"`+testEmail+`","password":"`+testPassword+`"}`, ""), 400, "native_password_auth")
	if wrapped.queries != 2 || !s.Ready() {
		t.Fatal("actual dummy-query boundary was not exercised")
	}
	o.want(t, failuretype.NativeAuthUnknown) // not a count of the swallowed query
}
