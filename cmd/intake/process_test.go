package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"possums-intake/internal/ledger"
	"possums-intake/internal/testfixture"
)

const syntheticAdminKey = "SYNTHETIC_ADMIN_KEY_ONLY_32_BYTES_LONG"

func helperEnv(key string) []string {
	var env []string
	for _, value := range os.Environ() {
		if strings.HasPrefix(value, "LEDGER_ADMIN_KEY=") || strings.HasPrefix(value, "LEDGER_VOLUME_KEY_B64=") || strings.HasPrefix(value, "INTAKE_SYNTHETIC_HELPER=") || strings.HasPrefix(value, "OTEL_") || strings.HasPrefix(value, "INTAKE_METRICS_") {
			continue
		}
		env = append(env, value)
	}
	return append(env, "INTAKE_SYNTHETIC_HELPER=1", "LEDGER_ADMIN_KEY="+key, "OTEL_SDK_DISABLED=true")
}

type process struct {
	cmd         *exec.Cmd
	out, stderr bytes.Buffer
	done        chan error
	base        string
	stopped     bool
}

func startProcess(t *testing.T, dir, address string) *process {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal("test executable unavailable")
	}
	p := &process{done: make(chan error, 1), base: "http://" + address}
	p.cmd = exec.Command(binary, "-test.run=^TestCLIProcess$", "--", "serve", "--state", dir, "--listen", address)
	p.cmd.Env = helperEnv(syntheticAdminKey)
	p.cmd.Stdout, p.cmd.Stderr = &p.out, &p.stderr
	if p.cmd.Start() != nil {
		t.Fatal("synthetic process failed to start")
	}
	go func() { p.done <- p.cmd.Wait() }()
	t.Cleanup(func() { stopProcess(t, p) })
	client := &http.Client{Timeout: 200 * time.Millisecond}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticks := time.NewTicker(20 * time.Millisecond)
	defer ticks.Stop()
	for {
		select {
		case <-p.done:
			p.stopped = true
			t.Fatal("service exited before readiness; captured output withheld")
		case <-deadline.C:
			t.Fatal("service did not become ready")
		case <-ticks.C:
			response, err := client.Get(p.base + "/healthz")
			if err == nil {
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if response.StatusCode == http.StatusOK {
					return p
				}
			}
		}
	}
}
func stopProcess(t *testing.T, p *process) {
	t.Helper()
	if p.stopped {
		return
	}
	p.stopped = true
	if p.cmd.Process.Signal(syscall.SIGTERM) != nil {
		t.Error("signal delivery failed")
	}
	select {
	case err := <-p.done:
		if err != nil {
			t.Error("service shutdown failed; captured output withheld")
		}
	case <-time.After(8 * time.Second):
		p.cmd.Process.Kill()
		<-p.done
		t.Error("service did not complete shutdown")
	}
	if p.out.Len() != 0 || p.stderr.Len() != 0 {
		t.Error("normal service emitted diagnostics or request logs; output withheld")
	}
}
func syntheticAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("loopback fixture unavailable")
	}
	address := listener.Addr().String()
	listener.Close()
	return address
}
func fixtureInitFailureCode(stderr string) ledger.Code {
	var diagnostic ledger.Diagnostic
	if json.Unmarshal([]byte(stderr), &diagnostic) == nil {
		switch diagnostic.Code {
		case ledger.CodeBegin, ledger.CodeWork, ledger.CodeWriter, ledger.CodeCommit, ledger.CodeClose,
			"SITE_EXECUTION_LIMIT", "SITE_EXECUTION_INTERRUPTED", "SITE_BUNDLE_REFUSED", "SITE_BUNDLE_UNAVAILABLE",
			"SITE_RENDER_UNAVAILABLE", "SITE_PUBLICATION_UNAVAILABLE", "SITE_CUSTOMER_SCHEMA_UNAVAILABLE", "SITE_CUSTOMER_SCHEMA_REFUSED":
			return diagnostic.Code
		}
	}
	return "SYNTHETIC_INIT_COMPLETION_UNKNOWN"
}

func initProcessState(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	out, stderr, success := runCLI(t, "init", "--state", dir)
	if !success || stderr != "" || out != "{\"status\":\"initialized_empty\"}\n" {
		t.Fatalf("SYNTHETIC_INIT_REFUSED stage=fixture_init constraint=closed_cli_completion code=%s; completion unconfirmed, raw output withheld", fixtureInitFailureCode(stderr))
	}
	return dir
}

func seedProcessAccount(t *testing.T, dir, id string) {
	t.Helper()
	s, err := ledger.Open(context.Background(), dir)
	if err != nil {
		t.Fatal("synthetic stopped fixture open unavailable")
	}
	_, createErr := testfixture.Create(context.Background(), s, id)
	closeErr := s.Close()
	if createErr != nil || closeErr != nil {
		t.Fatal("synthetic supported auth fixture unavailable")
	}
}

func rpc(t *testing.T, p *process, path, body, key, identity string) (int, map[string]json.RawMessage) {
	t.Helper()
	status, fields, err := requestRPC(p, path, body, key, identity)
	if err != nil {
		t.Fatal(err)
	}
	return status, fields
}

func requestRPC(p *process, path, body, key, identity string) (int, map[string]json.RawMessage, error) {
	request, err := http.NewRequest(http.MethodPost, p.base+path, strings.NewReader(body))
	if err != nil {
		return 0, nil, errors.New("synthetic request creation failed")
	}
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	if identity != "" {
		request.Header.Set("Idempotency-Key", identity)
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, errors.New("synthetic HTTP delivery failed")
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, nil, errors.New("synthetic response read failed")
	}
	if bytes.Contains(payload, []byte(syntheticAdminKey)) || bytes.Contains(payload, []byte("SYNTHETIC_HOSTILE_PRIVATE_VALUE")) || bytes.Contains(payload, []byte("goroutine ")) {
		return 0, nil, errors.New("HTTP boundary leaked private sentinel")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil {
		return 0, nil, errors.New("HTTP response was not JSON")
	}
	return response.StatusCode, fields, nil
}
func successRPC(t *testing.T, p *process, path, body, identity string) map[string]json.RawMessage {
	t.Helper()
	status, fields := rpc(t, p, path, body, syntheticAdminKey, identity)
	if status != 200 {
		t.Fatalf("synthetic operation status %d", status)
	}
	return fields
}
func readReservation(t *testing.T, fields map[string]json.RawMessage) struct{ ID, State, Expiry string } {
	t.Helper()
	var row struct {
		ID     string `json:"reservation_id"`
		State  string `json:"state"`
		Expiry string `json:"expires_at"`
	}
	if json.Unmarshal(fields["reservation"], &row) != nil || len(row.ID) != 32 {
		t.Fatal("reservation receipt missing")
	}
	return struct{ ID, State, Expiry string }{row.ID, row.State, row.Expiry}
}
func accountTotal(t *testing.T, fields map[string]json.RawMessage) string {
	t.Helper()
	var rows []struct {
		Balance string `json:"balance"`
	}
	if json.Unmarshal(fields["account_balance"], &rows) != nil || len(rows) != 1 {
		t.Fatal("balance row missing")
	}
	return rows[0].Balance
}
func processKey(n int) string { return fmt.Sprintf("v1.%013d.%032x", time.Now().UnixMilli(), n) }

func TestProcessAuthNativeRoutesPrivacyAndRestart(t *testing.T) {
	dir := initProcessState(t)
	seedProcessAccount(t, dir, "synthetic00000a")
	address := syntheticAddress(t)
	p := startProcess(t, dir, address)
	for _, path := range []string{"/credits", "/reservations", "/reservations/settle", "/reservations/refund", "/reservations/lookup", "/balance"} {
		for _, key := range []string{"", "SYNTHETIC_HOSTILE_PRIVATE_VALUE"} {
			status, fields := rpc(t, p, path, `{"SYNTHETIC_HOSTILE_PRIVATE_VALUE":true}`, key, "")
			if status != 401 {
				t.Fatal("accounting route skipped shared-key auth")
			}
			var d ledger.Diagnostic
			encoded, _ := json.Marshal(fields)
			if json.Unmarshal(encoded, &d) != nil || d.Code == "" || d.Stage == "" || d.Constraint == "" || d.Outcome == "" || d.NextStep == "" {
				t.Fatal("auth diagnostic incomplete")
			}
		}
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	// The website is now default-on; native administration remains excluded
	// without --operator and the accounting shared key grants no native access.
	response, err := client.Get(p.base + "/")
	if err != nil {
		t.Fatal("default website request unavailable")
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/html") || bytes.Contains(body, []byte(syntheticAdminKey)) {
		t.Fatal("default website response contract")
	}
	for _, path := range []string{"/_/", "/_/#/installer", "/api/collections", "/api/collections/_superusers/auth-with-password", "/api/records", "/api/files/x/y/z", "/api/realtime", "/api/settings", "/api/backups", "/api/backups/synthetic?token=SYNTHETIC_HOSTILE_PRIVATE_VALUE"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			request, _ := http.NewRequest(method, p.base+path, nil)
			request.Header.Set("Authorization", "Bearer "+syntheticAdminKey)
			response, err := client.Do(request)
			if err != nil {
				t.Fatal("native route probe failed")
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != 404 {
				t.Fatal("native route or installer reachable")
			}
		}
	}
	status, _ := rpc(t, p, "/balance", `{"account_id":{"private":"SYNTHETIC_HOSTILE_PRIVATE_VALUE"},"unexpected":true}`, syntheticAdminKey, "")
	if status != 400 {
		t.Fatal("hostile invalid body accepted")
	}
	successRPC(t, p, "/balance", `{"account_id":"synthetic00000a","future":{"ignored":true}}`, "")
	creditKey := processKey(1)
	successRPC(t, p, "/credits", `{"account_id":"synthetic00000a","amount":"100"}`, creditKey)
	reserveKey := processKey(2)
	row := readReservation(t, successRPC(t, p, "/reservations", `{"account_id":"synthetic00000a","maximum":"30","timeout_seconds":600}`, reserveKey))
	successRPC(t, p, "/reservations/settle", `{"reservation_id":"`+row.ID+`","charged_amount":"17"}`, "")
	activeKey := processKey(3)
	active := readReservation(t, successRPC(t, p, "/reservations", `{"account_id":"synthetic00000a","maximum":"20","timeout_seconds":600}`, activeKey))
	if accountTotal(t, successRPC(t, p, "/balance", `{"account_id":"synthetic00000a"}`, "")) != "83" {
		t.Fatal("settlement total incorrect")
	}
	stopProcess(t, p)
	inspectPrivateState(t, dir)
	p = startProcess(t, dir, address)
	// Same original state; credit retry does not grant startup/duplicate money.
	successRPC(t, p, "/credits", `{"account_id":"synthetic00000a","amount":"100"}`, creditKey)
	got := successRPC(t, p, "/balance", `{"account_id":"synthetic00000a"}`, "")
	if accountTotal(t, got) != "83" || string(got["available"]) != `"63"` {
		t.Fatal("restart changed acknowledged money")
	}
	retry := readReservation(t, successRPC(t, p, "/reservations", `{"account_id":"synthetic00000a","maximum":"20","timeout_seconds":600}`, activeKey))
	if retry != active {
		t.Fatal("restart or retry changed immutable reservation")
	}
	settled := readReservation(t, successRPC(t, p, "/reservations/refund", `{"reservation_id":"`+row.ID+`"}`, ""))
	if settled.State != "settled" {
		t.Fatal("restart lost first terminal outcome")
	}
	stopProcess(t, p)
	inspectPrivateState(t, dir)
}

func inspectPrivateState(t *testing.T, dir string) {
	t.Helper()
	for _, database := range []string{"data.db", "auxiliary.db"} {
		db, err := sql.Open("sqlite", filepath.Join(dir, database)+"?mode=rw")
		if err != nil {
			t.Fatal("offline storage inspection failed")
		}
		var count int
		query := "SELECT count(*) FROM _superusers"
		if database == "auxiliary.db" {
			query = "SELECT count(*) FROM _logs"
		}
		if err := db.QueryRow(query).Scan(&count); err != nil || count != 0 {
			db.Close()
			t.Fatal("superusers or request/activity logs persisted")
		}
		db.Close()
		if database == "auxiliary.db" {
			for _, suffix := range []string{"", "-wal", "-shm"} {
				data, err := os.ReadFile(filepath.Join(dir, database+suffix))
				if err != nil && !os.IsNotExist(err) {
					t.Fatal("auxiliary byte inspection failed")
				}
				for _, private := range []string{syntheticAdminKey, "SYNTHETIC_HOSTILE_PRIVATE_VALUE", "synthetic00000a"} {
					if bytes.Contains(data, []byte(private)) {
						t.Fatal("private value retained in framework auxiliary storage")
					}
				}
			}
		}
	}
}

func TestProcessLocksLostResponseRacesAndExpiry(t *testing.T) {
	dir := initProcessState(t)
	seedProcessAccount(t, dir, "synthetic00000a")
	address := syntheticAddress(t)
	p := startProcess(t, dir, address)
	successRPC(t, p, "/balance", `{"account_id":"synthetic00000a"}`, "")
	successRPC(t, p, "/credits", `{"account_id":"synthetic00000a","amount":"100"}`, processKey(1))
	// Raw synthetic lock connection, not a second PocketBase service/authority.
	db, err := sql.Open("sqlite", filepath.Join(dir, "data.db")+"?mode=rw&_pragma=busy_timeout(250)")
	if err != nil {
		t.Fatal("lock fixture unavailable")
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal("lock begin failed")
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE ledger_meta SET clock_high_water_ms=clock_high_water_ms WHERE singleton=1"); err != nil {
		t.Fatal("lock acquisition failed")
	}
	lockedKey := processKey(2)
	status, fields := rpc(t, p, "/credits", `{"account_id":"synthetic00000a","amount":"5"}`, syntheticAdminKey, lockedKey)
	if status != 503 || string(fields["outcome"]) != `"no_effect"` {
		t.Fatal("lock failure invented outcome")
	}
	if tx.Rollback() != nil {
		t.Fatal("lock release failed")
	}
	if accountTotal(t, successRPC(t, p, "/balance", `{"account_id":"synthetic00000a"}`, "")) != "100" {
		t.Fatal("locked rejection committed later")
	}
	successRPC(t, p, "/credits", `{"account_id":"synthetic00000a","amount":"5"}`, lockedKey)

	// Stop reading after the first response byte: no complete receipt reaches
	// this caller. The unchanged identity resolves it without another credit.
	lostKey := processKey(3)
	body := `{"account_id":"synthetic00000a","amount":"5"}`
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal("response-loss fixture connection failed")
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "POST /credits HTTP/1.1\r\nHost: local\r\nAuthorization: Bearer %s\r\nIdempotency-Key: %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", syntheticAdminKey, lostKey, len(body), body)
	_, readErr := bufio.NewReader(conn).ReadByte()
	conn.Close()
	if readErr != nil {
		t.Fatal("response-loss fixture failed to observe delivery start")
	}
	successRPC(t, p, "/credits", body, lostKey)
	if accountTotal(t, successRPC(t, p, "/balance", `{"account_id":"synthetic00000a"}`, "")) != "110" {
		t.Fatal("lost-response retry duplicated credit")
	}

	var wg sync.WaitGroup
	type response struct {
		status int
		fields map[string]json.RawMessage
		err    error
	}
	responses := make(chan response, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			status, fields, err := requestRPC(p, "/reservations", `{"account_id":"synthetic00000a","maximum":"100","timeout_seconds":600}`, syntheticAdminKey, processKey(n+10))
			responses <- response{status, fields, err}
		}(n)
	}
	wg.Wait()
	admitted := 0
	var id string
	for n := 0; n < 8; n++ {
		got := <-responses
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.status == 200 {
			admitted++
			id = readReservation(t, got.fields).ID
		} else if got.status != 409 {
			t.Fatal("race produced unexpected status")
		}
	}
	if admitted != 1 {
		t.Fatal("last-credit race overspent")
	}
	for _, path := range []string{"/reservations/settle", "/reservations/refund"} {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			body := `{"reservation_id":"` + id + `"}`
			if path == "/reservations/settle" {
				body = `{"reservation_id":"` + id + `","charged_amount":"17"}`
			}
			status, fields, err := requestRPC(p, path, body, syntheticAdminKey, "")
			responses <- response{status, fields, err}
		}(path)
	}
	wg.Wait()
	var firstState string
	for n := 0; n < 2; n++ {
		got := <-responses
		if got.err != nil || got.status != 200 {
			t.Fatal("terminal race response failed")
		}
		state := readReservation(t, got.fields).State
		if firstState != "" && firstState != state {
			t.Fatal("terminal competitors received different committed outcomes")
		}
		firstState = state
	}
	terminal := readReservation(t, successRPC(t, p, "/reservations/lookup", `{"reservation_id":"`+id+`"}`, ""))
	if terminal.State != "settled" && terminal.State != "refunded" {
		t.Fatal("terminal race missing outcome")
	}
	want := "110"
	if terminal.State == "settled" {
		want = "93"
	}
	if accountTotal(t, successRPC(t, p, "/balance", `{"account_id":"synthetic00000a"}`, "")) != want {
		t.Fatal("terminal race inconsistent money")
	}
	expiring := readReservation(t, successRPC(t, p, "/reservations", `{"account_id":"synthetic00000a","maximum":"1","timeout_seconds":1}`, processKey(30)))
	time.Sleep(1100 * time.Millisecond)
	status, fields = rpc(t, p, "/reservations/settle", `{"reservation_id":"`+expiring.ID+`","charged_amount":"1"}`, syntheticAdminKey, "")
	if status != 409 || readReservation(t, fields).State != "expired" {
		t.Fatal("late settlement charged expired hold")
	}
	if accountTotal(t, successRPC(t, p, "/balance", `{"account_id":"synthetic00000a"}`, "")) != want {
		t.Fatal("expiry debited balance")
	}
	stopProcess(t, p)
	inspectPrivateState(t, dir)
}

func TestProcessKeyRejectedBeforeListenerAndPrivateStartup(t *testing.T) {
	dir := initProcessState(t)
	address := syntheticAddress(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal("test executable unavailable")
	}
	for _, key := range []string{"", "SHORT_PRIVATE_SENTINEL"} {
		command := exec.Command(binary, "-test.run=^TestCLIProcess$", "--", "serve", "--state", dir, "--listen", address)
		command.Env = helperEnv(key)
		var out, stderr bytes.Buffer
		command.Stdout, command.Stderr = &out, &stderr
		err := command.Run()
		checkFailure(t, out.String(), stderr.String(), err == nil, codeAdminKey)
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatal("invalid-key startup opened listener")
		}
		listener.Close()
	}
	out, stderr, success := runCLI(t, "serve", "--state", dir, "--listen", "0.0.0.0:8090")
	checkFailure(t, out, stderr, success, codeListen)
}
