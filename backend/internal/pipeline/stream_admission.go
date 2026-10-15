package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
)

const defaultStreamAdmissionTimeout = 60 * time.Second

// trailingUsageGrace bounds how long admission waits after an outputless
// finish frame for the usage frame that follows it.
const trailingUsageGrace = 2 * time.Second

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
	if timeout <= 0 {
		timeout = defaultStreamAdmissionTimeout
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	prefix := make([]core.StreamChunk, 0, 4)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
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
				// An outputless stream the upstream still charged for (a
				// reasoning model that spent max_tokens thinking) is a real
				// completion: admit it so it is billed like the non-stream
				// path, instead of a free fallback the operator pays for.
				// The usage frame carrying the cost trails the finish frame.
				prefix = append(prefix, collectTrailing(ctx, in, trailingUsageGrace)...)
				if upstreamCharged(prefix) {
					return replayAdmittedStream(ctx, prefix, in), nil
				}
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

// collectTrailing reads the frames that follow a finish frame until the
// stream closes, errors, or grace elapses.
func collectTrailing(ctx context.Context, in <-chan core.StreamChunk, grace time.Duration) []core.StreamChunk {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	var out []core.StreamChunk
	for {
		select {
		case <-ctx.Done():
			return out
		case <-timer.C:
			return out
		case chunk, ok := <-in:
			if !ok || chunk.Type == core.ChunkError {
				return out
			}
			out = append(out, chunk)
		}
	}
}

// upstreamCharged reports whether any usage frame carries a positive cost the
// upstream reported charging for this attempt.
func upstreamCharged(chunks []core.StreamChunk) bool {
	for _, c := range chunks {
		if c.Type == core.ChunkUsage && c.Usage != nil && c.Usage.UpstreamCostReported && c.Usage.UpstreamCostMicro > 0 {
			return true
		}
	}
	return false
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
