package core

import (
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"
)

// RecoverStream turns a panic in a stream goroutine into an error chunk, so a
// bad frame ends that one stream instead of the whole process. Defer it after
// the deferred close(out), so it runs first.
func RecoverStream(out chan<- StreamChunk) {
	r := recover()
	if r == nil {
		return
	}
	slog.Error("stream goroutine panicked", "panic", r, "stack", string(debug.Stack()))
	chunk := StreamChunk{Type: ChunkError, Err: &ProviderError{Kind: ErrInternal, Message: fmt.Sprintf("stream failed: %v", r)}}
	select {
	case out <- chunk:
	case <-time.After(time.Second):
	}
}
