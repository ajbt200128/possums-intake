package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strconv"
	"strings"
)

const volumeMountInfoLimit = 1024 * 1024

// This checks kernel mount evidence, not physical durability, disk identity or
// Tinfoil's actual filesystem type. Those still require platform qualification.
func hasVolumeMount(ctx context.Context, input io.Reader, state string, major, minor uint32) bool {
	child := filepath.Join(state, "ledger")
	limited := &io.LimitedReader{R: input, N: volumeMountInfoLimit + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), 16*1024)
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if atEOF && len(data) > 0 && bytes.IndexByte(data, '\n') < 0 {
			return 0, nil, io.ErrUnexpectedEOF
		}
		return bufio.ScanLines(data, atEOF)
	})
	matches := 0
	valid := false
	for scanner.Scan() {
		if ctx.Err() != nil {
			return false
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			return false
		}
		separator := 6
		for separator < len(fields) && fields[separator] != "-" {
			separator++
		}
		if separator+4 != len(fields) {
			return false
		}
		mount, ok := decodeMountField(fields[4])
		if !ok {
			return false
		}
		if mount == child || strings.HasPrefix(mount, child+"/") {
			return false // Even a same-device bind can redirect accounting paths.
		}
		if mount != state {
			continue
		}
		matches++
		device := strings.Split(fields[2], ":")
		if len(device) != 2 {
			return false
		}
		gotMajor, e1 := strconv.ParseUint(device[0], 10, 32)
		gotMinor, e2 := strconv.ParseUint(device[1], 10, 32)
		source, sourceOK := decodeMountField(fields[separator+2])
		valid = e1 == nil && e2 == nil && major != 0 && gotMajor == uint64(major) && gotMinor == uint64(minor) &&
			fields[3] == "/" && sourceOK && blockVolumeSource(fields[separator+1], source) &&
			mountOptionsRW(fields[5]) && mountOptionsRW(fields[separator+3])
	}
	return scanner.Err() == nil && limited.N > 1 && matches == 1 && valid && ctx.Err() == nil
}

func blockVolumeSource(filesystem, source string) bool {
	if !strings.HasPrefix(source, "/dev/") || len(source) <= len("/dev/") {
		return false
	}
	// No platform filesystem is assumed. Explicitly exclude ephemeral and
	// userspace sources even if hostile fixture metadata claims a block major.
	switch filesystem {
	case "tmpfs", "ramfs", "rootfs", "overlay", "aufs", "devtmpfs", "proc", "sysfs":
		return false
	}
	return !strings.HasPrefix(filesystem, "fuse")
}

func mountOptionsRW(options string) bool {
	rw := false
	for _, option := range strings.Split(options, ",") {
		if option == "ro" {
			return false
		}
		if option == "rw" {
			rw = true
		}
	}
	return rw
}

func decodeMountField(field string) (string, bool) {
	var out strings.Builder
	for i := 0; i < len(field); i++ {
		if field[i] != '\\' {
			out.WriteByte(field[i])
			continue
		}
		if i+3 >= len(field) {
			return "", false
		}
		switch field[i+1 : i+4] {
		case "040":
			out.WriteByte(' ')
		case "011":
			out.WriteByte('\t')
		case "012":
			out.WriteByte('\n')
		case "134":
			out.WriteByte('\\')
		default:
			return "", false
		}
		i += 3
	}
	return out.String(), true
}
