package gateway

import (
	"io"
	"log/slog"
	"sync"
	"time"
)

// sseHeartbeatFrame is an SSE comment event. Spec-compliant SSE clients ignore
// comment lines, so it is safe to interleave between real events; its only job
// is to keep bytes flowing so proxies between KeiRouter and the client do not
// drop the connection as idle while the upstream model is silent.
var sseHeartbeatFrame = []byte(": keep-alive\n\n")

// heartbeatWriter wraps a client stream writer and emits sseHeartbeatFrame
// whenever no data has been written for the configured interval. It exists for
// the direct (zero-copy) stream path, where the copy loop blocks on upstream
// reads and cannot time-share heartbeats itself.
//
// All writes — frames from the copy loop and heartbeats from the background
// goroutine — are serialized by a mutex, and the copy loop only ever writes
// complete SSE frames per Write call, so a heartbeat can never split a frame.
// Flushing is folded into Write so the underlying http.Flusher is also never
// used concurrently.
type heartbeatWriter struct {
	mu       sync.Mutex
	dst      io.Writer
	flush    func()
	interval time.Duration
	last     time.Time
	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
}

// newHeartbeatWriter starts the heartbeat goroutine; callers must invoke stop
// when the stream ends, or the goroutine leaks for the life of the process.
func newHeartbeatWriter(dst io.Writer, flush func(), interval time.Duration) *heartbeatWriter {
	hw := &heartbeatWriter{
		dst:      dst,
		flush:    flush,
		interval: interval,
		last:     time.Now(),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
	go hw.run()
	return hw
}

// Write forwards one complete frame to the client and flushes it immediately,
// matching the per-frame flush behavior of the unwrapped direct path.
func (hw *heartbeatWriter) Write(p []byte) (int, error) {
	hw.mu.Lock()
	defer hw.mu.Unlock()
	n, err := hw.dst.Write(p)
	hw.last = time.Now()
	if err == nil && hw.flush != nil {
		hw.flush()
	}
	return n, err
}

func (hw *heartbeatWriter) run() {
	defer close(hw.doneCh)
	defer func() {
		// A failed heartbeat must stop the beats, not the process.
		if r := recover(); r != nil {
			slog.Error("stream heartbeat panicked", "panic", r)
		}
	}()
	ticker := time.NewTicker(hw.interval)
	defer ticker.Stop()
	for {
		select {
		case <-hw.stopCh:
			return
		case <-ticker.C:
			if !hw.beat() {
				return
			}
		}
	}
}

// beat sends a heartbeat if the stream has been silent for a full interval.
// It reports false when the client is gone, which permanently stops the
// heartbeat loop; the copy loop discovers the same failure on its next write.
func (hw *heartbeatWriter) beat() bool {
	hw.mu.Lock()
	defer hw.mu.Unlock()
	if time.Since(hw.last) < hw.interval {
		return true
	}
	n, err := hw.dst.Write(sseHeartbeatFrame)
	if err != nil || n != len(sseHeartbeatFrame) {
		return false
	}
	hw.last = time.Now()
	if hw.flush != nil {
		hw.flush()
	}
	return true
}

// stop terminates the heartbeat loop and waits until it can no longer write to
// the response. Waiting is required before the handler performs its final flush
// or returns the ResponseWriter to net/http.
func (hw *heartbeatWriter) stop() {
	hw.stopOnce.Do(func() { close(hw.stopCh) })
	<-hw.doneCh
}
