package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
)

const defaultStreamAdmissionTimeout = 60 * time.Second

// Providers in this set are known to report routing failures inside an HTTP
// 200 stream. They must use the parsed channel path so admitStream can inspect
// the first canonical chunks before the gateway commits bytes to the client.
func requiresParsedStreamAdmission(provider string) bool {
	return provider == "codex"
}

// admitStream delays committing a streaming attempt until it produces actual
// model output. Providers sometimes return HTTP 200 and only report auth,
// quota, or capacity failures in the first stream frame. Keeping those frames
// inside the pipeline lets the attempt planner fall back before the gateway has
// written response headers or bytes to the client.
func admitStream(ctx context.Context, in <-chan core.StreamChunk, timeout time.Duration, provider, model string) (<-chan core.StreamChunk, error) {
	return admitStreamNotify(ctx, in, timeout, provider, model, nil, 0)
}

// streamPendingNotifyAfter is how long an admitted-but-silent attempt may go
// before the gateway is told to commit the response and start heartbeats.
// Reasoning models (o-series, DeepSeek-R1 via some gateways) legitimately
// emit nothing for a minute or more; without any bytes on the wire the
// client, or a proxy in front of it (nginx 60s, Cloudflare 100s), gives up.
const streamPendingNotifyAfter = 20 * time.Second

// admitStreamNotify is admitStream with a pending callback: when no output
// has arrived after pendingAfter, onPending is invoked exactly once so the
// caller can commit headers and keep the client connection alive while the
// upstream is still thinking. A nil onPending disables the notification.
func admitStreamNotify(ctx context.Context, in <-chan core.StreamChunk, timeout time.Duration, provider, model string,
	onPending func(), pendingAfter time.Duration) (<-chan core.StreamChunk, error) {
	if timeout <= 0 {
		timeout = defaultStreamAdmissionTimeout
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var pendingC <-chan time.Time
	if onPending != nil && pendingAfter > 0 && pendingAfter < timeout {
		pendingTimer := time.NewTimer(pendingAfter)
		defer pendingTimer.Stop()
		pendingC = pendingTimer.C
	}

	prefix := make([]core.StreamChunk, 0, 4)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pendingC:
			pendingC = nil
			onPending()
		case <-timer.C:
			return nil, &core.ProviderError{
				Kind:     core.ErrTimeout,
				Scope:    core.FailureScopeRequest,
				Provider: provider,
				Model:    model,
				Message:  "stream admission timeout: no model output received for " + timeout.String(),
			}
		case chunk, ok := <-in:
			if !ok {
				return nil, &core.ProviderError{
					Kind:     core.ErrUpstream,
					Scope:    core.FailureScopeRequest,
					Provider: provider,
					Model:    model,
					Message:  "provider stream ended before producing model output",
				}
			}
			if chunk.Type == core.ChunkError {
				if chunk.Err != nil {
					return nil, chunk.Err
				}
				return nil, &core.ProviderError{
					Kind:     core.ErrUpstream,
					Scope:    core.FailureScopeRequest,
					Provider: provider,
					Model:    model,
					Message:  "provider stream failed before producing model output",
				}
			}
			prefix = append(prefix, chunk)
			if meaningfulAdmissionChunk(chunk) {
				return replayAdmittedStream(ctx, prefix, in), nil
			}
			if chunk.Type == core.ChunkFinish {
				return nil, &core.ProviderError{
					Kind:     core.ErrUpstream,
					Scope:    core.FailureScopeRequest,
					Provider: provider,
					Model:    model,
					Message:  fmt.Sprintf("provider stream finished without model output (%s)", chunk.FinishReason),
				}
			}
		}
	}
}

func meaningfulAdmissionChunk(chunk core.StreamChunk) bool {
	switch chunk.Type {
	case core.ChunkText, core.ChunkThinking:
		return chunk.Delta != ""
	case core.ChunkToolCall:
		return chunk.ToolCall != nil && chunk.ToolCall.ID != ""
	default:
		return false
	}
}

func replayAdmittedStream(ctx context.Context, prefix []core.StreamChunk, in <-chan core.StreamChunk) <-chan core.StreamChunk {
	out := make(chan core.StreamChunk, 16)
	go func() {
		defer close(out)
		forward := func(chunk core.StreamChunk) bool {
			select {
			case out <- chunk:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for _, chunk := range prefix {
			if !forward(chunk) {
				return
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case chunk, ok := <-in:
				if !ok || !forward(chunk) {
					return
				}
			}
		}
	}()
	return out
}
