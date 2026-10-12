package telemetry

import (
	"context"
	"os"

	"github.com/shirou/gopsutil/v4/common"
	"github.com/shirou/gopsutil/v4/process"
)

type processSource func(context.Context) (cpuSeconds float64, rss uint64, valid bool)

func currentProcess(ctx context.Context) (float64, uint64, bool) {
	// Override the library's ambient path configuration, never its numeric source.
	// Only supported Times/MemoryInfo methods are called; PID stays local.
	ctx = context.WithValue(ctx, common.EnvKey, common.EnvMap{
		common.HostProcEnvKey: "/proc", common.HostSysEnvKey: "/sys", common.HostEtcEnvKey: "/etc",
		common.HostVarEnvKey: "/var", common.HostRunEnvKey: "/run", common.HostDevEnvKey: "/dev",
		common.HostRootEnvKey: "/", common.HostProcMountinfo: "/proc/self/mountinfo",
	})
	p, err := process.NewProcessWithContext(ctx, int32(os.Getpid()))
	if err != nil {
		return 0, 0, false
	}
	cpu, err := p.TimesWithContext(ctx)
	if err != nil || cpu == nil {
		return 0, 0, false
	}
	mem, err := p.MemoryInfoWithContext(ctx)
	if err != nil || mem == nil {
		return 0, 0, false
	}
	return cpu.User + cpu.System, mem.RSS, true
}
