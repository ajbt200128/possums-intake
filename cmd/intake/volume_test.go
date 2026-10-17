package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

func syntheticVolumeKey() string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xa5}, 32))
}

func requireVolumeFailure(t *testing.T, err error, code ledger.Code) *ledger.Diagnostic {
	t.Helper()
	var d *ledger.Diagnostic
	kind := failuretype.RuntimeSetupUnknown
	switch code {
	case codeVolumeInput, codeVolumeKey:
		kind = failuretype.RequestInvalid
	case codeVolumeRejected:
		kind = failuretype.AuthenticationRefused
	case codeVolumeCancelled:
		kind = failuretype.Cancelled
	case codeVolumeDeadline:
		kind = failuretype.DeadlineExceeded
	case codeVolumePlatform:
		kind = failuretype.PolicyRefused
	}
	if !errors.As(err, &d) || d.Code != code || d.Kind() != kind {
		t.Fatal("expected closed volume typed diagnosis")
	}
	if d.Stage == "" || d.Constraint == "" || d.Outcome == "" || d.NextStep == "" {
		t.Fatal("incomplete volume diagnosis")
	}
	wire, marshalErr := json.Marshal(d)
	if marshalErr != nil {
		t.Fatal("diagnostic serialization unavailable")
	}
	for _, hostile := range []string{"HOSTILE", syntheticVolumeKey(), "/private-secret", "sensitive-volume"} {
		if bytes.Contains(wire, []byte(hostile)) || strings.Contains(fmt.Sprint(d), hostile) || strings.Contains(fmt.Sprintf("%+v", d), hostile) {
			t.Fatal("volume diagnosis disclosed hostile data")
		}
	}
	return d
}

func TestVolumeValidation(t *testing.T) {
	for _, name := range []string{"a", "a-0", strings.Repeat("z", 63)} {
		if !validVolumeName(name) {
			t.Fatal("valid closed name rejected")
		}
	}
	for _, name := range []string{"", "A", "1a", "a/b", "a..", "a\x00", "a_", "é", strings.Repeat("a", 64)} {
		if validVolumeName(name) {
			t.Fatal("hostile name accepted")
		}
	}
	for _, path := range []string{"", "/", "state", "/state", "/state/other", "/state/ledger/", "/a/../state/ledger", "//state/ledger", "/state\n/ledger", "/state\x00/ledger", "/" + strings.Repeat("a", 4096) + "/ledger"} {
		if validVolumeStatePath(path) {
			t.Fatal("noncanonical mount path accepted")
		}
	}
	if !validVolumeStatePath("/state/ledger") {
		t.Fatal("canonical mount rejected")
	}
	for _, key := range []string{"", "HOSTILE", syntheticVolumeKey() + "\n", strings.TrimRight(syntheticVolumeKey(), "="), base64.StdEncoding.EncodeToString(make([]byte, 31)), base64.StdEncoding.EncodeToString(make([]byte, 33)), strings.Repeat("A", 42) + "B="} {
		_, err := decodeVolumeKey(key, true)
		requireVolumeFailure(t, err, codeVolumeKey)
	}
	if _, err := decodeVolumeKey(syntheticVolumeKey(), false); err == nil {
		t.Fatal("missing key accepted")
	}
}

func TestVolumeConsumesEnvironmentBeforeValidationAndBuildsOnePacket(t *testing.T) {
	for _, initialize := range []bool{false, true} {
		t.Setenv(volumeKeyEnv, syntheticVolumeKey())
		calls := 0
		err := prepareManagedVolumeUsing(context.Background(), "sensitive-volume", "/private-secret/ledger", initialize,
			func(ctx context.Context, name, state string, packet []byte, p *volumeProgress) error {
				calls++
				if _, present := os.LookupEnv(volumeKeyEnv); present {
					t.Fatal("key remains in child environment")
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > volumeTimeout || time.Until(deadline) <= 0 {
					t.Fatal("unbounded deadline")
				}
				mode := byte('u')
				if initialize {
					mode = 'i'
				}
				if len(packet) != 34 || packet[0] != 2 || packet[1] != mode || !bytes.Equal(packet[2:], bytes.Repeat([]byte{0xa5}, 32)) {
					t.Fatal("wrong vendor packet")
				}
				return nil
			})
		if err != nil || calls != 1 {
			t.Fatal("preparation did not invoke exactly once")
		}
	}
	t.Setenv(volumeKeyEnv, syntheticVolumeKey())
	err := prepareManagedVolumeUsing(context.Background(), "../HOSTILE", "/private-secret/ledger", false, nil)
	requireVolumeFailure(t, err, codeVolumeInput)
	if _, present := os.LookupEnv(volumeKeyEnv); present {
		t.Fatal("invalid options retained key environment")
	}
}

func TestVolumeCancellationPanicAndClosePrivacy(t *testing.T) {
	t.Setenv(volumeKeyEnv, syntheticVolumeKey())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := prepareManagedVolumeUsing(ctx, "ledger", "/state/ledger", false, nil)
	requireVolumeFailure(t, err, codeVolumeCancelled)
	t.Setenv(volumeKeyEnv, syntheticVolumeKey())
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	requireVolumeFailure(t, prepareManagedVolumeUsing(expired, "ledger", "/state/ledger", false, nil), codeVolumeDeadline)
	for _, sent := range []bool{false, true} {
		t.Setenv(volumeKeyEnv, syntheticVolumeKey())
		err := prepareManagedVolumeUsing(context.Background(), "ledger", "/state/ledger", true,
			func(_ context.Context, _, _ string, _ []byte, p *volumeProgress) error {
				p.sent = sent
				panic("HOSTILE")
			})
		d := requireVolumeFailure(t, err, codeVolumeUnknown)
		if sent && (d.Outcome != "volume_outcome_unknown" || !strings.Contains(d.NextStep, "never reset")) {
			t.Fatal("panic lost unknown initialization outcome")
		}
	}
	p := &volumeProgress{sent: true}
	for _, panicClose := range []bool{false, true} {
		primary := error(volumeFailure(codeVolumeExchange, "volume_response", "exact_response_required_cause_unknown", p))
		closeVolumeResource(func() error {
			if panicClose {
				panic("HOSTILE")
			}
			return errors.New("HOSTILE")
		}, &primary, p)
		d := requireVolumeFailure(t, primary, codeVolumeExchange)
		if d.Outcome != "volume_outcome_unknown" || !strings.Contains(d.NextStep, "closure is also unconfirmed") {
			t.Fatal("cleanup erased primary uncertainty")
		}
		var onlyClose error
		closeVolumeResource(func() error { return errors.New("HOSTILE") }, &onlyClose, &volumeProgress{sent: true, ok: true})
		requireVolumeFailure(t, onlyClose, codeVolumeCleanup)
	}
}

func TestVolumeKnownFailureKinds(t *testing.T) {
	for _, test := range []struct {
		err  error
		code ledger.Code
	}{
		{fmt.Errorf("HOSTILE: %w", context.Canceled), codeVolumeCancelled},
		{fmt.Errorf("HOSTILE: %w", context.DeadlineExceeded), codeVolumeDeadline},
		{fmt.Errorf("HOSTILE: %w", os.ErrDeadlineExceeded), codeVolumeDeadline},
		{errors.New("HOSTILE context canceled deadline exceeded"), codeVolumeExchange},
	} {
		d := volumeOperationFailure(context.Background(), test.err, codeVolumeExchange, "volume_response", "response_cause_unknown", &volumeProgress{sent: true})
		requireVolumeFailure(t, d, test.code)
		if d.Outcome != "volume_outcome_unknown" {
			t.Fatal("typed failure erased possible-send outcome")
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	requireVolumeFailure(t, volumeOperationFailure(cancelled, errors.New("HOSTILE"), codeVolumeExchange, "volume_response", "response_cause_unknown", &volumeProgress{}), codeVolumeCancelled)
	requireVolumeFailure(t, volumeOperationFailure(context.Background(), errors.New("HOSTILE"), codeVolumeMount, "volume_mount", "mount_cause_unknown", &volumeProgress{}), codeVolumeMount)
}

func TestVolumeMountEvidence(t *testing.T) {
	valid := "31 25 253:7 / /state rw,nosuid shared:1 - ext4 /dev/mapper/disk rw,errors=remount-ro\n"
	placeholder := "45 25 0:42 / /state ro shared:1 - tmpfs none rw\n"
	otherID := strings.Replace(valid, "31 ", "32 ", 1)
	for _, test := range []struct {
		name, data string
		valid      bool
	}{
		{"exact", valid, true},
		{"covered_placeholder_first_opened_id_not_greatest", placeholder + valid, true},
		{"covered_placeholder_last_opened_id_not_greatest", valid + placeholder, true},
		{"opened_id_greatest", strings.Replace(placeholder, "45 ", "30 ", 1) + valid, true},
		{"missing_selected_id", placeholder + otherID, false},
		{"zero_record_id", strings.Replace(valid, "31 ", "0 ", 1), false},
		{"malformed_record_id", strings.Replace(valid, "31 ", "HOSTILE ", 1), false},
		{"overflow_record_id", strings.Replace(valid, "31 ", "18446744073709551616 ", 1), false},
		{"malformed_other_id", valid + strings.Replace(placeholder, "45 ", "HOSTILE ", 1), false},
		{"duplicate_selected_elsewhere", valid + strings.Replace(valid, "/state", "/other", 1), false},
		{"duplicate_selected_elsewhere_first", strings.Replace(valid, "/state", "/other", 1) + valid, false},
		{"duplicate_selected_numeric_id", valid + strings.Replace(valid, "31 ", "031 ", 1), false},
		{"selected_wrong_target_with_valid_other_id", strings.Replace(valid, "/state", "/other", 1) + otherID, false},
		{"selected_ro_with_writable_other_id", strings.Replace(valid, "rw,nosuid", "ro,nosuid", 1) + otherID, false},
		{"selected_wrong_device_with_valid_other_id", strings.Replace(valid, "253:7", "253:8", 1) + otherID, false},
		{"selected_nonblock_with_valid_other_id", strings.Replace(valid, "/dev/mapper/disk", "none", 1) + otherID, false},
		{"parent", strings.Replace(valid, "/state", "/", 1), false},
		{"child", strings.Replace(valid, "/state", "/state/child", 1), false},
		{"wrong_device", strings.Replace(valid, "253:7", "253:8", 1), false},
		{"zero_major", strings.Replace(valid, "253:7", "0:7", 1), false},
		{"bind_subdir", strings.Replace(valid, "253:7 / ", "253:7 /subdir ", 1), false},
		{"mount_ro", strings.Replace(valid, "rw,nosuid", "ro,nosuid", 1), false},
		{"super_ro", strings.Replace(valid, "rw,errors", "ro,errors", 1), false},
		{"contradictory", strings.Replace(valid, "rw,nosuid", "rw,ro,nosuid", 1), false},
		{"ephemeral", strings.Replace(valid, "ext4", "tmpfs", 1), false},
		{"nonblock", strings.Replace(valid, "/dev/mapper/disk", "none", 1), false},
		{"fuse", strings.Replace(valid, "ext4", "fuseblk", 1), false},
		{"duplicate", valid + valid, false},
		{"child_bind_same_device", valid + strings.Replace(otherID, "/state", "/state/ledger", 1), false},
		{"child_bind_other_device", valid + strings.Replace(strings.Replace(otherID, "/state", "/state/ledger", 1), "253:7", "253:8", 1), false},
		{"nested_child_mount", valid + strings.Replace(otherID, "/state", "/state/ledger/data.db", 1), false},
		{"covered_layout_child_bind", placeholder + valid + strings.Replace(otherID, "/state", "/state/ledger", 1), false},
		{"unrelated_lost_found_mount", valid + strings.Replace(otherID, "/state", "/state/lost+found", 1), true},
		{"truncated", "31 25 253:7 / /state rw - ext4\n", false},
		{"unterminated", strings.TrimSuffix(valid, "\n"), false},
		{"bad_escape", strings.Replace(valid, "/state", `/st\999ate`, 1), false},
		{"long_line", valid + strings.Repeat("a", 16*1024), false},
		{"total_bound", valid + strings.Repeat("1 2 0:1 / /other rw - tmpfs none rw\n", 40000), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := hasVolumeMount(context.Background(), strings.NewReader(test.data), "/state", 31, 253, 7); got != test.valid {
				t.Fatal("mount evidence verdict mismatch")
			}
		})
	}
	if got, ok := decodeMountField(`/state\040space\134name`); !ok || got != "/state space\\name" {
		t.Fatal("kernel mount escape decoding")
	}
	if hasVolumeMount(context.Background(), failingVolumeReader{}, "/state", 31, 253, 7) {
		t.Fatal("read failure accepted")
	}
	if hasVolumeMount(context.Background(), strings.NewReader(valid), "/state", 0, 253, 7) {
		t.Fatal("zero opened mount identity accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if hasVolumeMount(ctx, strings.NewReader(valid), "/state", 31, 253, 7) {
		t.Fatal("cancelled mount evidence accepted")
	}
}

type failingVolumeReader struct{}

func (failingVolumeReader) Read([]byte) (int, error) { return 0, errors.New("HOSTILE") }

func TestVolumeUnsupportedRuntime(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("non-Linux refusal only")
	}
	t.Setenv(volumeKeyEnv, syntheticVolumeKey())
	requireVolumeFailure(t, prepareManagedVolume(context.Background(), "ledger", "/state/ledger", false), codeVolumePlatform)
}

func TestVolumeLinuxRuntimeAvailability(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("actual Linux SOCK_SEQPACKET and mount checks require Linux; not executed on this host")
	}
	// Linux runtime coverage is in volume_linux_test.go, not emulated here.
}
