package pipeline

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/inflight"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// gatedConnector emits one text chunk, then holds the stream open until gate
// closes (finishing normally) or the call context is cancelled.
type gatedConnector struct {
	plannerConnector
	gate chan struct{}
}

func (c gatedConnector) Stream(ctx context.Context, _ *core.ChatRequest, _ core.Credentials, _ core.StreamConfig) (<-chan core.StreamChunk, error) {
	out := make(chan core.StreamChunk, 1)
	go func() {
		defer close(out)
		out <- core.StreamChunk{Type: core.ChunkText, Delta: "hi"}
		select {
		case <-c.gate:
			out <- core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishStop}
		case <-ctx.Done():
		}
	}()
	return out, nil
}

func inflightStreamRequest() *core.ChatRequest {
	return &core.ChatRequest{
		Model: "gpt-4o",
		Messages: []core.Message{{
			Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hello"}},
		}},
		Metadata: core.RequestMetadata{TenantID: store.DefaultTenantID, SourceDialect: core.DialectOpenAI},
	}
}

var inflightTargets = Options{Targets: []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}}}

func TestStream_InflightReleased(t *testing.T) {
	t.Run("drained", func(t *testing.T) {
		conn := gatedConnector{gate: make(chan struct{})}
		tr := inflight.New()
		p := New(Deps{Dispatcher: newPlannerDispatcherWithConnector(t, conn, "acc-1"), Inflight: tr})

		result, err := p.Stream(context.Background(), inflightStreamRequest(), inflightTargets)
		require.NoError(t, err)
		first := <-result.Chunks
		require.Equal(t, core.ChunkText, first.Type)
		require.Equal(t, map[string]int64{"openai": 1}, tr.Snapshot())

		close(conn.gate)
		for range result.Chunks {
		}
		require.Empty(t, tr.Snapshot())
	})

	t.Run("client cancel", func(t *testing.T) {
		conn := gatedConnector{gate: make(chan struct{})}
		tr := inflight.New()
		p := New(Deps{Dispatcher: newPlannerDispatcherWithConnector(t, conn, "acc-1"), Inflight: tr})

		ctx, cancel := context.WithCancel(context.Background())
		result, err := p.Stream(ctx, inflightStreamRequest(), inflightTargets)
		require.NoError(t, err)
		<-result.Chunks
		require.Equal(t, map[string]int64{"openai": 1}, tr.Snapshot())

		cancel()
		for range result.Chunks {
		}
		require.Empty(t, tr.Snapshot())
	})

	t.Run("fallback releases failed attempt", func(t *testing.T) {
		tr := inflight.New()
		d := newPlannerDispatcherWithConnector(t, admissionFallbackConnector{}, "acc-1", "acc-2")
		p := New(Deps{Dispatcher: d, Inflight: tr})

		result, err := p.Stream(context.Background(), inflightStreamRequest(), inflightTargets)
		require.NoError(t, err)
		require.Equal(t, "acc-2", result.AccountID)
		require.Equal(t, map[string]int64{"openai": 1}, tr.Snapshot(), "only the live fallback attempt counts")
		for range result.Chunks {
		}
		require.Empty(t, tr.Snapshot())
	})

	t.Run("zero-copy direct body", func(t *testing.T) {
		tr := inflight.New()
		d := newPlannerDispatcherWithConnector(t, rawForbiddenConnector{}, "acc-1")
		p := New(Deps{Dispatcher: d, Inflight: tr})

		result, err := p.Stream(context.Background(), guardrailStreamRequest("hello"), inflightTargets)
		require.NoError(t, err)
		require.NotNil(t, result.DirectBody)
		require.Equal(t, map[string]int64{"openai": 1}, tr.Snapshot())
		_, _ = io.Copy(io.Discard, result.DirectBody)
		_ = result.DirectBody.Close()
		result.DirectCompleteFunc(nil)
		require.Empty(t, tr.Snapshot())
	})
}

// observingChatConnector records the tracker snapshot while Chat runs.
type observingChatConnector struct {
	plannerConnector
	tr   *inflight.Tracker
	seen *map[string]int64
}

func (c observingChatConnector) Chat(context.Context, *core.ChatRequest, core.Credentials) (*core.ChatResponse, error) {
	*c.seen = c.tr.Snapshot()
	return &core.ChatResponse{
		Model:        "gpt-4o",
		Message:      core.Message{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartText, Text: "ok"}}},
		FinishReason: core.FinishStop,
	}, nil
}

func TestChat_InflightReleased(t *testing.T) {
	tr := inflight.New()
	var seen map[string]int64
	conn := observingChatConnector{tr: tr, seen: &seen}
	p := New(Deps{Dispatcher: newPlannerDispatcherWithConnector(t, conn, "acc-1"), Inflight: tr})

	_, err := p.Chat(context.Background(), inflightStreamRequest(), inflightTargets)
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"openai": 1}, seen)
	require.Empty(t, tr.Snapshot())
}
