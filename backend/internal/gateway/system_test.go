package gateway

import (
	"testing"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/stretchr/testify/require"
)

func TestBusyPct(t *testing.T) {
	a := cpu.TimesStat{User: 100, System: 50, Idle: 850}
	b := cpu.TimesStat{User: 130, System: 60, Idle: 910} // +40 busy, +60 idle
	require.InDelta(t, 40, busyPct(a, b), 0.001)

	// Counters that didn't move (or went backwards after a reset) read as idle.
	require.Zero(t, busyPct(b, b))
	require.Zero(t, busyPct(b, a))

	// iowait counts as idle, matching gopsutil and top.
	c := cpu.TimesStat{User: 100, Idle: 800, Iowait: 100}
	require.InDelta(t, 10, busyPct(cpu.TimesStat{}, c), 0.001)
}

// The process CPU share used to read 0% forever because each sample built a
// fresh gopsutil Process whose first Percent(0) call has nothing to diff.
func TestSysMonitorMeasuresOwnCPU(t *testing.T) {
	m := newSysMonitor()
	m.collect()

	deadline := time.Now().Add(300 * time.Millisecond)
	x := 0
	for time.Now().Before(deadline) {
		x++
	}
	_ = x

	s := m.collect()
	require.Greater(t, s.WindowS, 0.0)
	require.Greater(t, s.ProcCPUCores, 0.1, "a busy loop should register")
	require.LessOrEqual(t, s.ProcCPUPct, 100.0)
	require.Greater(t, s.CPUPct, 0.0)
	require.NotEmpty(t, s.CPUPerCore)
	require.Equal(t, s.SampledAt, m.snapshot().SampledAt, "handlers serve the collected snapshot")
}
