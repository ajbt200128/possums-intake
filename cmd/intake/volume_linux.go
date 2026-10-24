//go:build linux

package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func preparePlatformVolume(ctx context.Context, name, state string, packet []byte, initialize bool, progress *volumeProgress) (result error) {
	path := "/run/tinfoil/volumes/" + name + "/control.sock"
	info, err := os.Lstat(path)
	if err != nil {
		return volumeOperationFailure(ctx, err, codeVolumeSocket, "volume_socket", "socket_metadata_cause_unknown", progress)
	}
	if !validVolumeSocket(info, uint32(os.Geteuid())) {
		return volumeFailure(codeVolumeSocket, "volume_socket", "owned_0600_seqpacket_socket", progress)
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unixpacket", path)
	if err != nil {
		return volumeOperationFailure(ctx, err, codeVolumeExchange, "volume_connect", "connect_cause_unknown", progress)
	}
	if err = exchangeVolumePacket(ctx, conn.(*net.UnixConn), packet, initialize, progress); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return volumeOperationFailure(ctx, err, codeVolumeMount, "volume_mount", "context_unavailable", progress)
	}
	return verifyVolumeMount(ctx, state, progress)
}

func validVolumeSocket(info os.FileInfo, uid uint32) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode()&os.ModeSocket != 0 && info.Mode()&os.ModeSymlink == 0 && info.Mode().Perm() == 0600 && info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0 && stat.Uid == uid
}

type volumePacketConn interface {
	SetDeadline(time.Time) error
	Write([]byte) (int, error)
	ReadMsgUnix([]byte, []byte) (int, int, int, *net.UnixAddr, error)
	Close() error
}

// Internal socket seam only; the production entry point never accepts a path.
func exchangeVolumePacket(ctx context.Context, conn volumePacketConn, packet []byte, initialize bool, progress *volumeProgress) (result error) {
	defer closeVolumeResource(conn.Close, &result, progress)
	if err := ctx.Err(); err != nil {
		return volumeOperationFailure(ctx, err, codeVolumeExchange, "volume_exchange", "context_unavailable", progress)
	}
	deadline, ok := ctx.Deadline()
	if !ok || deadline.After(time.Now().Add(volumeOperationTimeout(initialize))) {
		return volumeFailure(codeVolumeExchange, "volume_exchange", "finite_deadline_required", progress)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return volumeOperationFailure(ctx, err, codeVolumeExchange, "volume_exchange", "deadline_setup_cause_unknown", progress)
	}
	progress.sent = true
	n, err := conn.Write(packet) // Exactly one atomic SOCK_SEQPACKET write; never retry.
	if err != nil || n != len(packet) {
		return volumeOperationFailure(ctx, err, codeVolumeExchange, "volume_send", "single_complete_packet_cause_unknown", progress)
	}
	var response [9]byte // Longest allowed response is eight bytes.
	n, _, flags, _, err := conn.ReadMsgUnix(response[:], nil)
	if err != nil || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 || n > 8 {
		return volumeOperationFailure(ctx, err, codeVolumeExchange, "volume_response", "bounded_complete_response_cause_unknown", progress)
	}
	switch string(response[:n]) {
	case "ok":
		progress.ok = true
		if ctx.Err() != nil {
			return volumeOperationFailure(ctx, ctx.Err(), codeVolumeExchange, "volume_response", "context_unavailable", progress)
		}
		return nil
	case "rejected":
		return volumeFailure(codeVolumeRejected, "volume_response", "control_request_rejected", progress)
	case "failed":
		return volumeFailure(codeVolumeExchange, "volume_response", "platform_operation_failed_cause_unknown", progress)
	default:
		return volumeFailure(codeVolumeExchange, "volume_response", "exact_response_required_cause_unknown", progress)
	}
}

func verifyVolumeMount(ctx context.Context, state string, progress *volumeProgress) (result error) {
	mount := filepath.Dir(state)
	// Keep the verified parent descriptor open through the child check. Walk
	// without symlinks and never create, remove or probe filesystem entries.
	fd, err := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return volumeOperationFailure(ctx, err, codeVolumeMount, "volume_mount", "directory_open_cause_unknown", progress)
	}
	defer func() { closeVolumeResource(func() error { return unix.Close(fd) }, &result, progress) }()
	for _, component := range strings.Split(strings.TrimPrefix(mount, "/"), "/") {
		if component == "" {
			continue
		} // Root parent has no components.
		if err := ctx.Err(); err != nil {
			return volumeOperationFailure(ctx, err, codeVolumeMount, "volume_mount", "context_unavailable", progress)
		}
		next, openErr := unix.Openat(fd, component, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if openErr != nil {
			return volumeOperationFailure(ctx, openErr, codeVolumeMount, "volume_mount", "non_symlink_directory_required", progress)
		}
		old := fd
		fd = next
		if closeErr := unix.Close(old); closeErr != nil {
			return volumeOperationFailure(ctx, closeErr, codeVolumeCleanup, "volume_cleanup", "directory_close_cause_unknown", progress)
		}
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return volumeOperationFailure(ctx, err, codeVolumeMount, "volume_mount", "directory_stat_cause_unknown", progress)
	}
	actual, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(fd))
	if err != nil || actual != mount {
		return volumeOperationFailure(ctx, err, codeVolumeMount, "volume_mount", "unchanged_exact_mount_path", progress)
	}
	mountID, ok := volumeMountID(fd)
	if !ok {
		return volumeOperationFailure(ctx, nil, codeVolumeMount, "volume_mount", "opened_mount_identity_unavailable_cause_unknown", progress)
	}
	mounts, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return volumeOperationFailure(ctx, err, codeVolumeMount, "volume_mount", "mount_evidence_open_cause_unknown", progress)
	}
	defer closeVolumeResource(mounts.Close, &result, progress)
	if !hasVolumeMount(ctx, mounts, mount, mountID, unix.Major(uint64(stat.Dev)), unix.Minor(uint64(stat.Dev))) {
		return volumeOperationFailure(ctx, nil, codeVolumeMount, "volume_mount", "exact_dedicated_block_mount_rw_without_child_mount_required", progress)
	}
	return verifyVolumeChild(ctx, fd, uint64(stat.Dev), progress)
}

func volumeMountID(fd int) (uint64, bool) {
	var stat unix.Statx_t
	if unix.Statx(fd, "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &stat) != nil ||
		stat.Mask&unix.STATX_MNT_ID == 0 || stat.Mnt_id == 0 {
		return 0, false
	}
	return stat.Mnt_id, true
}

// Missing ledger is permitted only after the caller verifies its exact parent
// mount and excludes explicit child/nested mounts. Init/Open own creation/refusal.
func verifyVolumeChild(ctx context.Context, parentFD int, parentDev uint64, progress *volumeProgress) (result error) {
	if err := ctx.Err(); err != nil {
		return volumeOperationFailure(ctx, err, codeVolumeMount, "volume_child", "context_unavailable", progress)
	}
	fd, err := unix.Openat(parentFD, "ledger", unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) && ctx.Err() == nil {
			return nil
		}
		return volumeOperationFailure(ctx, err, codeVolumeMount, "volume_child", "non_symlink_ledger_directory_required", progress)
	}
	defer closeVolumeResource(func() error { return unix.Close(fd) }, &result, progress)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return volumeOperationFailure(ctx, err, codeVolumeMount, "volume_child", "child_stat_cause_unknown", progress)
	}
	if uint64(stat.Dev) != parentDev {
		return volumeFailure(codeVolumeMount, "volume_child", "same_parent_device_required", progress)
	}
	if err := ctx.Err(); err != nil {
		return volumeOperationFailure(ctx, err, codeVolumeMount, "volume_child", "context_unavailable", progress)
	}
	return nil
}
