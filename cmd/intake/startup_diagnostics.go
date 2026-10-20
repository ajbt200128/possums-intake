package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

// This is opt-in synthetic-container evidence, not real-data authorization,
// a ledger, log or readiness authority. Neither TMPDIR nor options select its location.
const (
	startupDirectory = "possums-intake-startup"
	startupFilename  = "record.json"
	startupMaxBytes  = 512
	startupHold      = 120 * time.Second
)

type startupPhase string

const (
	phaseValidation    startupPhase = "validation"
	phaseVolumePrepare startupPhase = "volume_prepare"
	phaseScratch       startupPhase = "scratch_create"
	phaseInit          startupPhase = "initialize"
	phaseOpen          startupPhase = "open"
	phaseHandler       startupPhase = "operator_handler"
	phaseWebsite       startupPhase = "website_setup"
	phaseBind          startupPhase = "listener_bind"
)

type startupRecord struct {
	Version int          `json:"version"`
	State   string       `json:"state"`
	Phase   startupPhase `json:"phase"`
	Code    ledger.Code  `json:"code"`
}

type startupRecorder struct {
	pendingManaged bool // No recording or hold until actual parsed scope is confirmed.
	phase          startupPhase
	bound          bool
	failed         bool
	write          func(startupRecord) *ledger.Diagnostic
	stderr         io.Writer
	observer       failureowner.Observer
	timer          func(time.Duration) *time.Timer
}

// Only the explicit mode and exact opt-in select a recorder here. Managed
// scope is confirmed later by the actual flag parser, never by rescanning argv.
func startupRecorderForArgs(args []string) *startupRecorder {
	if len(args) == 0 {
		return nil
	}
	if args[0] == "synthetic-serve" && os.Getenv("LEDGER_SYNTHETIC_STARTUP_DIAGNOSTICS") == "true" {
		return newStartupRecorder()
	}
	if args[0] == "serve" && os.Getenv("LEDGER_MANAGED_STARTUP_DIAGNOSTICS") == "true" {
		r := newStartupRecorder()
		r.pendingManaged = true
		return r
	}
	return nil
}

func newStartupRecorder() *startupRecorder {
	return &startupRecorder{
		write:  func(r startupRecord) *ledger.Diagnostic { return writeStartupRecord("/tmp", r) },
		stderr: os.Stderr,
		timer:  time.NewTimer,
	}
}

func (s *startupRecorder) checkpoint(phase startupPhase) {
	if s == nil {
		return
	}
	s.phase = phase
	s.record(startupRecord{1, "starting", phase, ""})
}

func (s *startupRecorder) listenerBound() {
	if s == nil {
		return
	}
	// Mark the lifecycle boundary even if the best-effort record write fails.
	// A bound listener alone is never called healthy.
	s.bound = true
	s.record(startupRecord{1, "bound", phaseBind, ""})
}

func (s *startupRecorder) finish(ctx context.Context, err error) {
	if s == nil || s.pendingManaged || s.bound || err == nil {
		return
	}
	s.record(startupRecord{1, "failed", s.phase, startupFailureCode(s.phase, err)})
	timer := s.timer(startupHold) // private test seam; no runtime duration setting
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// The recorder alone owns the helper's failure. Disable further attempts before
// reporting; diagnostic availability never controls startup or hold decisions.
func (s *startupRecorder) record(r startupRecord) {
	if s.pendingManaged || s.failed {
		return
	}
	if fault := s.write(r); fault != nil {
		s.failed = true
		// Reconstruct the closed template, never serialize helper-supplied fields.
		cleanupUnknown := fault.Outcome == "recording_unavailable_cleanup_unconfirmed"
		fault = startupRecordFailure(fault.Constraint, failuretype.RuntimeSetupUnknown)
		if cleanupUnknown {
			fault = startupRecordCleanupFailure(fault, "staging_cleanup")
		}
		failureowner.Observe(s.observer, fault.Kind())
		if s.stderr != nil {
			if err := json.NewEncoder(s.stderr).Encode(fault); err != nil {
				failureowner.Observe(s.observer, startupRecordFailure("stderr_delivery", failuretype.ResponseDeliveryUnknown).Kind())
			}
		}
	}
}

func startupRecordFailure(constraint string, kind failuretype.Type) *ledger.Diagnostic {
	switch constraint {
	case "canonical_record_schema", "bounded_record_encoding", "parent_open", "parent_close",
		"private_directory_create", "private_directory_open", "private_directory_safety", "private_directory_close",
		"stale_staging_unlink", "staging_create", "complete_staging_write", "staging_close",
		"atomic_record_replace", "staging_cleanup", "stderr_delivery":
	default:
		constraint = "recording_boundary"
	}
	d := ledger.CanonicalFailure(&ledger.Diagnostic{}, kind)
	d.Code, d.Stage, d.Constraint, d.Outcome = "SYNTHETIC_STARTUP_RECORD_UNAVAILABLE", "startup_record", constraint, "recording_unavailable"
	d.NextStep = "Startup diagnostic recording is unavailable; cause unknown. Inspect synthetic temporary storage and stderr access privately. Use HTTP readiness and the primary startup diagnostic; this reports no financial outcome."
	return d
}

// One recording attempt owns its first failure and any teardown uncertainty;
// preserve its constraint without hiding a failed cleanup or retrying it.
func startupRecordCleanupFailure(primary *ledger.Diagnostic, constraint string) *ledger.Diagnostic {
	if primary == nil {
		primary = startupRecordFailure(constraint, failuretype.RuntimeSetupUnknown)
	}
	if primary.Outcome != "recording_unavailable_cleanup_unconfirmed" {
		primary.NextStep += " Diagnostic resource cleanup is also unconfirmed; cause unknown."
	}
	primary.Outcome = "recording_unavailable_cleanup_unconfirmed"
	return primary
}

func startupUnknown(phase startupPhase) ledger.Code {
	switch phase {
	case phaseValidation:
		return "SYNTHETIC_VALIDATION_CAUSE_UNKNOWN"
	case phaseVolumePrepare:
		return codeVolumeUnknown
	case phaseScratch:
		return "SYNTHETIC_SCRATCH_CAUSE_UNKNOWN"
	case phaseInit:
		return "SYNTHETIC_INIT_CAUSE_UNKNOWN"
	case phaseOpen:
		return "SYNTHETIC_OPEN_CAUSE_UNKNOWN"
	case phaseHandler:
		return "SYNTHETIC_HANDLER_CAUSE_UNKNOWN"
	case phaseWebsite:
		return "SYNTHETIC_WEBSITE_CAUSE_UNKNOWN"
	case phaseBind:
		return "SYNTHETIC_BIND_CAUSE_UNKNOWN"
	default:
		return ""
	}
}

// Only typed project codes in the phase-specific local catalog survive. Never
// copy Diagnostic fields, inspect error strings or invoke arbitrary error hooks.
func startupFailureCode(phase startupPhase, err error) ledger.Code {
	if d, ok := err.(*ledger.Diagnostic); ok && d != nil && startupCodeAllowed(phase, d.Code) {
		return d.Code
	}
	return startupUnknown(phase)
}

func startupCodeAllowed(phase startupPhase, code ledger.Code) bool {
	if code != "" && code == startupUnknown(phase) {
		return true
	}
	switch phase {
	case phaseValidation:
		switch code {
		case codeSyntheticOptions, codeAdminKey, codeListen, "LEDGER_OPERATOR_SETUP_INVALID":
			return true
		}
	case phaseVolumePrepare:
		switch code {
		case codeVolumeInput, codeVolumeKey, codeVolumeRejected, codeVolumeCancelled,
			codeVolumeDeadline, codeVolumeSocket, codeVolumeExchange, codeVolumeMount,
			codeVolumeCleanup, codeVolumeUnknown, codeVolumePlatform:
			return true
		}
	case phaseScratch:
		return code == codeScratch
	case phaseInit:
		switch code {
		case ledger.CodePath, ledger.CodeExisting, ledger.CodeStartup, ledger.CodeCorrupt,
			ledger.CodeSchema, ledger.CodeBegin, ledger.CodeWork, ledger.CodeWriter,
			ledger.CodeStorage, ledger.CodeCommit, ledger.CodeClose, "LEDGER_OPERATOR_SETUP_INVALID":
			return true
		}
	case phaseOpen:
		switch code {
		case ledger.CodePath, ledger.CodeMissing, ledger.CodeStartup, ledger.CodeCorrupt,
			ledger.CodeSchema, ledger.CodeBegin, ledger.CodeWork, ledger.CodeWriter,
			ledger.CodeStorage, ledger.CodeCommit, ledger.CodeClose:
			return true
		}
	case phaseHandler:
		switch code {
		case codeHandler, ledger.CodeClosed, "LEDGER_OPERATOR_SETUP_INVALID":
			return true
		}
	case phaseWebsite:
		switch code {
		case ledger.CodeClosed, "SITE_CUSTOMER_SCHEMA_REFUSED", "SITE_CUSTOMER_SCHEMA_UNAVAILABLE",
			"SITE_PUBLICATION_UNAVAILABLE", "SITE_AUTH_CONFIG", "SITE_AUTH_MFA_UNSUPPORTED", "SITE_AUTH_UNAVAILABLE":
			return true
		}
	case phaseBind:
		return code == codeBind
	}
	return false
}

func validStartupRecord(r startupRecord) bool {
	if r.Version != 1 || startupUnknown(r.Phase) == "" {
		return false
	}
	switch r.State {
	case "starting":
		return r.Code == ""
	case "bound":
		return r.Phase == phaseBind && r.Code == ""
	case "failed":
		return startupCodeAllowed(r.Phase, r.Code)
	default:
		return false
	}
}

// The fixed parent /tmp is the container runtime boundary. Descendants are
// opened relative to a held fd, never followed through symlinks. O_NONBLOCK
// prevents hostile FIFOs from blocking a passive healthcheck.
func openStartupDirectory(parent string, create bool, ops startupRecordIO) (fd int, fault *ledger.Diagnostic) {
	fd = -1
	parentFD, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, startupRecordFailure("parent_open", failuretype.RuntimeSetupUnknown)
	}
	defer func() {
		if err := ops.closeFD(parentFD); err != nil {
			fault = startupRecordCleanupFailure(fault, "parent_close")
			if fd >= 0 {
				if err := ops.closeFD(fd); err != nil {
					fault = startupRecordCleanupFailure(fault, "private_directory_close")
				}
				fd = -1
			}
		}
	}()
	if create {
		if err := ops.mkdir(parentFD, startupDirectory, 0700); err != nil && err != unix.EEXIST {
			return -1, startupRecordFailure("private_directory_create", failuretype.RuntimeSetupUnknown)
		}
	}
	fd, err = unix.Openat(parentFD, startupDirectory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return -1, startupRecordFailure("private_directory_open", failuretype.RuntimeSetupUnknown)
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&07777 != 0700 || st.Mode&unix.S_IFMT != unix.S_IFDIR {
		fault = startupRecordFailure("private_directory_safety", failuretype.RuntimeSetupUnknown)
		if err := ops.closeFD(fd); err != nil {
			fault = startupRecordCleanupFailure(fault, "private_directory_close")
		}
		return -1, fault
	}
	return fd, nil
}

// Private offline fault seams; runtime operations and locations remain fixed.
type startupRecordIO struct {
	mkdir   func(int, string, uint32) error
	write   func(*os.File, []byte) (int, error)
	close   func(*os.File) error
	closeFD func(int) error
	unlink  func(int, string, int) error
	rename  func(int, string, int, string) error
}

func startupRecordSystemIO() startupRecordIO {
	return startupRecordIO{
		mkdir: unix.Mkdirat, write: (*os.File).Write, close: (*os.File).Close, closeFD: unix.Close,
		unlink: unix.Unlinkat, rename: unix.Renameat,
	}
}

// A single staging file is atomically renamed, never appended. The helper
// classifies but does not observe; its caller owns presentation/observation.
func writeStartupRecord(parent string, r startupRecord) *ledger.Diagnostic {
	return writeStartupRecordIO(parent, r, startupRecordSystemIO())
}

func writeStartupRecordIO(parent string, r startupRecord, ops startupRecordIO) (fault *ledger.Diagnostic) {
	failure := func(constraint string) *ledger.Diagnostic {
		return startupRecordFailure(constraint, failuretype.RuntimeSetupUnknown)
	}
	if !validStartupRecord(r) {
		return failure("canonical_record_schema")
	}
	payload, err := json.Marshal(r)
	if err != nil || len(payload) > startupMaxBytes {
		return failure("bounded_record_encoding")
	}
	fd, fault := openStartupDirectory(parent, true, ops)
	if fault != nil {
		return fault
	}
	defer func() {
		if err := ops.closeFD(fd); err != nil {
			fault = startupRecordCleanupFailure(fault, "private_directory_close")
		}
	}()
	const staging = "next"
	// Unlinking this fixed private entry never follows its target. A crash may
	// leave one staging entry; replacing it does not accumulate history.
	if err := ops.unlink(fd, staging, 0); err != nil && err != unix.ENOENT {
		return failure("stale_staging_unlink")
	}
	out, err := unix.Openat(fd, staging, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return failure("staging_create")
	}
	renamed := false
	defer func() {
		if !renamed {
			if err := ops.unlink(fd, staging, 0); err != nil && err != unix.ENOENT {
				fault = startupRecordCleanupFailure(fault, "staging_cleanup")
			}
		}
	}()
	file := os.NewFile(uintptr(out), staging)
	n, writeErr := ops.write(file, payload)
	closeErr := ops.close(file)
	if writeErr != nil || n != len(payload) {
		fault = failure("complete_staging_write")
		if closeErr != nil {
			fault = startupRecordCleanupFailure(fault, "staging_close")
		}
		return fault
	}
	if closeErr != nil {
		return startupRecordCleanupFailure(nil, "staging_close")
	}
	if err := ops.rename(fd, staging, fd, startupFilename); err != nil {
		return failure("atomic_record_replace")
	}
	renamed = true
	return nil
}

func readStartupRecord(parent string) (startupRecord, bool) {
	var record startupRecord
	dir, fault := openStartupDirectory(parent, false, startupRecordSystemIO())
	if fault != nil {
		return record, false
	}
	defer unix.Close(dir)
	fd, err := unix.Openat(dir, startupFilename, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return record, false
	}
	file := os.NewFile(uintptr(fd), startupFilename)
	defer file.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&07777 != 0600 || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Size < 1 || st.Size > startupMaxBytes {
		return record, false
	}
	payload, err := io.ReadAll(io.LimitReader(file, startupMaxBytes+1))
	if err != nil || len(payload) > startupMaxBytes || json.Unmarshal(payload, &record) != nil || !validStartupRecord(record) {
		return startupRecord{}, false
	}
	// Accept only this writer's exact canonical schema, rejecting unknown or
	// duplicate fields, case aliases, trailing data and hostile encodings.
	canonical, err := json.Marshal(record)
	if err != nil || !bytes.Equal(payload, canonical) {
		return startupRecord{}, false
	}
	return record, true
}

func syntheticHealthClient() *http.Client {
	return &http.Client{
		Timeout:       2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext,
			DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 4096,
		},
	}
}

func syntheticHTTPReady(ctx context.Context, client *http.Client) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:8090/healthz", nil)
	if err != nil {
		return false
	}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 65))
	return err == nil && response.StatusCode == http.StatusOK && (bytes.Equal(payload, []byte("{\"status\":\"ok\"}\n")) || bytes.Equal(payload, []byte("{\"status\":\"ok\"}")))
}

func syntheticHealthcheck(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return healthDiagnostic("SYNTHETIC_HEALTHCHECK_OPTIONS_INVALID", "healthcheck", "no_options", "Use synthetic-healthcheck without options; it performs one fixed passive readiness request.")
	}
	client := syntheticHealthClient()
	defer client.CloseIdleConnections()
	if syntheticHTTPReady(ctx, client) {
		return nil // readiness does not depend on diagnostic filesystem success
	}
	r, valid := readStartupRecord("/tmp")
	if !valid {
		return healthDiagnostic("SYNTHETIC_HEALTH_EVIDENCE_UNAVAILABLE", "healthcheck", "valid_bounded_startup_evidence", "Readiness is unconfirmed and the startup cause is unknown. Inspect the synthetic container privately; do not infer healthy startup or replay work.")
	}
	return startupHealthDiagnostic(r)
}

func healthDiagnostic(code ledger.Code, phase, constraint, next string) *ledger.Diagnostic {
	return runtimeFailure(code, phase, constraint, "not_ready", next)
}

// Reconstruct only locally authored templates, never file or HTTP bytes. These
// diagnostics report startup/readiness, not financial or cleanup outcomes.
func startupHealthDiagnostic(r startupRecord) *ledger.Diagnostic {
	if !validStartupRecord(r) {
		return healthDiagnostic("SYNTHETIC_HEALTH_EVIDENCE_UNAVAILABLE", "healthcheck", "valid_bounded_startup_evidence", "Startup cause unknown. Inspect the synthetic container privately.")
	}
	if r.State == "bound" {
		return healthDiagnostic("SYNTHETIC_HTTP_NOT_READY", string(phaseBind), "confirmed_http_readiness", "The listener was bound, but HTTP readiness is unconfirmed; cause unknown. Inspect the synthetic container privately.")
	}
	if r.State == "starting" {
		return healthDiagnostic("SYNTHETIC_STARTUP_INCOMPLETE", string(r.Phase), "completed_startup_checkpoint", "Startup completion is unconfirmed; cause unknown. Inspect the synthetic container privately.")
	}
	next := "Underlying startup cause unknown. Inspect runtime configuration and temporary storage privately; do not reuse or reinitialize partial state."
	constraint := "completed_startup_checkpoint"
	switch r.Code {
	case codeSyntheticOptions:
		constraint, next = "synthetic_listen_only", "Use synthetic-serve with optional --listen only; no state or operator option."
	case codeAdminKey:
		constraint, next = "valid_runtime_admin_key", "Inject a valid high-entropy LEDGER_ADMIN_KEY through the runtime environment; never put credentials in arguments or logs."
	case codeListen:
		constraint, next = "numeric_synthetic_listener", "Select a numeric loopback or wildcard listener and canonical numeric port."
	case "LEDGER_OPERATOR_SETUP_INVALID":
		constraint, next = "valid_runtime_operator_setup", "Supply valid throwaway runtime operator credentials and the qualified operator setup; inspect privately, never print credentials."
	case codeVolumeInput:
		constraint, next = "canonical_name_and_absolute_ledger_child", "Volume input does not meet the local name/state-path constraint. Inspect the volume declaration and state path privately."
	case codeVolumeKey:
		constraint, next = "canonical_standard_base64_32_bytes", "The managed key input must be present, canonical standard base64 encoding exactly 32 bytes. Check encoding and size privately; this does not establish that the disk key is wrong."
	case codeVolumeRejected:
		constraint, next = "accepted_volume_control_response", "The control endpoint rejected the operation; the rejection reason is unknown. Inspect platform state privately."
	case codeVolumeCancelled:
		constraint, next = "operation_cancelled", "Volume preparation observed cancellation before readiness. Inspect the primary diagnostic and platform state privately."
	case codeVolumeDeadline:
		constraint, next = "operation_deadline_exceeded", "Volume preparation exceeded its deadline before readiness. Inspect the primary diagnostic and platform state privately."
	case codeVolumeSocket:
		constraint, next = "available_safe_control_socket", "The local control socket could not be qualified or accessed; cause unknown. Inspect runtime socket ownership and permissions privately."
	case codeVolumeExchange:
		constraint, next = "completed_volume_control_exchange", "The bounded control exchange did not complete as required; cause unknown. Inspect the primary diagnostic and platform state privately."
	case codeVolumeMount:
		constraint, next = "verified_dedicated_writable_mount", "The dedicated writable volume mount was not verified; cause unknown. Inspect the original mount and state path privately."
	case codeVolumeCleanup:
		constraint, next = "confirmed_volume_resource_closure", "Local volume resource closure is unconfirmed; cause unknown. Inspect the primary diagnostic and runtime privately."
	case codeVolumePlatform:
		constraint, next = "supported_linux_runtime", "Managed volume preparation requires the supported Linux runtime. Inspect the selected runtime privately."
	case codeVolumeUnknown:
		constraint, next = "completed_volume_preparation", "Underlying volume preparation cause unknown. Inspect the primary diagnostic and platform state privately."
	}
	if r.Phase == phaseVolumePrepare {
		// The record retains only the code, not packet progress or cleanup evidence.
		next += " Readiness is unconfirmed; volume operation and cleanup outcomes may be unknown. Preserve the original disk/key and primary diagnostic; do not retry initialization, reset or restore state."
	}
	return healthDiagnostic(r.Code, string(r.Phase), constraint, next)
}
