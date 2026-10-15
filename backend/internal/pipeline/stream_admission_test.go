package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/store"
	"github.com/stretchr/testify/require"
)

type admissionFallbackConnector struct{}

func (admissionFallbackConnector) ID() string            { return "openai" }
func (admissionFallbackConnector) Dialect() core.Dialect { return core.DialectAnthropic }
func (admissionFallbackConnector) Chat(context.Context, *core.ChatRequest, core.Credentials) (*core.ChatResponse, error) {
	return nil, nil
}
func (admissionFallbackConnector) Stream(_ context.Context, _ *core.ChatRequest, creds core.Credentials, _ core.StreamConfig) (<-chan core.StreamChunk, error) {
	if creds.AccountID == "acc-1" {
		return streamOf(core.StreamChunk{Type: core.ChunkError, Err: &core.ProviderError{
			Kind: core.ErrUpstream, Scope: core.FailureScopeRequest, Message: "capacity in stream",
		}}), nil
	}
	return streamOf(
		core.StreamChunk{Type: core.ChunkText, Delta: "from fallback"},
		core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishStop},
	), nil
}

func streamOf(chunks ...core.StreamChunk) <-chan core.StreamChunk {
	out := make(chan core.StreamChunk, len(chunks))
	for _, chunk := range chunks {
		out <- chunk
	}
	close(out)
	return out
}

func TestAdmitStreamReplaysPrefixWithoutDroppingChunks(t *testing.T) {
	usage := core.Usage{PromptTokens: 3, TotalTokens: 3}
	in := streamOf(
		core.StreamChunk{Type: core.ChunkPing},
		core.StreamChunk{Type: core.ChunkUsage, Usage: &usage},
		core.StreamChunk{Type: core.ChunkText, Delta: "hello"},
		core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishStop},
	)

	out, err := admitStream(context.Background(), in, time.Second, "openai", "model")
	require.NoError(t, err)
	var got []core.StreamChunk
	for chunk := range out {
		got = append(got, chunk)
	}
	require.Len(t, got, 4)
	require.Equal(t, core.ChunkPing, got[0].Type)
	require.Equal(t, "hello", got[2].Delta)
	require.Equal(t, core.ChunkFinish, got[3].Type)
}

func TestRequiresParsedStreamAdmission(t *testing.T) {
	require.True(t, requiresParsedStreamAdmission("codex"))
	require.False(t, requiresParsedStreamAdmission("openai"))
}

func TestAdmitStreamReturnsTypedFirstFrameError(t *testing.T) {
	want := &core.ProviderError{Kind: core.ErrQuotaExhausted, Scope: core.FailureScopeAccount, Message: "quota exhausted"}
	out, err := admitStream(context.Background(), streamOf(core.StreamChunk{Type: core.ChunkError, Err: want}), time.Second, "qoder", "model")
	require.Nil(t, out)
	var got *core.ProviderError
	require.ErrorAs(t, err, &got)
	require.Same(t, want, got)
}

func TestAdmitStreamRejectsEmptySuccessfulStream(t *testing.T) {
	out, err := admitStream(context.Background(), streamOf(), time.Second, "openai", "model")
	require.Nil(t, out)
	var pe *core.ProviderError
	require.ErrorAs(t, err, &pe)
	require.Equal(t, core.ErrUpstream, pe.Kind)
	require.Equal(t, core.FailureScopeRequest, pe.Scope)
}

func TestAdmitStreamTimesOutWithoutMeaningfulOutput(t *testing.T) {
	in := make(chan core.StreamChunk)
	out, err := admitStream(context.Background(), in, 5*time.Millisecond, "openai", "model")
	require.Nil(t, out)
	var pe *core.ProviderError
	require.True(t, errors.As(err, &pe))
	require.Equal(t, core.ErrTimeout, pe.Kind)
	require.Equal(t, core.FailureScopeRequest, pe.Scope)
}

func TestPipelineFallsBackWhenFirstStreamErrorsBeforeOutput(t *testing.T) {
	d := newPlannerDispatcherWithConnector(t, admissionFallbackConnector{}, "acc-1", "acc-2")
	p := New(Deps{Dispatcher: d})
	req := &core.ChatRequest{
		Model: "gpt-4o",
		Messages: []core.Message{{
			Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hello"}},
		}},
		Metadata: core.RequestMetadata{
			TenantID:      store.DefaultTenantID,
			SourceDialect: core.DialectOpenAI,
		},
	}

	result, err := p.Stream(context.Background(), req, Options{
		Targets: []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}},
	})
	require.NoError(t, err)
	require.Equal(t, "acc-2", result.AccountID)

	var text string
	for chunk := range result.Chunks {
		if chunk.Type == core.ChunkText {
			text += chunk.Delta
		}
	}
	require.Equal(t, "from fallback", text)
}

func TestAdmitStreamAdmitsOutputlessStreamTheUpstreamCharged(t *testing.T) {
	// Surplus sends the usage frame (with buyer_cost_micro) after the finish
	// frame. A reasoning model that spent max_tokens thinking has no visible
	// output, but the upstream still charged: the stream must be admitted (and
	// so billed) like the non-stream path, not rejected into a free fallback.
	charged := core.Usage{PromptTokens: 25, CompletionTokens: 600, UpstreamCostMicro: 9, UpstreamCostReported: true}
	in := streamOf(
		core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishLength},
		core.StreamChunk{Type: core.ChunkUsage, Usage: &charged},
	)
	out, err := admitStream(context.Background(), in, time.Second, "custom-openai-surplus", "gpt-5.6-luna")
	require.NoError(t, err)
	var got []core.StreamChunk
	for chunk := range out {
		got = append(got, chunk)
	}
	require.Len(t, got, 2)
	require.Equal(t, core.ChunkFinish, got[0].Type)
	require.Equal(t, core.ChunkUsage, got[1].Type)
	require.Equal(t, 9.0, got[1].Usage.UpstreamCostMicro)
}

func TestAdmitStreamAdmitsChargedUsageSeenBeforeFinish(t *testing.T) {
	charged := core.Usage{CompletionTokens: 10, UpstreamCostMicro: 1, UpstreamCostReported: true}
	in := streamOf(
		core.StreamChunk{Type: core.ChunkUsage, Usage: &charged},
		core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishLength},
	)
	out, err := admitStream(context.Background(), in, time.Second, "custom-openai-surplus", "m")
	require.NoError(t, err)
	n := 0
	for range out {
		n++
	}
	require.Equal(t, 2, n)
}

func TestAdmitStreamRejectsOutputlessStreamWithoutCharge(t *testing.T) {
	for name, usage := range map[string]*core.Usage{
		"no usage":        nil,
		"tokens, no cost": {CompletionTokens: 50},
		"zero cost":       {CompletionTokens: 1, UpstreamCostMicro: 0, UpstreamCostReported: true},
	} {
		chunks := []core.StreamChunk{{Type: core.ChunkFinish, FinishReason: core.FinishLength}}
		if usage != nil {
			chunks = append(chunks, core.StreamChunk{Type: core.ChunkUsage, Usage: usage})
		}
		out, err := admitStream(context.Background(), streamOf(chunks...), time.Second, "openai", "m")
		require.Nil(t, out, name)
		var pe *core.ProviderError
		require.ErrorAs(t, err, &pe, name)
		require.Equal(t, core.ErrUpstream, pe.Kind, name)
	}
}

func TestAdmitStreamTrailingUsageWaitIsBounded(t *testing.T) {
	// An upstream that sends finish and then hangs must not hold the request
	// for the whole admission timeout before falling back.
	in := make(chan core.StreamChunk, 1)
	in <- core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishStop}
	started := time.Now()
	out, err := admitStream(context.Background(), in, time.Minute, "openai", "m")
	require.Nil(t, out)
	require.Error(t, err)
	require.Less(t, time.Since(started), trailingUsageGrace+time.Second)
}
