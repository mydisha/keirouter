package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"

	"github.com/mydisha/keirouter/backend/internal/store"
)

const (
	historySize    = 60 // 5 min at 5s interval
	sampleInterval = 5 * time.Second
	cpuSpikeThresh = 80.0
	memSpikeThresh = 85.0
	diskWarnThresh = 90.0

	// DB persistence settings for resource_samples. The interval must be a
	// multiple of sampleInterval: each row averages the ticks since the last.
	resourceDBInterval = 15 * time.Second
	resourceRetention  = 7 * 24 * time.Hour // 7 days
)

// SystemSample holds one point-in-time snapshot for the history ring buffer.
type SystemSample struct {
	Timestamp   int64   `json:"ts"`
	CPUPct      float64 `json:"cpu_pct"`
	MemUsedPct  float64 `json:"mem_pct"`
	Goroutines  int     `json:"goroutines"`
	HeapAllocMB float64 `json:"heap_mb"`
	IsCPUSpike  bool    `json:"cpu_spike,omitempty"`
	IsMemSpike  bool    `json:"mem_spike,omitempty"`

	// Process-level metrics (keirouter's own resource usage). ProcCPUPct is
	// the share of the whole host's CPU capacity, on the same 0–100 scale as
	// CPUPct, so the two can share an axis.
	ProcCPUPct  float64 `json:"proc_cpu_pct"`
	ProcRSSMB   float64 `json:"proc_rss_mb"`
	ProcThreads int32   `json:"proc_threads,omitempty"`
	ProcOpenFDs int32   `json:"proc_open_fds,omitempty"`
}

// SystemSnapshot is the detailed payload for the latest collector tick.
type SystemSnapshot struct {
	SampledAt int64 `json:"sampled_at"`
	// WindowS is how many seconds the CPU percentages are averaged over.
	WindowS float64 `json:"window_s"`

	// CPU
	CPUPct     float64   `json:"cpu_pct"`
	CPUPerCore []float64 `json:"cpu_per_core"`
	CPUCount   int       `json:"cpu_count"`
	CPUModel   string    `json:"cpu_model,omitempty"`
	// LoadAvg is the 1, 5 and 15 minute load average (absent on Windows).
	LoadAvg []float64 `json:"load_avg,omitempty"`

	// Memory
	MemTotalMB     uint64  `json:"mem_total_mb"`
	MemUsedMB      uint64  `json:"mem_used_mb"`
	MemAvailableMB uint64  `json:"mem_available_mb"`
	MemUsedPct     float64 `json:"mem_pct"`
	SwapTotalMB    uint64  `json:"swap_total_mb"`
	SwapUsedMB     uint64  `json:"swap_used_mb"`

	// Disk (the volume holding the working directory)
	DiskPath    string  `json:"disk_path"`
	DiskTotalGB float64 `json:"disk_total_gb"`
	DiskUsedGB  float64 `json:"disk_used_gb"`
	DiskFreeGB  float64 `json:"disk_free_gb"`
	DiskUsedPct float64 `json:"disk_pct"`

	// Go runtime
	Goroutines  int     `json:"goroutines"`
	HeapAllocMB float64 `json:"heap_alloc_mb"`
	HeapSysMB   float64 `json:"heap_sys_mb"`
	HeapInuseMB float64 `json:"heap_inuse_mb"`
	HeapIdleMB  float64 `json:"heap_idle_mb"`
	NextGCMB    float64 `json:"next_gc_mb"`

	// GC
	GCPauseTotalMs float64 `json:"gc_pause_total_ms"`
	GCPauseLastMs  float64 `json:"gc_pause_last_ms"`
	GCCycles       uint32  `json:"gc_cycles"`

	// Process & host identity
	UptimeS     int64  `json:"uptime_s"`
	HostUptimeS int64  `json:"host_uptime_s"`
	PID         int    `json:"pid"`
	Host        string `json:"host"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	GoVersion   string `json:"go_version"`

	// Process-level metrics (keirouter's own resource usage).
	ProcCPUPct    float64 `json:"proc_cpu_pct"`   // share of total host CPU, 0–100
	ProcCPUCores  float64 `json:"proc_cpu_cores"` // cores' worth of CPU in use
	ProcRSSMB     float64 `json:"proc_rss_mb"`
	ProcThreads   int32   `json:"proc_threads"`
	ProcOpenFDs   int32   `json:"proc_open_fds"`
	ProcFDLimit   uint64  `json:"proc_fd_limit,omitempty"`
	ProcConns     int     `json:"proc_conns"`
	ProcConnsOpen int     `json:"proc_conns_established"`
}

// systemHistory holds a circular buffer of samples.
type systemHistory struct {
	mu      sync.RWMutex
	buf     [historySize]SystemSample
	head    int
	count   int
	started time.Time
}

func newSystemHistory() *systemHistory {
	return &systemHistory{started: time.Now()}
}

func (h *systemHistory) push(s SystemSample) {
	h.mu.Lock()
	h.buf[h.head] = s
	h.head = (h.head + 1) % historySize
	if h.count < historySize {
		h.count++
	}
	h.mu.Unlock()
}

func (h *systemHistory) samples() []SystemSample {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]SystemSample, h.count)
	for i := 0; i < h.count; i++ {
		idx := (h.head - h.count + i + historySize) % historySize
		out[i] = h.buf[idx]
	}
	return out
}

// Global; shared between the background collector and HTTP handlers.
var sysHistory = newSystemHistory()

// sysMonitor owns the CPU counters and the latest snapshot. collectMu
// serializes collect, which owns the previous-counter fields; latest is
// guarded separately because handlers read it.
//
// CPU percentages are computed from our own counter deltas instead of
// gopsutil's Percent(0) helpers: those keep one package-global "last call"
// (and a per-Process one), so every extra caller — an HTTP request, the DB
// writer — shrank the measured window to milliseconds and produced 0%/100%
// noise, and a freshly built Process always reported 0%.
type sysMonitor struct {
	proc    *process.Process
	numCPU  int
	diskDir string

	// Fixed facts read once.
	hostName, osName, arch, cpuModel string
	bootTime                         uint64

	collectMu sync.Mutex
	prevHost  cpu.TimesStat
	prevCores []cpu.TimesStat
	prevProc  float64 // process user+system CPU seconds
	prevAt    time.Time
	primed    bool

	mu     sync.RWMutex
	latest SystemSnapshot
}

var (
	sysMonOnce sync.Once
	sysMonInst *sysMonitor
)

// sysMon returns the process-wide monitor, reading the fixed host facts on
// first use rather than at package init (test binaries never pay for it).
func sysMon() *sysMonitor {
	sysMonOnce.Do(func() { sysMonInst = newSysMonitor() })
	return sysMonInst
}

func newSysMonitor() *sysMonitor {
	m := &sysMonitor{numCPU: runtime.NumCPU(), diskDir: diskDirectory()}
	if p, err := process.NewProcess(int32(os.Getpid())); err == nil {
		m.proc = p
	}
	if hi, err := host.Info(); err == nil {
		m.hostName = hi.Hostname
		m.osName = hi.Platform + " " + hi.PlatformVersion
		m.arch = hi.KernelArch
		m.bootTime = hi.BootTime
	}
	if infos, err := cpu.Info(); err == nil && len(infos) > 0 {
		m.cpuModel = infos[0].ModelName
	}
	if n, err := cpu.Counts(true); err == nil && n > 0 {
		m.numCPU = n
	}
	return m
}

// diskDirectory is the mount point worth reporting: the volume KeiRouter runs
// from (where its SQLite file and logs live), not a hard-coded "/" that may
// be a different disk or, on Windows, not exist.
func diskDirectory() string {
	wd, err := os.Getwd()
	if err != nil {
		return filepath.VolumeName(os.TempDir()) + string(filepath.Separator)
	}
	best := ""
	if parts, err := disk.Partitions(false); err == nil {
		for _, p := range parts {
			mp := p.Mountpoint
			if len(mp) <= len(best) {
				continue
			}
			if wd == mp || strings.HasPrefix(wd, strings.TrimSuffix(mp, string(filepath.Separator))+string(filepath.Separator)) {
				best = mp
			}
		}
	}
	if best == "" {
		return wd
	}
	return best
}

// snapshot returns the latest collected snapshot with the uptime refreshed.
func (m *sysMonitor) snapshot() SystemSnapshot {
	m.mu.RLock()
	s := m.latest
	m.mu.RUnlock()
	s.UptimeS = int64(time.Since(sysHistory.started).Seconds())
	if m.bootTime > 0 {
		s.HostUptimeS = time.Now().Unix() - int64(m.bootTime)
	}
	return s
}

// collect reads every counter once, computes CPU usage over the time since
// the previous call, stores the snapshot and returns it.
func (m *sysMonitor) collect() SystemSnapshot {
	m.collectMu.Lock()
	defer m.collectMu.Unlock()
	now := time.Now()
	s := SystemSnapshot{
		SampledAt: now.Unix(),
		CPUCount:  m.numCPU,
		CPUModel:  m.cpuModel,
		PID:       os.Getpid(),
		Host:      m.hostName,
		OS:        m.osName,
		Arch:      m.arch,
		GoVersion: runtime.Version(),
		DiskPath:  m.diskDir,
	}
	if s.OS == " " || s.OS == "" {
		s.OS = runtime.GOOS
	}
	if s.Arch == "" {
		s.Arch = runtime.GOARCH
	}

	// CPU: deltas against the previous tick.
	var hostT cpu.TimesStat
	if ts, err := cpu.Times(false); err == nil && len(ts) > 0 {
		hostT = ts[0]
	}
	cores, _ := cpu.Times(true)
	var procCPU float64
	if m.proc != nil {
		if t, err := m.proc.Times(); err == nil && t != nil {
			procCPU = t.User + t.System
		}
	}
	if m.primed {
		s.WindowS = now.Sub(m.prevAt).Seconds()
		s.CPUPct = busyPct(m.prevHost, hostT)
		if len(cores) == len(m.prevCores) {
			s.CPUPerCore = make([]float64, len(cores))
			for i := range cores {
				s.CPUPerCore[i] = busyPct(m.prevCores[i], cores[i])
			}
		}
		if s.WindowS > 0 && procCPU >= m.prevProc {
			s.ProcCPUCores = (procCPU - m.prevProc) / s.WindowS
			if m.numCPU > 0 {
				s.ProcCPUPct = clampPct(s.ProcCPUCores / float64(m.numCPU) * 100)
			}
		}
	}
	if s.CPUPerCore == nil {
		s.CPUPerCore = []float64{}
	}
	m.prevHost, m.prevCores, m.prevProc, m.prevAt, m.primed = hostT, cores, procCPU, now, true

	if avg, err := load.Avg(); err == nil && (avg.Load1 > 0 || avg.Load5 > 0 || avg.Load15 > 0) {
		s.LoadAvg = []float64{avg.Load1, avg.Load5, avg.Load15}
	}

	// Memory
	if vm, err := mem.VirtualMemory(); err == nil {
		s.MemTotalMB = vm.Total / (1024 * 1024)
		s.MemUsedMB = vm.Used / (1024 * 1024)
		s.MemAvailableMB = vm.Available / (1024 * 1024)
		s.MemUsedPct = vm.UsedPercent
	}
	if sw, err := mem.SwapMemory(); err == nil {
		s.SwapTotalMB = sw.Total / (1024 * 1024)
		s.SwapUsedMB = sw.Used / (1024 * 1024)
	}

	// Disk
	if usage, err := disk.Usage(m.diskDir); err == nil {
		s.DiskTotalGB = float64(usage.Total) / (1024 * 1024 * 1024)
		s.DiskUsedGB = float64(usage.Used) / (1024 * 1024 * 1024)
		s.DiskFreeGB = float64(usage.Free) / (1024 * 1024 * 1024)
		s.DiskUsedPct = usage.UsedPercent
	}

	// Go runtime
	s.Goroutines = runtime.NumGoroutine()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	s.HeapAllocMB = float64(ms.HeapAlloc) / (1024 * 1024)
	s.HeapSysMB = float64(ms.HeapSys) / (1024 * 1024)
	s.HeapInuseMB = float64(ms.HeapInuse) / (1024 * 1024)
	s.HeapIdleMB = float64(ms.HeapIdle) / (1024 * 1024)
	s.NextGCMB = float64(ms.NextGC) / (1024 * 1024)
	s.GCPauseTotalMs = float64(ms.PauseTotalNs) / 1e6
	if ms.NumGC > 0 {
		s.GCPauseLastMs = float64(ms.PauseNs[(ms.NumGC+255)%256]) / 1e6
	}
	s.GCCycles = ms.NumGC

	// Process
	if m.proc != nil {
		if mi, err := m.proc.MemoryInfo(); err == nil && mi != nil {
			s.ProcRSSMB = float64(mi.RSS) / (1024 * 1024)
		}
		if t, err := m.proc.NumThreads(); err == nil {
			s.ProcThreads = t
		}
		if fds, err := m.proc.NumFDs(); err == nil {
			s.ProcOpenFDs = fds
		}
		if limits, err := m.proc.Rlimit(); err == nil {
			for _, l := range limits {
				if l.Resource == process.RLIMIT_NOFILE {
					s.ProcFDLimit = l.Soft
				}
			}
		}
	}
	// Only this process's sockets: host-wide enumeration walks every
	// process's fd table and says nothing about the gateway.
	if conns, err := net.ConnectionsPid("all", int32(s.PID)); err == nil {
		s.ProcConns = len(conns)
		for _, c := range conns {
			if c.Status == "ESTABLISHED" {
				s.ProcConnsOpen++
			}
		}
	}

	m.mu.Lock()
	m.latest = s
	m.mu.Unlock()
	return s
}

// busyPct is the share of non-idle time between two counter readings, using
// the same accounting as gopsutil (guest time is already inside user on Linux).
func busyPct(a, b cpu.TimesStat) float64 {
	aAll, aBusy := cpuAllBusy(a)
	bAll, bBusy := cpuAllBusy(b)
	if bAll <= aAll || bBusy < aBusy {
		return 0
	}
	return clampPct((bBusy - aBusy) / (bAll - aAll) * 100)
}

func cpuAllBusy(t cpu.TimesStat) (all, busy float64) {
	all = t.Total()
	if runtime.GOOS == "linux" {
		all -= t.Guest + t.GuestNice
	}
	return all, all - t.Idle - t.Iowait
}

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func sampleFromSnapshot(s SystemSnapshot) SystemSample {
	return SystemSample{
		Timestamp:   s.SampledAt,
		CPUPct:      s.CPUPct,
		MemUsedPct:  s.MemUsedPct,
		Goroutines:  s.Goroutines,
		HeapAllocMB: s.HeapAllocMB,
		IsCPUSpike:  s.CPUPct >= cpuSpikeThresh,
		IsMemSpike:  s.MemUsedPct >= memSpikeThresh,
		ProcCPUPct:  s.ProcCPUPct,
		ProcRSSMB:   s.ProcRSSMB,
		ProcThreads: s.ProcThreads,
		ProcOpenFDs: s.ProcOpenFDs,
	}
}

var sysCollectorOnce sync.Once

// startSystemCollector launches the background goroutine that samples system
// metrics every 5 seconds into the ring buffer. If resRepo is non-nil, every
// 15 seconds it also writes a resource row (CPU averaged over the ticks since
// the previous row) and hourly prunes rows older than 7 days.
//
// The monitor and ring buffer are process-wide, so only the first call starts
// a collector (later calls, e.g. extra Servers in tests, are no-ops): a second
// collector would push duplicate samples and halve the CPU window.
func startSystemCollector(resRepo *store.ResourceRepo) {
	sysCollectorOnce.Do(func() { runSystemCollector(resRepo) })
}

func runSystemCollector(resRepo *store.ResourceRepo) {
	mon := sysMon()
	// Prime the CPU counters; the first real sample follows after a short
	// window so the page isn't empty (or showing 0% CPU) right after boot.
	mon.collect()

	go func() {
		time.Sleep(time.Second)
		sysHistory.push(sampleFromSnapshot(mon.collect()))

		ticker := time.NewTicker(sampleInterval)
		defer ticker.Stop()
		pruneTicker := time.NewTicker(time.Hour)
		defer pruneTicker.Stop()

		perRow := int(resourceDBInterval / sampleInterval)
		var acc []SystemSnapshot
		for {
			select {
			case <-ticker.C:
				snap := mon.collect()
				sysHistory.push(sampleFromSnapshot(snap))
				if resRepo == nil {
					continue
				}
				acc = append(acc, snap)
				if len(acc) < perRow {
					continue
				}
				row := resourceSampleFrom(acc)
				acc = acc[:0]
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				if err := resRepo.InsertResourceSample(ctx, row); err != nil {
					slog.Default().Warn("resource sample persistence failed", "err", err)
				}
				cancel()
			case <-pruneTicker.C:
				if resRepo == nil {
					continue
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				if err := resRepo.PruneResourceSamples(ctx, resourceRetention); err != nil {
					slog.Default().Warn("resource sample pruning failed", "err", err)
				}
				cancel()
			}
		}
	}()
}

// resourceSampleFrom turns the snapshots since the last DB row into one row:
// CPU is averaged across them (so a 15s row covers 15s, not the last 5s),
// everything else comes from the newest.
func resourceSampleFrom(snaps []SystemSnapshot) store.ResourceSample {
	last := snaps[len(snaps)-1]
	var hostCPU, procCPU float64
	for _, s := range snaps {
		hostCPU += s.CPUPct
		procCPU += s.ProcCPUPct
	}
	n := float64(len(snaps))

	row := store.ResourceSample{
		TenantID:           "default",
		CreatedAt:          time.Unix(last.SampledAt, 0),
		Goroutines:         int64(last.Goroutines),
		HeapAllocBytes:     int64(last.HeapAllocMB * 1024 * 1024),
		HeapSysBytes:       int64(last.HeapSysMB * 1024 * 1024),
		GCPauseNS:          int64(last.GCPauseTotalMs * 1e6),
		NextGCBytes:        int64(last.NextGCMB * 1024 * 1024),
		NumGC:              int64(last.GCCycles),
		ProcCPUPercent:     procCPU / n,
		ProcRSSBytes:       int64(last.ProcRSSMB * 1024 * 1024),
		ProcThreads:        int64(last.ProcThreads),
		HostCPUPercent:     hostCPU / n,
		HostMemUsedBytes:   int64(last.MemUsedMB) * 1024 * 1024,
		HostMemTotalBytes:  int64(last.MemTotalMB) * 1024 * 1024,
		HostDiskUsedBytes:  int64(last.DiskUsedGB * 1024 * 1024 * 1024),
		HostDiskTotalBytes: int64(last.DiskTotalGB * 1024 * 1024 * 1024),
	}
	if last.ProcOpenFDs > 0 {
		v := int64(last.ProcOpenFDs)
		row.ProcOpenFDs = &v
	}
	if counters, err := net.IOCounters(false); err == nil && len(counters) > 0 {
		row.HostNetSentBytes = int64(counters[0].BytesSent)
		row.HostNetRecvBytes = int64(counters[0].BytesRecv)
	}
	if len(last.LoadAvg) == 3 {
		l1, l5, l15 := last.LoadAvg[0], last.LoadAvg[1], last.LoadAvg[2]
		row.HostLoad1, row.HostLoad5, row.HostLoad15 = &l1, &l5, &l15
	}
	return row
}

// ---- HTTP handlers ----------------------------------------------------------

// adminSystem returns the latest system snapshot. It reads what the collector
// measured instead of sampling per request, so every viewer sees the same
// numbers the history chart is built from.
func (s *Server) adminSystem(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, sysMon().snapshot())
}

// adminSystemResourceHistory returns time-bucketed resource samples from the
// database for long-term trend charts. Query params: ?hours=24&interval=5m
func (s *Server) adminSystemResourceHistory(w http.ResponseWriter, r *http.Request) {
	if s.resources == nil {
		writeJSON(w, http.StatusOK, []store.ResourceBucket{})
		return
	}
	hours := 24
	if h := r.URL.Query().Get("hours"); h != "" {
		if v, err := time.ParseDuration(h + "h"); err == nil && v > 0 {
			hours = int(v.Hours())
		}
	}
	interval := 5 * time.Minute
	if iv := r.URL.Query().Get("interval"); iv != "" {
		if d, err := time.ParseDuration(iv); err == nil && d >= time.Minute {
			interval = d
		}
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	buckets, err := s.resources.ResourceBuckets(r.Context(), since, interval)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load resource history")
		return
	}
	if buckets == nil {
		buckets = []store.ResourceBucket{}
	}
	writeJSON(w, http.StatusOK, buckets)
}

// adminSystemHistory returns the historical ring buffer of samples.
func (s *Server) adminSystemHistory(w http.ResponseWriter, r *http.Request) {
	type thresholds struct {
		CPU  float64 `json:"cpu_pct"`
		Mem  float64 `json:"mem_pct"`
		Disk float64 `json:"disk_pct"`
	}
	type response struct {
		Interval   int            `json:"interval_sec"`
		MaxSize    int            `json:"max_size"`
		Thresholds thresholds     `json:"thresholds"`
		Spikes     []SystemSample `json:"spikes"`
		Samples    []SystemSample `json:"samples"`
	}
	samples := sysHistory.samples()

	// Extract spikes for quick overview
	spikes := make([]SystemSample, 0)
	for _, s := range samples {
		if s.IsCPUSpike || s.IsMemSpike {
			spikes = append(spikes, s)
		}
	}

	writeJSON(w, http.StatusOK, response{
		Interval:   int(sampleInterval.Seconds()),
		MaxSize:    historySize,
		Thresholds: thresholds{CPU: cpuSpikeThresh, Mem: memSpikeThresh, Disk: diskWarnThresh},
		Spikes:     spikes,
		Samples:    samples,
	})
}
