package main

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"possums-intake/internal/ledger"
)

const (
	volumeKeyEnv                    = "LEDGER_VOLUME_KEY_B64"
	volumeTimeout                   = 30 * time.Second
	codeVolumeInput     ledger.Code = "LEDGER_VOLUME_INPUT_INVALID"
	codeVolumeKey       ledger.Code = "LEDGER_VOLUME_KEY_INVALID"
	codeVolumeRejected  ledger.Code = "LEDGER_VOLUME_KEY_REJECTED"
	codeVolumeCancelled ledger.Code = "LEDGER_VOLUME_CANCELLED"
	codeVolumeDeadline  ledger.Code = "LEDGER_VOLUME_DEADLINE_EXCEEDED"
	codeVolumeSocket    ledger.Code = "LEDGER_VOLUME_SOCKET_UNAVAILABLE"
	codeVolumeExchange  ledger.Code = "LEDGER_VOLUME_EXCHANGE_UNAVAILABLE"
	codeVolumeMount     ledger.Code = "LEDGER_VOLUME_MOUNT_UNVERIFIED"
	codeVolumeCleanup   ledger.Code = "LEDGER_VOLUME_CLEANUP_UNCONFIRMED"
	codeVolumeUnknown   ledger.Code = "LEDGER_VOLUME_CAUSE_UNKNOWN"
	codeVolumePlatform  ledger.Code = "LEDGER_VOLUME_PLATFORM_UNSUPPORTED"
)

// The caller owns failure observation and calls this exactly once before ledger
// work. Unsetting affects future child environments, not memory erasure, native
// process history, or the platform's managed-secret custody.
func prepareManagedVolume(ctx context.Context, name, state string, initialize bool) error {
	return prepareManagedVolumeUsing(ctx, name, state, initialize, preparePlatformVolume)
}

func prepareManagedVolumeUsing(ctx context.Context, name, state string, initialize bool, run func(context.Context, string, string, []byte, *volumeProgress) error) (result error) {
	progress := &volumeProgress{}
	defer func() {
		if recover() != nil {
			result = volumeFailure(codeVolumeUnknown, "volume_prepare", "cause_unknown", progress)
		}
	}()
	encoded, present := os.LookupEnv(volumeKeyEnv)
	unsetErr := os.Unsetenv(volumeKeyEnv)
	if unsetErr != nil {
		return volumeFailure(codeVolumeUnknown, "volume_key", "environment_removal_cause_unknown", progress)
	}
	ctx, cancel := context.WithTimeout(ctx, volumeTimeout)
	defer cancel()
	if !validVolumeName(name) || !validVolumeStatePath(state) {
		return volumeFailure(codeVolumeInput, "volume_options", "canonical_name_and_absolute_ledger_child", progress)
	}
	key, err := decodeVolumeKey(encoded, present)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return volumeOperationFailure(ctx, err, codeVolumeExchange, "volume_prepare", "context_unavailable", progress)
	}
	packet := make([]byte, 2+len(key))
	packet[0], packet[1] = 0x02, 'u'
	if initialize {
		packet[1] = 'i'
	}
	copy(packet[2:], key)
	return run(ctx, name, state, packet, progress)
}

func validVolumeName(name string) bool {
	if len(name) == 0 || len(name) > 63 || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, c := range name[1:] {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func validVolumeStatePath(state string) bool {
	if len(state) > 4096 || !filepath.IsAbs(state) || filepath.Clean(state) != state || filepath.Base(state) != "ledger" {
		return false
	}
	return !strings.ContainsAny(state, "\x00\r\n\t")
}

func decodeVolumeKey(encoded string, present bool) ([]byte, error) {
	// DecodeString ignores CR/LF even in Strict mode, so round-trip canonicality
	// is required in addition to strict padding-bit and exact-size checks.
	if present && len(encoded) == 44 {
		key, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err == nil && len(key) == 32 && base64.StdEncoding.EncodeToString(key) == encoded {
			return key, nil
		}
	}
	return nil, volumeFailure(codeVolumeKey, "volume_key", "canonical_standard_base64_32_bytes", &volumeProgress{})
}

type volumeProgress struct {
	sent bool // Set BEFORE Write: any write failure can have an unknown outcome.
	ok   bool
}

func volumeFailure(code ledger.Code, stage, constraint string, progress *volumeProgress) *ledger.Diagnostic {
	outcome := "no_volume_request_sent"
	next := "Volume preparation stopped; cause unknown unless a validation constraint is stated. Check the managed secret, volume declaration and local permissions privately. No ledger work is authorized."
	if progress.sent {
		outcome = "volume_outcome_unknown"
		next = "Volume operation outcome is unknown. Inspect the original disk and platform state privately; never reset it or blindly repeat initialize. No ledger work is authorized."
	}
	if progress.ok {
		outcome = "volume_ok_ledger_not_started"
		next = "The control socket returned ok, but local preparation did not complete. Inspect the original disk and mount privately; never reset it or blindly repeat initialize. No ledger work is authorized."
	}
	if code == codeVolumePlatform {
		next = "Managed volume preparation requires Linux. Use the supported Linux runtime. No ledger work is authorized."
	}
	if code == codeVolumeRejected {
		next = "The control endpoint rejected the operation; the refusal reason is not established. " + next
	}
	return runtimeFailure(code, stage, constraint, outcome, next)
}

// Classify only supported error identity and the actual context, never text.
func volumeOperationFailure(ctx context.Context, err error, fallback ledger.Code, stage, constraint string, progress *volumeProgress) *ledger.Diagnostic {
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, os.ErrDeadlineExceeded) && ctx.Err() != nil {
		err = ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return volumeFailure(codeVolumeCancelled, stage, "operation_cancelled", progress)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return volumeFailure(codeVolumeDeadline, stage, "operation_deadline_exceeded", progress)
	}
	return volumeFailure(fallback, stage, constraint, progress)
}

// Cleanup is independently uncertain without replacing the primary diagnosis.
func closeVolumeResource(close func() error, result *error, progress *volumeProgress) {
	failed := false
	var closeErr error
	func() {
		defer func() {
			if recover() != nil {
				failed = true
			}
		}()
		closeErr = close()
		failed = closeErr != nil
	}()
	if !failed {
		return
	}
	if *result == nil {
		*result = volumeOperationFailure(context.Background(), closeErr, codeVolumeCleanup, "volume_cleanup", "close_cause_unknown", progress)
		return
	}
	if primary, ok := (*result).(*ledger.Diagnostic); ok {
		copy := *primary
		copy.NextStep += " Local resource closure is also unconfirmed; inspect privately."
		*result = &copy
	}
}
