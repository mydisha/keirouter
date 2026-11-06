package gateway

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
)

func TestHeartbeatWriterEmitsAfterIdle(t *testing.T) {
	var dst lockedBuffer
	flushed := make(chan struct{}, 4)
	hw := newHeartbeatWriter(&dst, func() {
		select {
		case flushed <- struct{}{}:
		default:
		}
	}, 10*time.Millisecond)
	t.Cleanup(hw.stop)

	select {
	case <-flushed:
	case <-time.After(time.Second):
		t.Fatal("heartbeat was not flushed after idle interval")
	}
	hw.stop()

	if got := dst.String(); got != string(sseHeartbeatFrame) {
		t.Fatalf("heartbeat output = %q, want %q", got, sseHeartbeatFrame)
	}
}

func TestHeartbeatWriterDataWritePostponesHeartbeat(t *testing.T) {
	var dst lockedBuffer
	interval := 80 * time.Millisecond
	hw := newHeartbeatWriter(&dst, nil, interval)
	t.Cleanup(hw.stop)

	time.Sleep(interval / 2)
	frame := []byte("data: hello\n\n")
	if n, err := hw.Write(frame); err != nil || n != len(frame) {
		t.Fatalf("Write = (%d, %v), want (%d, nil)", n, err, len(frame))
	}

	// First ticker firing occurs less than one interval after the data write.
	time.Sleep(3 * interval / 4)
	hw.stop()
	if got := dst.String(); got != string(frame) {
		t.Fatalf("output before renewed idle interval = %q, want %q", got, frame)
	}
}

func TestHeartbeatWriterStopWaitsForWriter(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	dst := &blockingWriter{entered: entered, release: release}
	hw := newHeartbeatWriter(dst, nil, 5*time.Millisecond)

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("heartbeat write did not start")
	}

	stopped := make(chan struct{})
	go func() {
		hw.stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("stop returned while heartbeat write was active")
	case <-time.After(20 * time.Millisecond):
	}

	close(release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop did not return after heartbeat write completed")
	}
}

func TestHeartbeatWriterStopsAfterWriteFailure(t *testing.T) {
	hw := newHeartbeatWriter(errorWriter{}, nil, 5*time.Millisecond)
	select {
	case <-hw.doneCh:
	case <-time.After(time.Second):
		t.Fatal("heartbeat loop did not stop after write failure")
	}
	// stop remains safe after run exits on its own and when called repeatedly.
	hw.stop()
	hw.stop()
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type blockingWriter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockingWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return len(p), nil
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestDirectStream_FlushesABurstOnce(t *testing.T) {
	var body strings.Builder
	for range 30 {
		body.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
	}
	var dst lockedBuffer
	flushes := 0
	hw := newHeartbeatWriter(&dst, func() { flushes++ }, time.Hour)
	defer hw.stop()

	_, err := copySanitizedStream(hw, strings.NewReader(body.String()), core.DialectOpenAI, hw.Flush)
	require.NoError(t, err)
	hw.Flush()
	require.Equal(t, body.String(), dst.String(), "every frame is written")
	require.LessOrEqual(t, flushes, 3, "frames already buffered upstream go out in one flush")
}
