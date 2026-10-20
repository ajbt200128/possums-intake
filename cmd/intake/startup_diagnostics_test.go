package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

const startupSentinel = "SYNTHETIC_PRIVATE_SENTINEL"

func recordFixture(t *testing.T) (string, string) {
	t.Helper()
	parent := t.TempDir()
	writeStartupRecord(parent, startupRecord{1, "starting", phaseValidation, ""})
	return parent, filepath.Join(parent, startupDirectory, startupFilename)
}

func TestStartupRecordStrictBoundedPrivateReplacement(t *testing.T) {
	parent, path := recordFixture(t)
	for _, r := range []startupRecord{{1, "starting", phaseInit, ""}, {1, "failed", phaseInit, ledger.CodeExisting}, {1, "bound", phaseBind, ""}} {
		writeStartupRecord(parent, r)
		got, ok := readStartupRecord(parent)
		info, err := os.Lstat(path)
		entries, listErr := os.ReadDir(filepath.Dir(path))
		if !ok || got != r || err != nil || info.Mode().Perm() != 0600 || info.Size() > startupMaxBytes || listErr != nil || len(entries) != 1 {
			t.Fatal("STARTUP_RECORD_REPLACEMENT_CONTRACT")
		}
	}
	original := []byte("private fixture")
	target := filepath.Join(parent, "target")
	if os.WriteFile(target, original, 0600) != nil || os.Remove(path) != nil || os.Symlink(target, path) != nil {
		t.Fatal("STARTUP_RECORD_FIXTURE")
	}
	if _, ok := readStartupRecord(parent); ok {
		t.Fatal("STARTUP_RECORD_SYMLINK_ACCEPTED")
	}
	// Atomic replacement may replace a symlink entry but cannot write its target.
	writeStartupRecord(parent, startupRecord{1, "starting", phaseOpen, ""})
	got, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal("STARTUP_RECORD_SYMLINK_FOLLOWED")
	}
	if os.Symlink(target, filepath.Join(filepath.Dir(path), "next")) != nil {
		t.Fatal("STARTUP_RECORD_FIXTURE")
	}
	writeStartupRecord(parent, startupRecord{1, "starting", phaseBind, ""})
	got, err = os.ReadFile(target)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal("STARTUP_STAGING_SYMLINK_FOLLOWED")
	}
}

func TestStartupRecordRejectsHostileAndUnsafeEvidence(t *testing.T) {
	valid, _ := json.Marshal(startupRecord{1, "failed", phaseValidation, codeAdminKey})
	cases := map[string][]byte{
		"empty": nil, "hostile": []byte(startupSentinel), "oversize": bytes.Repeat([]byte("x"), startupMaxBytes+1),
		"unknown_field": append(append([]byte{}, valid[:len(valid)-1]...), []byte(`,"secret":"SYNTHETIC_PRIVATE_SENTINEL"}`)...),
		"duplicate":     []byte(`{"version":1,"state":"failed","phase":"validation","code":"LEDGER_RUNTIME_KEY_INVALID","code":"LEDGER_RUNTIME_KEY_INVALID"}`),
		"alias":         []byte(`{"Version":1,"state":"failed","phase":"validation","code":"LEDGER_RUNTIME_KEY_INVALID"}`),
		"unknown_phase": []byte(`{"version":1,"state":"failed","phase":"SYNTHETIC_PRIVATE_SENTINEL","code":"LEDGER_RUNTIME_KEY_INVALID"}`),
		"unknown_code":  []byte(`{"version":1,"state":"failed","phase":"validation","code":"SYNTHETIC_PRIVATE_SENTINEL"}`),
		"wrong_phase":   []byte(`{"version":1,"state":"failed","phase":"open","code":"LEDGER_RUNTIME_KEY_INVALID"}`),
		"unknown_state": []byte(`{"version":1,"state":"healthy","phase":"listener_bind","code":""}`),
		"version":       []byte(`{"version":2,"state":"bound","phase":"listener_bind","code":""}`),
		"trailing":      append(append([]byte{}, valid...), valid...),
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			parent, path := recordFixture(t)
			if os.WriteFile(path, payload, 0600) != nil {
				t.Fatal("STARTUP_RECORD_FIXTURE")
			}
			if _, ok := readStartupRecord(parent); ok {
				t.Fatal("STARTUP_UNTRUSTED_RECORD_ACCEPTED")
			}
		})
	}
	for _, kind := range []string{"missing", "symlink", "directory", "fifo", "hardlink", "permissions", "directory_permissions", "directory_symlink"} {
		t.Run(kind, func(t *testing.T) {
			parent, path := recordFixture(t)
			if os.Remove(path) != nil {
				t.Fatal("STARTUP_RECORD_FIXTURE")
			}
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink("missing", path)
			case "directory":
				err = os.Mkdir(path, 0600)
			case "fifo":
				err = unix.Mkfifo(path, 0600)
			case "hardlink":
				target := filepath.Join(parent, "target")
				err = os.WriteFile(target, valid, 0600)
				if err == nil {
					err = os.Link(target, path)
				}
			case "permissions":
				err = os.WriteFile(path, valid, 0644)
			case "directory_permissions":
				err = os.Chmod(filepath.Dir(path), 0755)
			case "directory_symlink":
				err = os.Remove(filepath.Dir(path))
				if err == nil {
					err = os.Symlink(t.TempDir(), filepath.Dir(path))
				}
			}
			if err != nil {
				t.Fatal("STARTUP_RECORD_FIXTURE")
			}
			if _, ok := readStartupRecord(parent); ok {
				t.Fatal("STARTUP_UNSAFE_FILE_ACCEPTED")
			}
		})
	}
}

type hostileStartupError struct{}

func (hostileStartupError) Error() string { panic(startupSentinel) }

func TestStartupProjectionKnownCodesAndPhaseUnknown(t *testing.T) {
	for _, phase := range []startupPhase{phaseValidation, phaseScratch, phaseInit, phaseOpen, phaseHandler, phaseWebsite, phaseBind} {
		for _, cause := range []error{hostileStartupError{}, &ledger.Diagnostic{Code: ledger.CodeBoundary, Stage: startupSentinel}, (*ledger.Diagnostic)(nil)} {
			code := startupFailureCode(phase, cause)
			d := startupHealthDiagnostic(startupRecord{1, "failed", phase, code})
			wire, _ := json.Marshal(d)
			if code != startupUnknown(phase) || d.Stage != string(phase) || !strings.Contains(d.NextStep, "cause unknown") || bytes.Contains(wire, []byte(startupSentinel)) {
				t.Fatal("STARTUP_PHASE_FALLBACK_CONTRACT")
			}
		}
	}
	for _, tc := range []struct {
		phase startupPhase
		code  ledger.Code
	}{{phaseValidation, codeAdminKey}, {phaseValidation, "LEDGER_OPERATOR_SETUP_INVALID"}, {phaseScratch, codeScratch}, {phaseInit, ledger.CodeCommit}, {phaseOpen, ledger.CodeMissing}, {phaseHandler, codeHandler}, {phaseWebsite, "SITE_CUSTOMER_SCHEMA_REFUSED"}, {phaseWebsite, "SITE_CUSTOMER_SCHEMA_UNAVAILABLE"}, {phaseWebsite, "SITE_PUBLICATION_UNAVAILABLE"}, {phaseWebsite, "SITE_AUTH_UNAVAILABLE"}, {phaseWebsite, "SITE_AUTH_CONFIG"}, {phaseWebsite, "SITE_AUTH_MFA_UNSUPPORTED"}, {phaseBind, codeBind}} {
		err := &ledger.Diagnostic{Code: tc.code, Stage: startupSentinel, Constraint: startupSentinel, Outcome: startupSentinel, NextStep: startupSentinel}
		r := startupRecord{1, "failed", tc.phase, startupFailureCode(tc.phase, err)}
		d := startupHealthDiagnostic(r)
		wire, _ := json.Marshal(d)
		if d.Code != tc.code || d.Stage != string(tc.phase) || !d.Kind().Valid() || d.Outcome != "not_ready" || bytes.Contains(wire, []byte(startupSentinel)) {
			t.Fatal("STARTUP_TYPED_PROJECTION_CONTRACT")
		}
	}
}

func TestWebsiteStartupCatalogRejectsCrossComponentCodes(t *testing.T) {
	for _, phase := range []startupPhase{phaseValidation, phaseScratch, phaseInit, phaseOpen, phaseHandler, phaseBind} {
		if startupCodeAllowed(phase, "SITE_CUSTOMER_SCHEMA_REFUSED") {
			t.Fatal("STARTUP_WEBSITE_CODE_ACCEPTED_IN_OTHER_COMPONENT")
		}
	}
	if startupCodeAllowed(phaseWebsite, codeHandler) || startupCodeAllowed(phaseWebsite, ledger.Code(startupSentinel)) {
		t.Fatal("STARTUP_WEBSITE_CATALOG_NOT_CLOSED")
	}
}

func shortStartupRecorder(t *testing.T, write func(startupRecord) *ledger.Diagnostic) *startupRecorder {
	t.Helper()
	return &startupRecorder{write: write, timer: func(d time.Duration) *time.Timer {
		if d != 120*time.Second {
			t.Error("STARTUP_HOLD_BOUND_CHANGED")
		}
		return time.NewTimer(10 * time.Millisecond)
	}}
}

func TestStartupHoldCancellationAndPostBindExit(t *testing.T) {
	for _, route := range []string{"timer", "cancel", "bound", "success"} {
		t.Run(route, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var records []startupRecord
			r := shortStartupRecorder(t, func(record startupRecord) *ledger.Diagnostic { records = append(records, record); return nil })
			r.checkpoint(phaseValidation)
			timers := 0
			baseTimer := r.timer
			r.timer = func(d time.Duration) *time.Timer {
				timers++
				if len(records) != 2 || records[1].State != "failed" {
					t.Error("STARTUP_HOLD_BEFORE_FAILURE_RECORD")
				}
				if route == "cancel" {
					cancel()
					return time.NewTimer(startupHold)
				}
				return baseTimer(d)
			}
			err := error(hostileStartupError{})
			if route == "bound" {
				r.listenerBound()
			} else if route == "success" {
				err = nil
			}
			started := time.Now()
			r.finish(ctx, err)
			elapsed := time.Since(started)
			want := 1
			if route == "bound" || route == "success" {
				want = 0
			}
			if timers != want || elapsed > time.Second || (route == "timer" && elapsed < 10*time.Millisecond) {
				t.Fatal("STARTUP_HOLD_LIFECYCLE_CONTRACT")
			}
		})
	}
}

func TestRecordedStartupFaultPhasesCleanupAndOwnership(t *testing.T) {
	t.Setenv("LEDGER_ADMIN_KEY", syntheticAdminKey)
	t.Setenv("LEDGER_OPERATOR_EMAIL", "synthetic@example.invalid")
	t.Setenv("LEDGER_OPERATOR_PASSWORD", "synthetic-password")
	for _, fault := range []string{"scratch", "init", "open", "handler", "bind", "panic", "website_panic"} {
		t.Run(fault, func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			if fault == "scratch" {
				t.Setenv("TMPDIR", filepath.Join(tmp, "missing"))
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal("STARTUP_BIND_FIXTURE")
			}
			defer listener.Close()
			var records []startupRecord
			r := shortStartupRecorder(t, func(record startupRecord) *ledger.Diagnostic {
				records = append(records, record)
				if record.State != "starting" {
					return nil
				}
				if fault == "panic" && record.Phase == phaseInit || fault == "website_panic" && record.Phase == phaseWebsite {
					panic(startupSentinel)
				}
				if (fault == "init" && record.Phase == phaseInit) || (fault == "open" && record.Phase == phaseOpen) || (fault == "handler" && record.Phase == phaseHandler) {
					entries, readErr := os.ReadDir(tmp)
					if readErr != nil || len(entries) != 1 {
						t.Fatal("STARTUP_SCRATCH_FIXTURE")
					}
					scratch := filepath.Join(tmp, entries[0].Name())
					switch fault {
					case "init":
						err = os.WriteFile(filepath.Join(scratch, "partial"), []byte(startupSentinel), 0600)
					case "open":
						err = os.Remove(filepath.Join(scratch, "data.db"))
					case "handler":
						var db *sql.DB
						db, err = sql.Open("sqlite", filepath.Join(scratch, "data.db"))
						if err == nil {
							_, err = db.Exec("DELETE FROM _collections WHERE name='_superusers'")
							db.Close()
						}
					}
					if err != nil {
						t.Fatal("STARTUP_STORAGE_FAULT_FIXTURE")
					}
				}
				return nil
			})
			o := &runtimeObserver{}
			err = executeRecorded(context.Background(), []string{"synthetic-serve", "--listen", listener.Addr().String()}, o, r)
			last := records[len(records)-1]
			wantPhase := map[string]startupPhase{"scratch": phaseScratch, "init": phaseInit, "open": phaseOpen, "handler": phaseHandler, "bind": phaseBind, "panic": phaseInit, "website_panic": phaseWebsite}[fault]
			wantCode := map[string]ledger.Code{"scratch": codeScratch, "init": ledger.CodeExisting, "open": ledger.CodeMissing, "handler": "LEDGER_OPERATOR_SETUP_INVALID", "bind": codeBind, "panic": startupUnknown(phaseInit), "website_panic": startupUnknown(phaseWebsite)}[fault]
			if err == nil || last.State != "failed" || last.Phase != wantPhase || last.Code != wantCode {
				t.Fatal("STARTUP_PHASE_PRIMARY_FAILURE_CONTRACT")
			}
			entries, readErr := os.ReadDir(tmp)
			preserved := fault == "init" || fault == "open" || fault == "panic" || fault == "website_panic"
			if readErr != nil || (preserved && len(entries) != 1) || (!preserved && len(entries) != 0) {
				t.Fatal("STARTUP_EXISTING_SCRATCH_POLICY_CHANGED")
			}
			// A primary init/open fault plus the independently observed retained
			// scratch refusal remains two observations, never a recorder count.
			if preserved {
				o.mu.Lock()
				count := len(o.kinds)
				o.mu.Unlock()
				if count != 2 {
					t.Fatal("STARTUP_FAILURE_OWNER_CHANGED")
				}
			} else if fault == "bind" {
				o.want(t, failuretype.RuntimeServeUnknown)
			}
		})
	}
}

func TestStartupDiagnosticFilesystemFailureKeepsPrimary(t *testing.T) {
	t.Setenv("LEDGER_ADMIN_KEY", "")
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	blockedParent := filepath.Join(tmp, "file")
	if os.WriteFile(blockedParent, []byte(startupSentinel), 0600) != nil {
		t.Fatal("STARTUP_DIAGNOSTIC_FAULT_FIXTURE")
	}
	attempts := 0
	r := shortStartupRecorder(t, func(record startupRecord) *ledger.Diagnostic {
		attempts++
		return writeStartupRecord(blockedParent, record)
	})
	var stderr bytes.Buffer
	r.stderr = &stderr
	o := &runtimeObserver{}
	err := executeRecorded(context.Background(), []string{"synthetic-serve"}, o, r)
	if err == nil || ledger.Failure(err).Code != codeAdminKey {
		t.Fatal("STARTUP_DIAGNOSTIC_FILESYSTEM_REPLACED_PRIMARY")
	}
	o.want(t, failuretype.RuntimeSetupUnknown, failuretype.RequestInvalid)
	if attempts != 1 || r.phase != phaseValidation || !r.failed {
		t.Fatal("STARTUP_RECORD_FAILURE_RETRIED")
	}
	checkStartupRecordingReport(t, stderr.Bytes(), "parent_open")
}

func checkStartupRecordingReport(t *testing.T, wire []byte, constraint string) {
	t.Helper()
	var d ledger.Diagnostic
	if bytes.Count(wire, []byte("\n")) != 1 || json.Unmarshal(wire, &d) != nil ||
		d.Code != "SYNTHETIC_STARTUP_RECORD_UNAVAILABLE" || d.Stage != "startup_record" ||
		d.Constraint != constraint || (d.Outcome != "recording_unavailable" && d.Outcome != "recording_unavailable_cleanup_unconfirmed") ||
		!strings.Contains(d.NextStep, "cause unknown") || !strings.Contains(d.NextStep, "Inspect") ||
		bytes.Contains(wire, []byte(startupSentinel)) {
		t.Fatal("STARTUP_RECORD_SAFE_ONCE_ONLY_REPORT_CONTRACT")
	}
}

func TestStartupRecordWriteFailureBoundaries(t *testing.T) {
	for _, boundary := range []string{"canonical_record_schema", "parent_open", "private_directory_create", "private_directory_open", "private_directory_safety", "stale_staging_unlink", "staging_create", "complete_staging_write", "partial_write", "staging_close", "atomic_record_replace", "staging_cleanup", "parent_close", "private_directory_close"} {
		t.Run(boundary, func(t *testing.T) {
			parent, path := recordFixture(t)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal("STARTUP_RECORD_FIXTURE")
			}
			r := startupRecord{1, "starting", phaseInit, ""}
			ops := startupRecordSystemIO()
			hostile := errors.New(startupSentinel)
			want := boundary
			switch boundary {
			case "canonical_record_schema":
				r.Phase = startupPhase(startupSentinel)
			case "parent_open":
				parent = filepath.Join(parent, "missing")
			case "private_directory_create":
				ops.mkdir = func(int, string, uint32) error { return hostile }
			case "private_directory_open":
				if os.Remove(path) != nil || os.Remove(filepath.Dir(path)) != nil || os.Symlink(t.TempDir(), filepath.Dir(path)) != nil {
					t.Fatal("STARTUP_RECORD_FIXTURE")
				}
			case "private_directory_safety":
				if os.Chmod(filepath.Dir(path), 0755) != nil {
					t.Fatal("STARTUP_RECORD_FIXTURE")
				}
			case "stale_staging_unlink":
				if os.Mkdir(filepath.Join(filepath.Dir(path), "next"), 0700) != nil {
					t.Fatal("STARTUP_RECORD_FIXTURE")
				}
			case "staging_create":
				// Inject a FIFO after stale-entry removal; exclusive creation must
				// refuse it promptly, never open/block on it.
				ops.unlink = func(fd int, name string, flags int) error {
					if unix.Mkfifo(filepath.Join(filepath.Dir(path), "next"), 0600) != nil {
						t.Fatal("STARTUP_RECORD_FIXTURE")
					}
					return nil
				}
			case "complete_staging_write":
				ops.write = func(*os.File, []byte) (int, error) { return 0, hostile }
			case "partial_write":
				want = "complete_staging_write"
				ops.write = func(f *os.File, p []byte) (int, error) { return f.Write(p[:len(p)-1]) }
			case "staging_close":
				ops.close = func(f *os.File) error { _ = f.Close(); return hostile }
			case "atomic_record_replace", "staging_cleanup":
				ops.rename = func(int, string, int, string) error { return hostile }
				if boundary == "staging_cleanup" {
					want = "atomic_record_replace" // first recording failure survives cleanup
					calls := 0
					ops.unlink = func(fd int, name string, flags int) error {
						calls++
						if calls == 2 {
							return hostile
						}
						return unix.Unlinkat(fd, name, flags)
					}
				}
			case "parent_close", "private_directory_close":
				calls := 0
				ops.closeFD = func(fd int) error {
					calls++
					_ = unix.Close(fd)
					if (boundary == "parent_close" && calls == 1) || (boundary == "private_directory_close" && calls == 2) {
						return hostile
					}
					return nil
				}
			}
			started := time.Now()
			fault := writeStartupRecordIO(parent, r, ops)
			if fault == nil || fault.Kind() != failuretype.RuntimeSetupUnknown || fault.Constraint != want || time.Since(started) > time.Second {
				t.Fatal("STARTUP_RECORD_TYPED_BOUNDARY_CONTRACT")
			}
			if (boundary == "staging_cleanup" || boundary == "staging_close" || boundary == "parent_close" || boundary == "private_directory_close") && fault.Outcome != "recording_unavailable_cleanup_unconfirmed" {
				t.Fatal("STARTUP_RECORD_CLEANUP_FAILURE_HIDDEN")
			}
			wire, _ := json.Marshal(fault)
			checkStartupRecordingReport(t, append(wire, '\n'), want)
			if boundary != "private_directory_open" && boundary != "private_directory_close" {
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("STARTUP_RECORD_FAILED_REPLACEMENT_CHANGED_EVIDENCE")
				}
			}
		})
	}
}

func TestStartupRecordingUnavailableLifecycleAndDelivery(t *testing.T) {
	for _, route := range []string{"timer", "cancel", "bound", "success", "stderr_failure", "hostile_fields"} {
		t.Run(route, func(t *testing.T) {
			attempts, timers := 0, 0
			r := shortStartupRecorder(t, func(startupRecord) *ledger.Diagnostic {
				attempts++
				if route == "hostile_fields" {
					return &ledger.Diagnostic{Code: ledger.Code(startupSentinel), Constraint: startupSentinel, NextStep: startupSentinel}
				}
				return startupRecordFailure("staging_close", failuretype.RuntimeSetupUnknown)
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var stderr bytes.Buffer
			r.stderr = &stderr
			o := &runtimeObserver{}
			r.observer = o
			if route == "stderr_failure" {
				r.stderr = startupFailedWriter{}
			}
			r.checkpoint(phaseValidation)
			r.checkpoint(phaseBind)
			if route == "bound" {
				r.listenerBound()
			}
			baseTimer := r.timer
			r.timer = func(d time.Duration) *time.Timer {
				timers++
				if route == "cancel" {
					cancel()
					return time.NewTimer(d)
				}
				return baseTimer(d)
			}
			primary := error(hostileStartupError{})
			if route == "success" {
				primary = nil
			}
			started := time.Now()
			r.finish(ctx, primary)
			wantTimers := 1
			if route == "bound" || route == "success" {
				wantTimers = 0
			}
			if attempts != 1 || timers != wantTimers || r.phase != phaseBind || r.bound != (route == "bound") || time.Since(started) > time.Second {
				t.Fatal("STARTUP_RECORD_FAILURE_LIFECYCLE_CHANGED")
			}
			if route == "stderr_failure" {
				o.want(t, failuretype.RuntimeSetupUnknown, failuretype.ResponseDeliveryUnknown)
			} else {
				o.want(t, failuretype.RuntimeSetupUnknown)
				constraint := "staging_close"
				if route == "hostile_fields" {
					constraint = "recording_boundary"
				}
				checkStartupRecordingReport(t, stderr.Bytes(), constraint)
			}
		})
	}
}

type startupFailedWriter struct{}

func (startupFailedWriter) Write([]byte) (int, error) { return 0, errors.New(startupSentinel) }

func TestStartupPostBindServeFailureJoinsWithoutHold(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("STARTUP_POST_BIND_FIXTURE")
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	h := &coordinatedHTTP{
		serveEntered: make(chan struct{}), serveRelease: make(chan struct{}), serveJoined: make(chan struct{}),
		drainEntered: make(chan struct{}), drainRelease: make(chan struct{}), drainJoined: make(chan struct{}),
		serveErr: hostileStartupError{},
	}
	close(h.serveRelease)
	close(h.drainRelease)
	var records []startupRecord
	r := &startupRecorder{write: func(record startupRecord) *ledger.Diagnostic { records = append(records, record); return nil }, timer: func(time.Duration) *time.Timer {
		t.Error("STARTUP_POST_BIND_ADDED_HOLD")
		return time.NewTimer(0)
	}}
	r.listenerBound()
	o := &runtimeObserver{}
	err = coordinateServeObserved(ctx, work, cancel, &cleanupFixture{runs: make(chan bool)}, h, listener, make(chan time.Time), time.Now(), o)
	r.finish(ctx, err)
	if err == nil || len(records) != 1 || records[0].State != "bound" {
		t.Fatal("STARTUP_POST_BIND_LIFECYCLE_CHANGED")
	}
	select {
	case <-h.serveJoined:
	default:
		t.Fatal("STARTUP_POST_BIND_SERVE_ABANDONED")
	}
	select {
	case <-h.drainJoined:
	default:
		t.Fatal("STARTUP_POST_BIND_DRAIN_ABANDONED")
	}
	o.want(t, failuretype.RuntimeServeUnknown)
	if d := startupHealthDiagnostic(records[0]); d.Outcome != "not_ready" || d.Code != "SYNTHETIC_HTTP_NOT_READY" {
		t.Fatal("STARTUP_BOUND_SOCKET_CALLED_HEALTHY")
	}
}

type startupTransport func(*http.Request) (*http.Response, error)

func (f startupTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSyntheticPassiveHTTPReadinessExactAndHostile(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		ready  bool
	}{{200, "{\"status\":\"ok\"}\n", true}, {200, `{"status":"ok"}`, true}, {503, `{"status":"ok"}`, false}, {200, `{"status":"not_ready"}`, false}, {302, `{"status":"ok"}`, false}, {200, `{"status":"ok","private":"SYNTHETIC_PRIVATE_SENTINEL"}`, false}, {200, `{"status":"ok","status":"ok"}`, false}, {200, strings.Repeat(startupSentinel, 100), false}, {200, startupSentinel, false}, {200, `{"Status":"ok"}`, false}, {200, "{\"status\":\"ok\"}\n{}", false}} {
		calls := 0
		client := syntheticHealthClient()
		transport := client.Transport.(*http.Transport)
		if transport.Proxy != nil || !transport.DisableKeepAlives || !transport.DisableCompression || transport.MaxResponseHeaderBytes != 4096 || client.Timeout > 2*time.Second {
			t.Fatal("STARTUP_HEALTH_TRANSPORT_CONTRACT")
		}
		client.Transport = startupTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.Method != "GET" || r.URL.String() != "http://127.0.0.1:8090/healthz" || r.Body != nil || r.Header.Get("Authorization") != "" {
				t.Fatal("STARTUP_HEALTH_NOT_PASSIVE_FIXED")
			}
			return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": []string{"http://SYNTHETIC_PRIVATE_SENTINEL/"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})
		if syntheticHTTPReady(context.Background(), client) != tc.ready || calls != 1 {
			t.Fatal("STARTUP_HEALTH_EXACT_READINESS_CONTRACT")
		}
	}
	client := syntheticHealthClient()
	client.Transport = startupTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New(startupSentinel) })
	if syntheticHTTPReady(context.Background(), client) {
		t.Fatal("STARTUP_HEALTH_TRANSPORT_ERROR_ACCEPTED")
	}
}

// Reserve the production fixed location only when absent; never disturb a
// pre-existing record. Test file helpers otherwise use isolated private parents.
func reserveFixedStartupDirectory(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("/tmp", startupDirectory)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Skip("STARTUP_FIXED_DIRECTORY_UNAVAILABLE stage=fixture")
	}
	t.Cleanup(func() {
		if os.RemoveAll(dir) != nil {
			t.Error("STARTUP_FIXED_DIRECTORY_CLEANUP_UNCONFIRMED")
		}
	})
	return dir
}

func startupProcessEnv(tmp string, diagnostics string) []string {
	var env []string
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "LEDGER_") || strings.HasPrefix(entry, "OTEL_") || strings.HasPrefix(entry, "INTAKE_METRICS_") || strings.HasPrefix(entry, "TMPDIR=") || strings.HasPrefix(entry, "HTTP_PROXY=") || strings.HasPrefix(entry, "http_proxy=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "TMPDIR="+tmp, "LEDGER_SYNTHETIC_STARTUP_DIAGNOSTICS="+diagnostics, "LEDGER_ADMIN_KEY="+syntheticAdminKey, "LEDGER_OPERATOR_EMAIL=synthetic@example.invalid", "LEDGER_OPERATOR_PASSWORD=synthetic-password", "OTEL_SDK_DISABLED=false", "INTAKE_METRICS_ENDPOINT="+startupSentinel, "OTEL_EXPORTER_OTLP_ENDPOINT="+startupSentinel, "HTTP_PROXY=http://SYNTHETIC_PRIVATE_SENTINEL")
}

func replaceStartupEnv(env []string, key, value string) []string {
	for i, entry := range env {
		if strings.HasPrefix(entry, key+"=") {
			env[i] = key + "=" + value
			return env
		}
	}
	return append(env, key+"="+value)
}

func TestStartupDiagnosticsCompiledProcess(t *testing.T) {
	diagnosticDir := reserveFixedStartupDirectory(t)
	port, portErr := net.Listen("tcp", "127.0.0.1:8090")
	if portErr != nil {
		t.Skip("STARTUP_FIXED_PORT_UNAVAILABLE stage=fixture")
	}
	port.Close()
	binary := filepath.Join(t.TempDir(), "intake")
	if exec.Command("go", "build", "-o", binary, ".").Run() != nil {
		t.Fatal("STARTUP_COMPILED_BUILD_UNAVAILABLE")
	}
	runHealth := func(t *testing.T, env []string, args ...string) (bool, ledger.Diagnostic) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, append([]string{"synthetic-healthcheck"}, args...)...)
		cmd.Env = env
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		var d ledger.Diagnostic
		if stdout.Len() != 0 || bytes.Contains(stderr.Bytes(), []byte(startupSentinel)) || (err != nil && json.Unmarshal(stderr.Bytes(), &d) != nil) || (err == nil && stderr.Len() != 0) {
			t.Fatal("STARTUP_HEALTH_PUBLIC_OUTPUT_CONTRACT")
		}
		return err == nil, d
	}
	for _, tc := range []struct {
		name, envKey, value string
		phase               startupPhase
		code                ledger.Code
	}{{"key", "LEDGER_ADMIN_KEY", "", phaseValidation, codeAdminKey}, {"missing_operator", "LEDGER_OPERATOR_EMAIL", "", phaseValidation, "LEDGER_OPERATOR_SETUP_INVALID"}, {"invalid_operator", "LEDGER_OPERATOR_PASSWORD", "short", phaseValidation, "LEDGER_OPERATOR_SETUP_INVALID"}, {"scratch", "TMPDIR", "/nonexistent-SYNTHETIC_PRIVATE_SENTINEL", phaseScratch, codeScratch}, {"bind", "", "", phaseBind, codeBind}} {
		t.Run(tc.name, func(t *testing.T) {
			env := startupProcessEnv(t.TempDir(), "true")
			var listener net.Listener
			if tc.name == "bind" {
				var err error
				listener, err = net.Listen("tcp", "0.0.0.0:8090")
				if err != nil {
					t.Skip("STARTUP_FIXED_PORT_UNAVAILABLE stage=fixture")
				}
				defer listener.Close()
			} else {
				env = replaceStartupEnv(env, tc.envKey, tc.value)
			}
			// Clear only this fixture's previous replaceable record.
			_ = os.Remove(filepath.Join(diagnosticDir, startupFilename))
			cmd := exec.Command(binary, "synthetic-serve")
			cmd.Env = env
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if cmd.Start() != nil {
				t.Fatal("STARTUP_PROCESS_UNAVAILABLE")
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			joined := false
			defer func() {
				if !joined {
					cmd.Process.Kill()
					<-done
				}
			}()
			var record startupRecord
			for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); {
				var ok bool
				record, ok = readStartupRecord("/tmp")
				if ok && record.State == "failed" {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if record.State != "failed" || record.Phase != tc.phase || record.Code != tc.code {
				t.Fatal("STARTUP_COMPILED_PHASE_CONTRACT")
			}
			select {
			case <-done:
				joined = true
				t.Fatal("STARTUP_FAILURE_EXITED_BEFORE_CAPTURE")
			case <-time.After(30 * time.Millisecond):
			}
			// A held socket cannot serve a healthcheck. Test health output after
			// releasing it so the single GET fails promptly rather than waiting.
			if listener != nil {
				listener.Close()
			}
			ok, d := runHealth(t, env)
			if ok || d.Code != tc.code || d.Stage != string(tc.phase) || d.Outcome != "not_ready" {
				t.Fatal("STARTUP_COMPILED_HEALTH_FAILURE_CONTRACT")
			}
			signal := syscall.SIGTERM
			if tc.name == "key" {
				signal = syscall.SIGINT
			}
			if cmd.Process.Signal(signal) != nil {
				t.Fatal("STARTUP_SIGNAL_UNAVAILABLE")
			}
			select {
			case err := <-done:
				joined = true
				checkFailure(t, stdout.String(), stderr.String(), err == nil, tc.code)
			case <-time.After(2 * time.Second):
				t.Fatal("STARTUP_HOLD_SIGNAL_NOT_IMMEDIATE")
			}
		})
	}

	t.Run("record_failure_preserves_primary_and_signals", func(t *testing.T) {
		if os.Chmod(diagnosticDir, 0000) != nil {
			t.Fatal("STARTUP_DIAGNOSTIC_FAULT_FIXTURE")
		}
		defer os.Chmod(diagnosticDir, 0700)
		for _, signal := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			cmd := exec.CommandContext(ctx, binary, "synthetic-serve")
			cmd.Env = replaceStartupEnv(startupProcessEnv(t.TempDir(), "true"), "LEDGER_ADMIN_KEY", "")
			pipe, err := cmd.StderrPipe()
			if err != nil || cmd.Start() != nil {
				cancel()
				t.Fatal("STARTUP_PROCESS_UNAVAILABLE")
			}
			decoder := json.NewDecoder(pipe)
			var supplementary, primary ledger.Diagnostic
			// The context kills/joins a stalled fixture; the first line arrives
			// before the hold and main emits the authoritative line afterward.
			firstErr := decoder.Decode(&supplementary)
			if firstErr == nil {
				err = cmd.Process.Signal(signal)
			}
			primaryErr := decoder.Decode(&primary)
			var extra ledger.Diagnostic
			extraErr := decoder.Decode(&extra)
			waitErr := cmd.Wait()
			cancel()
			if firstErr != nil || err != nil || primaryErr != nil || extraErr != io.EOF || waitErr == nil || ctx.Err() == context.DeadlineExceeded || primary.Code != codeAdminKey {
				t.Fatal("STARTUP_RECORD_PRIMARY_SIGNAL_CONTRACT")
			}
			if supplementary.Constraint != "private_directory_open" && supplementary.Constraint != "private_directory_safety" {
				t.Fatal("STARTUP_RECORD_PERMISSION_CLASSIFICATION")
			}
			wire, _ := json.Marshal(supplementary)
			checkStartupRecordingReport(t, append(wire, '\n'), supplementary.Constraint)
		}
	})

	t.Run("exact_opt_in_and_ordinary_unchanged", func(t *testing.T) {
		for _, mode := range []string{"synthetic-serve", "serve", "init"} {
			for _, enabled := range []string{"", "false", "TRUE", "true"} {
				if mode == "synthetic-serve" && enabled == "true" {
					continue
				}
				before, _ := os.ReadFile(filepath.Join(diagnosticDir, startupFilename))
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				cmd := exec.CommandContext(ctx, binary, mode)
				cmd.Env = replaceStartupEnv(startupProcessEnv(t.TempDir(), enabled), "LEDGER_ADMIN_KEY", "")
				var out, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &stderr
				err := cmd.Run()
				cancel()
				after, _ := os.ReadFile(filepath.Join(diagnosticDir, startupFilename))
				if err == nil || ctx.Err() == context.DeadlineExceeded || !bytes.Equal(before, after) || bytes.Contains(stderr.Bytes(), []byte("SYNTHETIC_STARTUP_RECORD_UNAVAILABLE")) {
					t.Fatal("STARTUP_OPT_IN_ISOLATION_CONTRACT")
				}
			}
		}
	})

	t.Run("healthcheck_no_state_and_hostile_evidence", func(t *testing.T) {
		tmp := t.TempDir()
		env := startupProcessEnv(tmp, "true")
		path := filepath.Join(diagnosticDir, startupFilename)
		for _, payload := range [][]byte{nil, []byte(startupSentinel), bytes.Repeat([]byte("x"), startupMaxBytes+1)} {
			_ = os.Remove(path)
			if payload != nil && os.WriteFile(path, payload, 0600) != nil {
				t.Fatal("STARTUP_HEALTH_FIXTURE")
			}
			ok, d := runHealth(t, env)
			if ok || d.Code != "SYNTHETIC_HEALTH_EVIDENCE_UNAVAILABLE" {
				t.Fatal("STARTUP_HOSTILE_HEALTH_EVIDENCE_ACCEPTED")
			}
		}
		if ok, d := runHealth(t, env, "--url=http://SYNTHETIC_PRIVATE_SENTINEL"); ok || d.Code != "SYNTHETIC_HEALTHCHECK_OPTIONS_INVALID" {
			t.Fatal("STARTUP_HEALTH_OPTIONS_ACCEPTED")
		}
		entries, err := os.ReadDir(tmp)
		if err != nil || len(entries) != 0 {
			t.Fatal("STARTUP_HEALTHCHECK_CREATED_STATE")
		}
	})

	t.Run("hostile_HTTP_redirect_and_deadline", func(t *testing.T) {
		for _, fixture := range []string{"hostile", "oversize", "redirect", "stall"} {
			t.Run(fixture, func(t *testing.T) {
				listener, err := net.Listen("tcp", "127.0.0.1:8090")
				if err != nil {
					t.Fatal("STARTUP_HEALTH_HTTP_FIXTURE")
				}
				var calls atomic.Int32
				release := make(chan struct{})
				server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != http.MethodGet || r.URL.Path != "/healthz" {
						t.Error("STARTUP_HEALTH_FIXED_REQUEST_CHANGED")
					}
					switch fixture {
					case "stall":
						<-release
					case "redirect":
						w.Header().Set("Location", "http://127.0.0.1:8090/SYNTHETIC_PRIVATE_SENTINEL")
						w.WriteHeader(302)
					case "oversize":
						_, _ = io.WriteString(w, strings.Repeat(startupSentinel, 100))
					default:
						_, _ = io.WriteString(w, startupSentinel)
					}
				})}
				joined := make(chan struct{})
				go func() { defer close(joined); _ = server.Serve(listener) }()
				defer func() { close(release); _ = server.Shutdown(context.Background()); <-joined }()
				writeStartupRecord("/tmp", startupRecord{1, "bound", phaseBind, ""})
				started := time.Now()
				ok, d := runHealth(t, startupProcessEnv(t.TempDir(), "true"))
				if ok || d.Code != "SYNTHETIC_HTTP_NOT_READY" || calls.Load() != 1 || time.Since(started) > 3*time.Second {
					t.Fatal("STARTUP_HEALTH_PASSIVE_WIRE_CONTRACT")
				}
			})
		}
	})

	for _, writable := range []bool{true, false} {
		name := "success"
		if !writable {
			name = "success_despite_startup_record_write_failure"
		}
		t.Run(name, func(t *testing.T) {
			port, err := net.Listen("tcp", "127.0.0.1:8090")
			if err != nil {
				t.Skip("STARTUP_FIXED_PORT_UNAVAILABLE stage=fixture")
			}
			port.Close()
			tmp := t.TempDir()
			env := startupProcessEnv(tmp, "true")
			var exports atomic.Int32
			tripwire := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { exports.Add(1); w.WriteHeader(500) }))
			defer tripwire.Close()
			env = replaceStartupEnv(env, "OTEL_EXPORTER_OTLP_ENDPOINT", tripwire.URL)
			env = replaceStartupEnv(env, "INTAKE_METRICS_ENDPOINT", tripwire.URL)
			_ = os.Remove(filepath.Join(diagnosticDir, startupFilename))
			if !writable {
				// The reader/writer rejects this directory even when run as root.
				if os.Chmod(diagnosticDir, 0000) != nil {
					t.Fatal("STARTUP_DIAGNOSTIC_FAULT_FIXTURE")
				}
				defer os.Chmod(diagnosticDir, 0700)
			}
			p := &process{done: make(chan error, 1)}
			p.cmd = exec.Command(binary, "synthetic-serve")
			p.cmd.Env = env
			p.cmd.Stdout, p.cmd.Stderr = &p.out, &p.stderr
			if p.cmd.Start() != nil {
				t.Fatal("STARTUP_SUCCESS_PROCESS_UNAVAILABLE")
			}
			go func() { p.done <- p.cmd.Wait() }()
			stop := func() {
				if p.stopped {
					return
				}
				p.stopped = true
				if p.cmd.Process.Signal(syscall.SIGTERM) != nil {
					t.Error("STARTUP_SIGNAL_UNAVAILABLE")
				}
				select {
				case err := <-p.done:
					if err != nil {
						t.Error("STARTUP_SUCCESS_SHUTDOWN_UNAVAILABLE")
					}
				case <-time.After(8 * time.Second):
					_ = p.cmd.Process.Kill()
					<-p.done
					t.Error("STARTUP_SUCCESS_SHUTDOWN_NOT_JOINED")
				}
			}
			defer stop()
			ready := false
			client := syntheticHealthClient()
			defer client.CloseIdleConnections()
			for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); {
				if syntheticHTTPReady(context.Background(), client) {
					ready = true
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			record, valid := readStartupRecord("/tmp")
			if !ready || (writable && (!valid || record.State != "bound")) || (!writable && valid) {
				t.Fatal("STARTUP_SUCCESS_NOT_HTTP_READY")
			}
			if ok, _ := runHealth(t, env); !ok {
				t.Fatal("STARTUP_DIAGNOSTICS_CHANGED_READINESS")
			}
			stop()
			if p.out.Len() != 0 {
				t.Fatal("STARTUP_SUCCESS_STDOUT_CHANGED")
			}
			if writable {
				if p.stderr.Len() != 0 {
					t.Fatal("STARTUP_SUCCESS_CHECKPOINT_LOGGED")
				}
			} else {
				var d ledger.Diagnostic
				if json.Unmarshal(p.stderr.Bytes(), &d) != nil || (d.Constraint != "private_directory_open" && d.Constraint != "private_directory_safety") {
					t.Fatal("STARTUP_RECORD_PERMISSION_CLASSIFICATION")
				}
				checkStartupRecordingReport(t, p.stderr.Bytes(), d.Constraint)
			}
			entries, err := os.ReadDir(tmp)
			if err != nil || len(entries) != 0 || exports.Load() != 0 {
				t.Fatal("STARTUP_SUCCESS_CLEANUP_OR_TELEMETRY_CHANGED")
			}
		})
	}
}
