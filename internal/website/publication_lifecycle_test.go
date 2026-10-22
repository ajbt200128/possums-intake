package website

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"possums-intake/internal/testfixture"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/ledger"
	"possums-intake/internal/siteauth"
)

type publicationBlockingAuth struct {
	customerAuth
	entered, release chan struct{}
	once             sync.Once
}

func (a *publicationBlockingAuth) Validate(ctx context.Context, token string) (siteauth.Principal, error) {
	a.once.Do(func() { close(a.entered) })
	select {
	case <-a.release:
	case <-ctx.Done():
		return siteauth.Principal{}, ctx.Err()
	}
	return a.customerAuth.Validate(ctx, token)
}
func TestPublicationActualInflightGET(t *testing.T) {
	p := newPublisher(t)
	p.publish(t, "In flight old")
	p.b.login(t)
	auth := &publicationBlockingAuth{customerAuth: p.b.host.auth, entered: make(chan struct{}), release: make(chan struct{})}
	p.b.host.auth = auth
	var once sync.Once
	release := func() { once.Do(func() { close(auth.release) }) }
	done := make(chan struct{})
	var body string
	var status int
	go func() {
		defer close(done)
		response, text := p.b.request(t, "GET", "/", nil, "")
		status = response.StatusCode
		body = text
	}()
	defer func() { release(); <-done }()
	<-auth.entered
	p.publish(t, "After in flight")
	release()
	<-done
	if status != 200 || !strings.Contains(body, "In flight old") || strings.Contains(body, "After in flight") {
		t.Fatal("PUBLICATION_INFLIGHT_MIXED")
	}
	response, body := p.b.request(t, "GET", "/", nil, "")
	if response.StatusCode != 200 || !strings.Contains(body, "After in flight") {
		t.Fatal("PUBLICATION_NEW_ADMISSION_OLD")
	}
}

func TestPublicationLedgerRestartAndLoggingIsolation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if ledger.Init(ctx, dir) != nil {
		t.Fatal("PUBLICATION_LEDGER_INIT")
	}
	service, err := ledger.Open(ctx, dir)
	if err != nil {
		t.Fatal("PUBLICATION_LEDGER_OPEN")
	}
	defer func() {
		if service != nil {
			service.Close()
		}
	}()
	if _, err = testfixture.Create(ctx, service, "publication0000"); err != nil {
		t.Fatal("PUBLICATION_LEDGER_PROVISION")
	}
	if _, err = service.Credit(ctx, ledger.CreditInput{AccountID: "publication0000", Amount: "100", IdempotencyKey: fmt.Sprintf("v1.%013d.00000000000000000000000000000001", time.Now().UnixMilli())}); err != nil {
		t.Fatal("PUBLICATION_LEDGER_CREDIT")
	}
	if _, err = service.Reserve(ctx, ledger.ReserveInput{AccountID: "publication0000", Maximum: "20", IdempotencyKey: fmt.Sprintf("v1.%013d.00000000000000000000000000000002", time.Now().UnixMilli()), TimeoutSeconds: 60}); err != nil {
		t.Fatal("PUBLICATION_LEDGER_HOLD")
	}
	before, err := service.Balance(ctx, "publication0000")
	if err != nil {
		t.Fatal("PUBLICATION_LEDGER_BALANCE")
	}
	app, err := service.CustomerAuthApp()
	if err != nil || InitializePublication(app) != nil {
		t.Fatal("PUBLICATION_LEDGER_SCHEMA")
	}
	// Direct SDK state setup is explicit fixture initialization, never serving fallback.
	record, err := app.FindRecordById(selectionCollection, publicationRecord)
	if err != nil {
		t.Fatal("PUBLICATION_LEDGER_SELECTION")
	}
	encoded, err := json.Marshal(source("Durable ledger-isolated source"))
	if err != nil {
		t.Fatal("PUBLICATION_LEDGER_ENCODING")
	}
	record.Set("active", json.RawMessage(encoded))
	if app.Save(record) != nil {
		t.Fatal("PUBLICATION_LEDGER_CODE")
	}
	if service.Close() != nil {
		t.Fatal("PUBLICATION_LEDGER_CLOSE")
	}
	service = nil
	service, err = ledger.Open(ctx, dir)
	if err != nil {
		t.Fatal("PUBLICATION_LEDGER_RESTART")
	}
	after, err := service.Balance(ctx, "publication0000")
	if err != nil || before.AccountBalance != after.AccountBalance || before.Available != after.Available || len(after.PendingReservations) != 1 || before.PendingReservations[0] != after.PendingReservations[0] {
		t.Fatal("PUBLICATION_CHANGED_FINANCIAL_STATE")
	}
	app, err = service.CustomerAuthApp()
	if err != nil || app.Settings().Logs.MaxDays != 0 {
		t.Fatal("PUBLICATION_LEDGER_LOGGING")
	}
	var logs, origins int
	if app.AuxDB().NewQuery("SELECT count(*) FROM _logs").Row(&logs) != nil || app.DB().NewQuery("SELECT count(*) FROM _authOrigins").Row(&origins) != nil || logs != 0 || origins != 0 {
		t.Fatal("PUBLICATION_LEDGER_ACTIVITY_PERSISTED")
	}
	b := newBrowser(t)
	if b.host.BindPublication(app) != nil {
		t.Fatal("PUBLICATION_LEDGER_LOAD")
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	// Only public GET; no synthetic auth record from another app is used here.
	request.TLS = &tls.ConnectionState{}
	writer := httptest.NewRecorder()
	b.host.ServeHTTP(writer, request)
	if writer.Code != 200 || !strings.Contains(writer.Body.String(), "Durable ledger-isolated source") {
		t.Fatal("PUBLICATION_LEDGER_RELOADED_PAGE")
	}
}

func TestPublicationRejectsAlreadyTransactionalIntent(t *testing.T) {
	p := newPublisher(t)
	p.publish(t, "Before batch")
	old := p.b.host.revision
	err := p.b.app.RunInTransaction(func(tx core.App) error {
		record, err := tx.FindRecordById(publicationCollection, publicationRecord)
		if err != nil {
			t.Fatal("PUBLICATION_BATCH_RECORD")
		}
		request := httptest.NewRequest("PATCH", "/", strings.NewReader(`{"publish":true}`))
		request.Header.Set("Content-Type", "application/json")
		event := &core.RecordRequestEvent{RequestEvent: &core.RequestEvent{App: tx}, Record: record, Collection: record.Collection()}
		event.Request = request
		event.Response = httptest.NewRecorder()
		return tx.OnRecordUpdateRequest(publicationCollection).Trigger(event, func(e *core.RecordRequestEvent) error { t.Fatal("PUBLICATION_BATCH_FINALIZED"); return nil })
	})
	if err == nil || p.b.host.revision != old {
		t.Fatal("PUBLICATION_BATCH_PREPARED_INSIDE_TX")
	}
}
