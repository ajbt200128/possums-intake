//go:build !linux

package main

import "context"

func preparePlatformVolume(_ context.Context, _, _ string, _ []byte, _ bool, progress *volumeProgress) error {
	return volumeFailure(codeVolumePlatform, "volume_platform", "linux_required", progress)
}
