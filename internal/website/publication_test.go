package website

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"possums-intake/internal/operator"
)

type publisher struct {
	b        *browser
	token    string
	observer *numericObserver
}

func newPublisher(t *testing.T) *publisher {
	t.Helper()
	b := newBrowser(t)
	if InitializePublication(b.app) != nil {
		t.Fatal("PUBLICATION_SCHEMA")
	}
	if b.host.BindPublication(b.app) == nil {
		t.Fatal("PUBLICATION_EMPTY_ACTIVE_ACCEPTED")
	}
	observer := &numericObserver{}
	native, err := operator.NewHandlerObserved(b.host.fallback, b.app, nil, observer)
	if err != nil {
		t.Fatal("PUBLICATION_NATIVE")
	}
	b.host.fallback = native
	c, err := b.app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
	if err != nil {
		t.Fatal("PUBLICATION_ADMIN_COLLECTION")
	}
	r := core.NewRecord(c)
	r.SetEmail("publication-admin@example.invalid")
	r.SetPassword("synthetic-publication-admin-password")
	if b.app.Save(r) != nil {
		t.Fatal("PUBLICATION_ADMIN")
	}
	token, err := r.NewAuthToken()
	if err != nil {
		t.Fatal("PUBLICATION_ADMIN_TOKEN")
	}
	return &publisher{b, token, observer}
}
func source(label string) storedBundle {
	files := Fixture()
	files["pages.el"] = strings.ReplaceAll(files["pages.el"], "Private inference, with clear boundaries.", label)
	if label == "Second publication" {
		files["style.el"] = strings.ReplaceAll(files["style.el"], "#fafafa", "#ffffff")
	}
	return storedBundle{publicationHost, files}
}
func (p *publisher) patch(t *testing.T, data any) int {
	t.Helper()
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal("PUBLICATION_REQUEST_ENCODING")
	}
	req, err := http.NewRequest("PATCH", p.b.server.URL+"/api/collections/"+publicationCollection+"/records/"+publicationRecord, strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal("PUBLICATION_REQUEST")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", p.token)
	response, err := p.b.client.Do(req)
	if err != nil {
		t.Fatal("PUBLICATION_HTTP")
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal("PUBLICATION_BODY")
	}
	if response.StatusCode != 200 {
		if strings.Contains(string(body), "HOSTILE_PUBLICATION_SENTINEL") || strings.Contains(string(body), p.token) {
			t.Fatal("PUBLICATION_DIAGNOSTIC_DISCLOSURE")
		}
		var diagnostic struct{ Code, Stage string }
		if json.Unmarshal(body, &diagnostic) != nil || !strings.HasPrefix(diagnostic.Code, "SITE_PUBLICATION_") || !strings.HasPrefix(diagnostic.Stage, "publication_") {
			t.Fatal("PUBLICATION_NATIVE_STAGE_LOST")
		}
	}
	return response.StatusCode
}
func (p *publisher) publish(t *testing.T, label string) {
	t.Helper()
	if p.patch(t, map[string]any{"draft": source(label), "publish": true}) != 200 {
		t.Fatal("PUBLICATION_PUBLISH")
	}
}
func (p *publisher) current(t *testing.T) *core.Record {
	t.Helper()
	record, err := p.b.app.FindRecordById(selectionCollection, publicationRecord)
	if err != nil {
		t.Fatal("PUBLICATION_READ")
	}
	return record
}
func TestNativePublicationDraftLiveStaleSessionAndCSS(t *testing.T) {
	p := newPublisher(t)
	b := p.b
	response, _ := b.request(t, "GET", "/", nil, "")
	if response.StatusCode != 503 {
		t.Fatal("PUBLICATION_NO_FALLBACK")
	}
	if p.patch(t, map[string]any{"draft": source("Draft only")}) != 200 {
		t.Fatal("PUBLICATION_DRAFT_SAVE")
	}
	response, _ = b.request(t, "GET", "/", nil, "")
	if response.StatusCode != 503 {
		t.Fatal("PUBLICATION_DRAFT_ACTIVATED")
	}
	if p.patch(t, map[string]any{"publish": true}) != 200 {
		t.Fatal("PUBLICATION_INTENT")
	}
	response, body := b.request(t, "GET", "/", nil, "")
	if response.StatusCode != 200 || !strings.Contains(body, "Draft only") {
		t.Fatal("PUBLICATION_NOT_LIVE")
	}
	oldForm := b.form(t, "/login")
	// Final SDK login and private session are exercised before publication.
	b.login(t)
	old := b.host.revision
	p.publish(t, "Second publication")
	response, body = b.request(t, "GET", "/home", nil, "")
	if response.StatusCode != 200 {
		t.Fatal("PUBLICATION_SESSION_RESET")
	}
	response, _ = b.request(t, "POST", "/login", oldForm, b.server.URL)
	if response.StatusCode != 409 {
		t.Fatal("PUBLICATION_STALE_FORM")
	}
	for _, revision := range []*Revision{old, b.host.revision} {
		response, body = b.request(t, "GET", revision.CSSPath(), nil, "")
		if response.StatusCode != 200 || body != revision.CSS() {
			t.Fatal("PUBLICATION_CSS_COHERENCE")
		}
		expected := fmt.Sprintf("/_site/styles/%x.css", sha256.Sum256([]byte(body)))
		if revision.CSSPath() != expected {
			t.Fatal("PUBLICATION_CSS_NOT_CONTENT_ADDRESSED")
		}
	}
	before := b.host.revision
	if p.patch(t, map[string]any{"draft": storedBundle{publicationHost, Bundle{"init.el": "HOSTILE_PUBLICATION_SENTINEL ("}}, "publish": true}) == 200 {
		t.Fatal("PUBLICATION_INVALID_ACCEPTED")
	}
	if b.host.revision != before {
		t.Fatal("PUBLICATION_BAD_PREPARATION_CHANGED_ACTIVE")
	}
	// Code-only rollback republishes the retained previous bytes.
	retained := p.current(t).GetRaw("previous")
	if p.patch(t, map[string]any{"draft": retained, "publish": true}) != 200 {
		t.Fatal("PUBLICATION_ROLLBACK")
	}
	response, body = b.request(t, "GET", "/", nil, "")
	if response.StatusCode != 200 || !strings.Contains(body, "Draft only") {
		t.Fatal("PUBLICATION_ROLLBACK_CODE")
	}
	draft, err := b.app.FindRecordById(publicationCollection, publicationRecord)
	if err != nil || draft.GetBool("publish") {
		t.Fatal("PUBLICATION_INTENT_NOT_CLEARED")
	}
}

func TestPublicationColdReloadAndMissingActive(t *testing.T) {
	p := newPublisher(t)
	p.publish(t, "Cold restart source")
	app := p.b.app
	dir := app.DataDir()
	if app.ResetBootstrapState() != nil {
		t.Fatal("PUBLICATION_CLOSE")
	}
	reopened := core.NewBaseApp(core.BaseAppConfig{DataDir: dir, QueryTimeout: time.Second})
	if reopened.Bootstrap() != nil {
		t.Fatal("PUBLICATION_REOPEN")
	}
	t.Cleanup(func() {
		if reopened.ResetBootstrapState() != nil {
			t.Error("PUBLICATION_RECLOSE")
		}
	})
	h, err := NewFixture(prepared(t), p.b.host.auth, p.b.host.fallback, Config{Lifetime: time.Hour, IdleTimeout: time.Minute, ConcurrentRequests: 4})
	if err != nil || h.BindPublication(reopened) != nil {
		t.Fatal("PUBLICATION_RELOAD")
	}
	page, err := h.revision.Page(context.Background(), "public-home")
	if err != nil {
		t.Fatal("PUBLICATION_RELOAD_PAGE")
	}
	body, err := h.revision.Render(page, "", "")
	if err != nil || !strings.Contains(string(body), "Cold restart source") {
		t.Fatal("PUBLICATION_RELOAD_BYTES")
	}
	record, err := reopened.FindRecordById(selectionCollection, publicationRecord)
	if err != nil {
		t.Fatal("PUBLICATION_RELOAD_RECORD")
	}
	// Unrestricted native authority remains accepted; no financial restore/fallback.
	record.Set("active", nil)
	if reopened.Save(record) != nil {
		t.Fatal("PUBLICATION_NATIVE_MUTATION")
	}
	if h.BindPublication(reopened) == nil || h.revision != nil {
		t.Fatal("PUBLICATION_MISSING_ACTIVE_FALLBACK")
	}
}

func TestPublicationFaultClosesAdmissionAndExplicitPublishRecovers(t *testing.T) {
	p := newPublisher(t)
	p.publish(t, "Before fault")
	before := p.current(t).GetString("active")
	p.b.app.OnRecordUpdateExecute(publicationCollection).Bind(&hook.Handler[*core.RecordEvent]{Id: "publicationFault", Func: func(e *core.RecordEvent) error { return errors.New("HOSTILE_PUBLICATION_SENTINEL") }})
	if p.patch(t, map[string]any{"draft": source("Failed write"), "publish": true}) == 200 {
		t.Fatal("PUBLICATION_GUESSED_SUCCESS")
	}
	if p.current(t).GetString("active") != before || p.b.host.revision != nil {
		t.Fatal("PUBLICATION_FAULT_STATE")
	}
	response, _ := p.b.request(t, "GET", "/", nil, "")
	if response.StatusCode != 503 {
		t.Fatal("PUBLICATION_FAULT_ADMISSION")
	}
	p.b.app.OnRecordUpdateExecute(publicationCollection).Unbind("publicationFault")
	p.publish(t, "Explicit recovery")
	response, body := p.b.request(t, "GET", "/", nil, "")
	if response.StatusCode != 200 || !strings.Contains(body, "Explicit recovery") {
		t.Fatal("PUBLICATION_RECOVERY")
	}
}

func TestConcurrentPublishersCaptureCompleteRevisions(t *testing.T) {
	p := newPublisher(t)
	p.publish(t, "Initial")
	var group sync.WaitGroup
	for _, label := range []string{"Concurrent A", "Concurrent B", "Concurrent C"} {
		group.Add(1)
		go func(label string) { defer group.Done(); p.publish(t, label) }(label)
	}
	group.Wait()
	active := p.current(t).GetString("active")
	revision, _, err := prepareStored(context.Background(), active)
	if err != nil || revision.Digest != p.b.host.revision.Digest {
		t.Fatal("PUBLICATION_CONCURRENT_SELECTION")
	}
	if p.patch(t, map[string]any{"draft": source("Later draft")}) != 200 {
		t.Fatal("PUBLICATION_LATER_DRAFT")
	}
	if p.b.host.revision.Digest != revision.Digest {
		t.Fatal("PUBLICATION_DRAFT_CHANGED_RUNTIME")
	}
}

// A captured GET can finish on old code while the published pointer changes.
func TestPublicationCapturedRequestUsesOneRevision(t *testing.T) {
	p := newPublisher(t)
	p.publish(t, "Captured old")
	old := p.b.host.revision
	request := httptest.NewRequest("GET", "/", nil)
	request = request.WithContext(context.WithValue(request.Context(), revisionKey{}, old))
	p.publish(t, "New admission")
	writer := httptest.NewRecorder()
	route, _ := old.Named("public-home")
	p.b.host.page(writer, request, route, 200, "")
	if !strings.Contains(writer.Body.String(), "Captured old") || strings.Contains(writer.Body.String(), "New admission") {
		t.Fatal("PUBLICATION_MIXED_CAPTURE")
	}
}
