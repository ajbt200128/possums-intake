//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"possums-intake/internal/failureowner"
	"possums-intake/internal/ledger"
)

// Native Linux qualification, not a Darwin simulation or a successful dedicated
// block-mount claim: an actual unixpacket unlock exchange is followed by the
// actual kernel verifier refusing an ordinary temporary filesystem directory.
func TestLedgerReplaceLinuxUnixpacketThenKernelMountRefusal(t *testing.T) {
	dir := replaceV2Fixture(t, nil)
	before := replaceSnapshot(t, dir)
	socketDir, err := os.MkdirTemp("/tmp", "replace-packet-")
	if err != nil {
		t.Fatal("synthetic socket directory unavailable")
	}
	defer os.RemoveAll(socketDir)
	path := filepath.Join(socketDir, "control.sock")
	listener, err := net.ListenUnix("unixpacket", &net.UnixAddr{Name: path, Net: "unixpacket"})
	if err != nil {
		t.Fatal("native Linux unixpacket unavailable")
	}
	defer listener.Close()
	deadline := time.Now().Add(5 * time.Second)
	if listener.SetDeadline(deadline) != nil {
		t.Fatal("synthetic socket deadline unavailable")
	}
	key := bytes.Repeat([]byte{0x42}, 32)
	t.Setenv(volumeKeyEnv, base64.StdEncoding.EncodeToString(key))
	done := make(chan bool, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			done <- false
			return
		}
		defer conn.Close()
		if conn.SetDeadline(deadline) != nil {
			done <- false
			return
		}
		packet := make([]byte, 35)
		n, _, flags, _, readErr := conn.ReadMsgUnix(packet, nil)
		_, writeErr := conn.Write([]byte("ok"))
		done <- readErr == nil && writeErr == nil && flags&unix.MSG_TRUNC == 0 && n == 34 && bytes.Equal(packet[:n], append([]byte{2, 'u'}, key...))
	}()
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	err = executeLedgerReplaceUsing(ctx, replaceArgs(), nil, replaceCredentials,
		func(ctx context.Context, name, state string, initialize bool) error {
			if name != "ledger" || state != replaceState || initialize {
				t.Fatal("replacement volume binding differs")
			}
			return prepareManagedVolumeUsing(ctx, name, state, initialize, func(ctx context.Context, _, _ string, packet []byte, p *volumeProgress) error {
				conn, err := (&net.Dialer{}).DialContext(ctx, "unixpacket", path)
				if err != nil {
					return volumeOperationFailure(ctx, err, codeVolumeExchange, "volume_connect", "connect_cause_unknown", p)
				}
				if err := exchangeVolumePacket(ctx, conn.(*net.UnixConn), packet, false, p); err != nil {
					return err
				}
				return verifyVolumeMount(ctx, dir, p)
			})
		}, func(context.Context, string, ledger.OperatorCredentials, failureowner.Observer) error {
			t.Fatal("unverified mount admitted replacement")
			return nil
		})
	if !<-done {
		t.Fatal("native exchange was not exactly one unlock packet")
	}
	requireVolumeFailure(t, err, codeVolumeMount)
	after := replaceSnapshot(t, dir)
	for name, digest := range before {
		if after[name] != digest {
			t.Fatal("mount refusal changed predecessor")
		}
	}
}
