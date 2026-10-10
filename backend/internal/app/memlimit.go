package app

import (
	"log/slog"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// cgroupMemoryLimitFraction is the share of a container memory limit handed
// to the Go runtime as a soft limit. The remainder covers non-heap memory
// (goroutine stacks, OS buffers, the SQLite page cache) so the GC starts
// working harder before the kernel's OOM killer does.
const cgroupMemoryLimitFraction = 0.8

// applyMemoryLimit installs a runtime soft memory limit derived from the
// container's cgroup when the operator did not set GOMEMLIMIT explicitly.
// Outside a cgroup (bare metal, desktop) the runtime default is kept: Go's
// GOGC pacing already bounds heap growth relative to live data there.
func applyMemoryLimit(log *slog.Logger) {
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	limit, ok := cgroupMemoryLimit()
	if !ok {
		return
	}
	soft := int64(float64(limit) * cgroupMemoryLimitFraction)
	if soft <= 0 {
		return
	}
	debug.SetMemoryLimit(soft)
	if log != nil {
		log.Info("memory limit applied from cgroup", "cgroup_bytes", limit, "soft_limit_bytes", soft)
	}
}

// cgroupMemoryLimit reads the container memory limit (cgroup v2 first, then
// v1). It returns false when unlimited or unavailable.
func cgroupMemoryLimit() (int64, bool) {
	for _, path := range []string{
		"/sys/fs/cgroup/memory.max",
		"/sys/fs/cgroup/memory/memory.limit_in_bytes",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		v := strings.TrimSpace(string(raw))
		if v == "" || v == "max" {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			continue
		}
		// cgroup v1 reports a huge sentinel when unlimited.
		if n >= 1<<60 {
			continue
		}
		return n, true
	}
	return 0, false
}
