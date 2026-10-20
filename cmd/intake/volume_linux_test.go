//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"possums-intake/internal/ledger"
)

type fakeVolumeConn struct {
	response                                 string
	flags                                    int
	writeN                                   int
	writeErr, readErr, deadlineErr, closeErr error
	writes, reads, closes                    int
	afterRead                                func()
}

func (c *fakeVolumeConn) SetDeadline(time.Time) error { return c.deadlineErr }
func (c *fakeVolumeConn) Write(p []byte) (int, error) {
	c.writes++
	if c.writeN >= 0 {
		return c.writeN, c.writeErr
	}
	return len(p), c.writeErr
}
func (c *fakeVolumeConn) ReadMsgUnix(p, _ []byte) (int, int, int, *net.UnixAddr, error) {
	c.reads++
	if c.afterRead != nil {
		c.afterRead()
	}
	return copy(p, c.response), 0, c.flags, nil, c.readErr
}
func (c *fakeVolumeConn) Close() error { c.closes++; return c.closeErr }

func TestVolumeExchangeClosedProtocol(t *testing.T) {
	for _, response := range []string{"", "o", "ok\n", "ok\x00", "OK", "HOSTILE", "rejected", "failed", "okHOSTILE"} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		c := &fakeVolumeConn{response: response, writeN: -1}
		err := exchangeVolumePacket(ctx, c, make([]byte, 34), &volumeProgress{})
		cancel()
		code := codeVolumeExchange
		if response == "rejected" {
			code = codeVolumeRejected
		}
		requireVolumeFailure(t, err, code)
		if c.writes != 1 || c.reads != 1 || c.closes != 1 {
			t.Fatal("exchange retried or did not close")
		}
	}
	for _, c := range []*fakeVolumeConn{
		{response: "ok", writeN: -1, flags: unix.MSG_TRUNC},
		{response: "ok", writeN: -1, flags: unix.MSG_CTRUNC},
		{response: "ok", writeN: 1},
		{writeN: -1, writeErr: errors.New("HOSTILE")},
		{writeN: -1, readErr: errors.New("HOSTILE")},
		{writeN: -1, deadlineErr: errors.New("HOSTILE")},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := exchangeVolumePacket(ctx, c, make([]byte, 34), &volumeProgress{})
		cancel()
		requireVolumeFailure(t, err, codeVolumeExchange)
		if c.writes > 1 || c.reads > 1 || c.closes != 1 {
			t.Fatal("fault triggered retry or leaked socket")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c := &fakeVolumeConn{response: "ok", writeN: -1, closeErr: errors.New("HOSTILE")}
	p := &volumeProgress{}
	d := requireVolumeFailure(t, exchangeVolumePacket(ctx, c, make([]byte, 34), p), codeVolumeCleanup)
	if !p.ok || d.Outcome != "volume_ok_ledger_not_started" {
		t.Fatal("close lost exact ok evidence")
	}
	c = &fakeVolumeConn{writeN: -1}
	requireVolumeFailure(t, exchangeVolumePacket(context.Background(), c, nil, &volumeProgress{}), codeVolumeExchange)
	if c.writes != 0 {
		t.Fatal("sent without finite deadline")
	}
}

func TestVolumeExactOKSurvivesCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c := &fakeVolumeConn{response: "ok", writeN: -1, afterRead: cancel}
	p := &volumeProgress{}
	d := requireVolumeFailure(t, exchangeVolumePacket(ctx, c, make([]byte, 34), p), codeVolumeCancelled)
	if !p.ok || d.Outcome != "volume_ok_ledger_not_started" || c.writes != 1 || c.reads != 1 || c.closes != 1 {
		t.Fatal("cancellation erased exact socket ok evidence")
	}
}

func TestVolumeTypedSocketFailures(t *testing.T) {
	for _, test := range []struct {
		err  error
		code ledger.Code
	}{
		{context.Canceled, codeVolumeCancelled},
		{context.DeadlineExceeded, codeVolumeDeadline},
		{os.ErrDeadlineExceeded, codeVolumeDeadline},
		{errors.New("HOSTILE"), codeVolumeExchange},
	} {
		for _, stage := range []string{"deadline", "write", "read"} {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			c := &fakeVolumeConn{writeN: -1}
			wrapped := &net.OpError{Op: "HOSTILE", Net: "HOSTILE", Err: test.err}
			switch stage {
			case "deadline":
				c.deadlineErr = wrapped
			case "write":
				c.writeErr = wrapped
			case "read":
				c.readErr = wrapped
			}
			p := &volumeProgress{}
			d := requireVolumeFailure(t, exchangeVolumePacket(ctx, c, make([]byte, 34), p), test.code)
			cancel()
			if c.writes > 1 || c.reads > 1 || c.closes != 1 || (stage != "deadline" && d.Outcome != "volume_outcome_unknown") {
				t.Fatal("typed socket failure changed ownership or outcome")
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	c := &fakeVolumeConn{response: "rejected", writeN: -1, afterRead: cancel, closeErr: os.ErrDeadlineExceeded}
	p := &volumeProgress{}
	d := requireVolumeFailure(t, exchangeVolumePacket(ctx, c, make([]byte, 34), p), codeVolumeRejected)
	if !strings.Contains(d.NextStep, "closure is also unconfirmed") {
		t.Fatal("cleanup erased refusal")
	}
}

func TestVolumeRealUnixpacket(t *testing.T) {
	// Short path avoids the kernel's sockaddr_un path limit on long test roots.
	dir, err := os.MkdirTemp("/tmp", "volume-packet-")
	if err != nil {
		t.Fatal("synthetic socket fixture unavailable")
	}
	defer os.RemoveAll(dir)
	for _, response := range []string{"ok", "rejected", "failed", "ok\n", "ok" + string(bytes.Repeat([]byte{'X'}, 100))} {
		path := filepath.Join(dir, "control.sock")
		listener, err := net.ListenUnix("unixpacket", &net.UnixAddr{Name: path, Net: "unixpacket"})
		if err != nil {
			t.Fatal("Linux unixpacket listener unavailable")
		}
		deadline := time.Now().Add(time.Second)
		listener.SetDeadline(deadline)
		done := make(chan bool, 1)
		packet := append([]byte{2, 'u'}, bytes.Repeat([]byte{0xa5}, 32)...)
		go func() {
			conn, err := listener.AcceptUnix()
			if err != nil {
				done <- false
				return
			}
			conn.SetDeadline(deadline)
			buf := make([]byte, 35)
			n, _, flags, _, readErr := conn.ReadMsgUnix(buf, nil)
			_, writeErr := conn.Write([]byte(response))
			closeErr := conn.Close()
			done <- readErr == nil && writeErr == nil && closeErr == nil && flags&unix.MSG_TRUNC == 0 && bytes.Equal(buf[:n], packet)
		}()
		conn, dialErr := net.DialUnix("unixpacket", nil, &net.UnixAddr{Name: path, Net: "unixpacket"})
		if dialErr != nil {
			listener.Close()
			<-done
			t.Fatal("Linux unixpacket dial unavailable")
		}
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		err = exchangeVolumePacket(ctx, conn, packet, &volumeProgress{})
		cancel()
		serverOK := <-done
		listener.Close()
		if !serverOK {
			t.Fatal("actual vendor packet fixture mismatch")
		}
		if response == "ok" {
			if err != nil {
				t.Fatal("exact ok refused")
			}
		} else {
			code := codeVolumeExchange
			if response == "rejected" {
				code = codeVolumeRejected
			}
			requireVolumeFailure(t, err, code)
		}
	}
}

func TestVolumeRealUnixpacketTimeout(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "volume-timeout-")
	if err != nil {
		t.Fatal("fixture unavailable")
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "control.sock")
	listener, err := net.ListenUnix("unixpacket", &net.UnixAddr{Name: path, Net: "unixpacket"})
	if err != nil {
		t.Fatal("unixpacket unavailable")
	}
	defer listener.Close()
	listener.SetDeadline(time.Now().Add(time.Second))
	done := make(chan bool, 1)
	release := make(chan struct{})
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			done <- false
			return
		}
		conn.SetDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 34)
		n, err := conn.Read(buf)
		<-release
		done <- conn.Close() == nil && err == nil && n == 34
	}()
	conn, err := net.DialUnix("unixpacket", nil, &net.UnixAddr{Name: path, Net: "unixpacket"})
	if err != nil {
		close(release)
		<-done
		t.Fatal("fixture dial unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	p := &volumeProgress{}
	err = exchangeVolumePacket(ctx, conn, make([]byte, 34), p)
	cancel()
	close(release)
	serverOK := <-done
	d := requireVolumeFailure(t, err, codeVolumeDeadline)
	if !serverOK || !p.sent || d.Outcome != "volume_outcome_unknown" {
		t.Fatal("timeout lost possible-send outcome")
	}
}

func TestVolumeSocketMetadata(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "volume-metadata-")
	if err != nil {
		t.Fatal("socket fixture unavailable")
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "control.sock")
	listener, err := net.ListenUnix("unixpacket", &net.UnixAddr{Name: path, Net: "unixpacket"})
	if err != nil {
		t.Fatal("socket fixture unavailable")
	}
	defer listener.Close()
	if os.Chmod(path, 0600) != nil {
		t.Fatal("socket mode fixture unavailable")
	}
	info, err := os.Lstat(path)
	if err != nil || !validVolumeSocket(info, uint32(os.Geteuid())) || validVolumeSocket(info, uint32(os.Geteuid())+1) {
		t.Fatal("socket owner validation mismatch")
	}
	if os.Chmod(path, 0660) != nil {
		t.Fatal("socket mode fixture unavailable")
	}
	info, err = os.Lstat(path)
	if err != nil || validVolumeSocket(info, uint32(os.Geteuid())) {
		t.Fatal("unsafe socket mode accepted")
	}
	link := filepath.Join(dir, "link")
	if os.Symlink(path, link) != nil {
		t.Fatal("socket symlink fixture unavailable")
	}
	info, err = os.Lstat(link)
	if err != nil || validVolumeSocket(info, uint32(os.Geteuid())) {
		t.Fatal("symlink socket accepted")
	}
	info, err = os.Lstat(dir)
	if err != nil || validVolumeSocket(info, uint32(os.Geteuid())) {
		t.Fatal("directory socket accepted")
	}
}

func TestVolumeLedgerChildPreservesFilesystemEntries(t *testing.T) {
	parent := t.TempDir()
	lost := filepath.Join(parent, "lost+found")
	if os.Mkdir(lost, 0700) != nil || os.WriteFile(filepath.Join(lost, "fixture"), []byte("synthetic-filesystem-entry"), 0600) != nil {
		t.Fatal("filesystem-owned entry fixture unavailable")
	}
	fd, err := unix.Open(parent, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal("parent fixture unavailable")
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil {
		t.Fatal("parent fixture metadata unavailable")
	}
	p := &volumeProgress{sent: true, ok: true}
	child := filepath.Join(parent, "ledger")
	if verifyVolumeChild(context.Background(), fd, uint64(stat.Dev), p) != nil {
		t.Fatal("missing ledger child refused")
	}
	if _, err := os.Lstat(child); !os.IsNotExist(err) {
		t.Fatal("helper created ledger child")
	}
	if entries, err := os.ReadDir(parent); err != nil || len(entries) != 1 || entries[0].Name() != "lost+found" {
		t.Fatal("helper changed filesystem-owned entries")
	}
	if os.Mkdir(child, 0700) != nil {
		t.Fatal("ledger child fixture unavailable")
	}
	if verifyVolumeChild(context.Background(), fd, uint64(stat.Dev), p) != nil {
		t.Fatal("ordinary same-device directory refused")
	}
	if entries, err := os.ReadDir(child); err != nil || len(entries) != 0 {
		t.Fatal("helper wrote child state")
	}
	requireVolumeFailure(t, verifyVolumeChild(context.Background(), fd, uint64(stat.Dev)^1, p), codeVolumeMount)
	if os.Remove(child) != nil || os.WriteFile(child, []byte("synthetic-wrong-entry"), 0600) != nil {
		t.Fatal("wrong child fixture unavailable")
	}
	requireVolumeFailure(t, verifyVolumeChild(context.Background(), fd, uint64(stat.Dev), p), codeVolumeMount)
	if os.Remove(child) != nil || os.Symlink(lost, child) != nil {
		t.Fatal("symlink child fixture unavailable")
	}
	requireVolumeFailure(t, verifyVolumeChild(context.Background(), fd, uint64(stat.Dev), p), codeVolumeMount)
	if info, err := os.Lstat(child); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("helper changed rejected child")
	}
	if data, err := os.ReadFile(filepath.Join(lost, "fixture")); err != nil || string(data) != "synthetic-filesystem-entry" {
		t.Fatal("helper removed filesystem-owned state")
	}
}

func TestVolumeMountIDOpenedFD(t *testing.T) {
	fd, err := unix.Open(t.TempDir(), unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal("mount identity fixture unavailable")
	}
	defer func() {
		if unix.Close(fd) != nil {
			t.Error("mount identity fixture closure unconfirmed")
		}
	}()
	if id, ok := volumeMountID(fd); !ok || id == 0 {
		t.Fatal("opened directory mount identity unavailable")
	}
}

func TestVolumeMountIDInvalidFD(t *testing.T) {
	if id, ok := volumeMountID(-1); ok || id != 0 {
		t.Fatal("invalid descriptor mount identity accepted")
	}
}

func TestVolumeMountRejectsDirectorySymlinkAndMissing(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "link")
	if os.Symlink(dir, link) != nil {
		t.Fatal("fixture symlink unavailable")
	}
	for _, state := range []string{filepath.Join(dir, "ledger"), filepath.Join(link, "ledger"), filepath.Join(link, "missing", "ledger"), filepath.Join(dir, "missing", "ledger")} {
		requireVolumeFailure(t, verifyVolumeMount(context.Background(), state, &volumeProgress{sent: true, ok: true}), codeVolumeMount)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("mount verification wrote probe state")
	}
}
