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

// truncatedConnector streams text and then closes the channel without a
// finish event, like an upstream that hung up mid-generation.
type truncatedConnector struct{ plannerConnector }

func (truncatedConnector) Stream(context.Context, *core.ChatRequest, core.Credentials, core.StreamConfig) (<-chan core.StreamChunk, error) {
	return streamOf(
		core.StreamChunk{Type: core.ChunkText, Delta: "partial "},
		core.StreamChunk{Type: core.ChunkText, Delta: "answer"},
	), nil
}

func TestPipelineSynthesizesFinishForTruncatedStream(t *testing.T) {
	d := newPlannerDispatcherWithConnector(t, truncatedConnector{}, "acc-1")
	p := New(Deps{Dispatcher: d})
	req := &core.ChatRequest{
		Model:        "gpt-4o",
		IncludeUsage: true,
		Messages: []core.Message{{
			Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hello"}},
		}},
		Metadata: core.RequestMetadata{TenantID: store.DefaultTenantID, SourceDialect: core.DialectOpenAI},
	}
	result, err := p.Stream(context.Background(), req, Options{
		Targets: []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}},
	})
	require.NoError(t, err)

	var types []core.ChunkType
	var text string
	for chunk := range result.Chunks {
		types = append(types, chunk.Type)
		if chunk.Type == core.ChunkText {
			text += chunk.Delta
		}
	}
	require.Equal(t, "partial answer", text)
	require.Equal(t, []core.ChunkType{core.ChunkText, core.ChunkText, core.ChunkFinish, core.ChunkUsage}, types,
		"a finish event is synthesized before the usage event so clients see a well-formed end")
}

// pingingConnector emits keep-alives then text; pings must reset the stall
// timer without reaching the client.
type pingingConnector struct{ plannerConnector }

func (pingingConnector) Stream(context.Context, *core.ChatRequest, core.Credentials, core.StreamConfig) (<-chan core.StreamChunk, error) {
	return streamOf(
		core.StreamChunk{Type: core.ChunkPing},
		core.StreamChunk{Type: core.ChunkText, Delta: "hi"},
		core.StreamChunk{Type: core.ChunkPing},
		core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishStop},
	), nil
}

func TestPipelineDropsPingsFromClientStream(t *testing.T) {
	d := newPlannerDispatcherWithConnector(t, pingingConnector{}, "acc-1")
	p := New(Deps{Dispatcher: d})
	req := &core.ChatRequest{
		Model: "gpt-4o",
		Messages: []core.Message{{
			Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hello"}},
		}},
		Metadata: core.RequestMetadata{TenantID: store.DefaultTenantID, SourceDialect: core.DialectOpenAI},
	}
	result, err := p.Stream(context.Background(), req, Options{
		Targets: []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}},
	})
	require.NoError(t, err)
	var types []core.ChunkType
	for chunk := range result.Chunks {
		types = append(types, chunk.Type)
	}
	require.Equal(t, []core.ChunkType{core.ChunkText, core.ChunkFinish}, types)
}
