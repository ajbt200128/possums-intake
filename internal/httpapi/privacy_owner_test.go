package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

func TestHTTPActualWireAndStdioNeverReceiveRawFailureFields(t *testing.T) {
	oldOut, oldErr := os.Stdout, os.Stderr
	outRead, outWrite, err := os.Pipe()
	if err != nil {
		t.Fatal("synthetic stdout capture unavailable")
	}
	errRead, errWrite, err := os.Pipe()
	if err != nil {
		t.Fatal("synthetic stderr capture unavailable")
	}
	defer outRead.Close()
	defer errRead.Close()
	os.Stdout, os.Stderr = outWrite, errWrite
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr; _ = outWrite.Close(); _ = errWrite.Close() }()
	stdout := make(chan []byte, 1)
	stderr := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(outRead); stdout <- b }()
	go func() { b, _ := io.ReadAll(errRead); stderr <- b }()
	var serverLog bytes.Buffer
	spy := &failureSpy{}
	h := observedHandler(t, stubLedger{err: &ledger.Diagnostic{Code: ledger.CodeAccount, Stage: testKey, Constraint: "SYNTHETIC_PRIVATE_SENTINEL", Outcome: testKey, NextStep: testKey}}, spy)
	done := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { done <- struct{}{} }()
		if r.URL.Path == "/reservations/lookup" {
			r.Body = readFault{panicRead: true}
		}
		h.ServeHTTP(w, r)
	}))
	server.Config.ErrorLog = log.New(&serverLog, "", 0) // detect any raw net/http panic output
	server.Start()
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	for _, fixture := range []struct{ path, body string }{
		{"/balance", `{"account_id":"SYNTHETIC_PRIVATE_SENTINEL"}`},
		{"/balance", `{"account_id":"SYNTHETIC_PRIVATE_SENTINEL","account_id":"duplicate"}`},
		{"/reservations/lookup", `{"reservation_id":"SYNTHETIC_PRIVATE_SENTINEL"}`},
	} {
		r, _ := http.NewRequest("POST", server.URL+fixture.path, strings.NewReader(fixture.body))
		r.Header.Set("Authorization", "Bearer "+testKey)
		r.Header.Set("Content-Type", "application/json")
		response, err := client.Do(r)
		if err != nil {
			t.Fatal("synthetic HTTP delivery unavailable")
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		<-done
		if readErr != nil || strings.Contains(string(body), testKey) || strings.Contains(string(body), "SYNTHETIC_PRIVATE_SENTINEL") || strings.Contains(string(body), "kind") {
			t.Fatal("private field reached actual HTTP wire")
		}
		if response.StatusCode == 503 && !bytes.Contains(body, []byte("request_read")) {
			t.Fatal("reader failure stage lost on wire")
		}
	}
	server.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	_ = outWrite.Close()
	_ = errWrite.Close()
	if len(<-stdout) != 0 || len(<-stderr) != 0 || serverLog.Len() != 0 {
		t.Fatal("HTTP classification emitted stdio or net/http panic diagnostics")
	}
	expectFailures(t, spy, failuretype.AccountMissing, failuretype.RequestInvalid, failuretype.RequestReadUnknown)
}

func TestHTTPFailureReportAndObserverAddNoSQLOrRetry(t *testing.T) {
	s := newTestLedger(t)
	createAccountFixture(t, newTestHandler(t, s, nil), "aaaaaaaaaaaaaaa")
	// Same test-only private-app seam as completion tests; no production knob.
	field := reflect.ValueOf(s).Elem().FieldByName("app")
	app := *(*core.App)(unsafe.Pointer(field.UnsafeAddr()))
	db, ok := app.NonconcurrentDB().(*dbx.DB)
	if !ok {
		t.Fatal("pinned SQL count fixture unavailable")
	}
	var reads, writes atomic.Int64
	db.QueryLogFunc = func(context.Context, time.Duration, string, *sql.Rows, error) { reads.Add(1) }
	db.ExecLogFunc = func(context.Context, time.Duration, string, sql.Result, error) { writes.Add(1) }
	defer func() { db.QueryLogFunc = nil; db.ExecLogFunc = nil }()
	for _, id := range []string{"aaaaaaaaaaaaaaa", "missing00000000"} {
		reads.Store(0)
		writes.Store(0)
		_, _ = s.Balance(testContext, id)
		baselineReads, baselineWrites := reads.Load(), writes.Load()
		reads.Store(0)
		writes.Store(0)
		spy := &failureSpy{}
		h := observedHandler(t, s, spy)
		postJSON(t, h, "/balance", `{"account_id":"`+id+`"}`)
		if reads.Load() != baselineReads || writes.Load() != baselineWrites {
			t.Fatal("HTTP report/observation initiated extra SQL or retry")
		}
		if id == "missing00000000" {
			expectFailures(t, spy, failuretype.AccountMissing)
		} else {
			expectFailures(t, spy)
		}
	}
}
