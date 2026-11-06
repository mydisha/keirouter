package pipeline

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/store"
)

type recordingConnector struct {
	plannerConnector
	mu   sync.Mutex
	seen []*core.ChatRequest
}

func (c *recordingConnector) record(req *core.ChatRequest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, req)
}

func (c *recordingConnector) Chat(_ context.Context, req *core.ChatRequest, _ core.Credentials) (*core.ChatResponse, error) {
	c.record(req)
	return &core.ChatResponse{Message: core.Message{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartText, Text: "ok"}}}, FinishReason: core.FinishStop}, nil
}

func (c *recordingConnector) Stream(_ context.Context, req *core.ChatRequest, _ core.Credentials, _ core.StreamConfig) (<-chan core.StreamChunk, error) {
	c.record(req)
	ch := make(chan core.StreamChunk, 2)
	ch <- core.StreamChunk{Type: core.ChunkText, Delta: "ok"}
	ch <- core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishStop}
	close(ch)
	return ch, nil
}

func mixedReasoningRequest() *core.ChatRequest {
	return &core.ChatRequest{
		Model: "gpt-5",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}},
			{Role: core.RoleAssistant, Content: []core.ContentPart{
				{Type: core.PartThinking, Text: "claude", Signature: "EqQB", ReasoningOrigin: "anthropic"},
				{Type: core.PartThinking, Text: "gpt", Signature: "gAAAAB", ReasoningOrigin: "openai"},
				{Type: core.PartText, Text: "hello"},
			}},
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "again"}}},
		},
		Metadata: core.RequestMetadata{TenantID: store.DefaultTenantID},
	}
}

func TestSignature_ForeignThinkingNeverReachesTheTarget(t *testing.T) {
	conn := &recordingConnector{}
	p := New(Deps{Dispatcher: newPlannerDispatcherWithConnector(t, conn, "acc-1")})
	opts := Options{Targets: []dispatch.Target{{Provider: "openai", Model: "gpt-5"}}}

	req := mixedReasoningRequest()
	_, err := p.Chat(context.Background(), req, opts)
	require.NoError(t, err)

	res, err := p.Stream(context.Background(), mixedReasoningRequest(), opts)
	require.NoError(t, err)
	for range res.Chunks {
	}

	require.Len(t, conn.seen, 2)
	for _, sent := range conn.seen {
		assistant := sent.Messages[1].Content
		require.Len(t, assistant, 2)
		require.Equal(t, "gpt", assistant[0].Text)
		require.Equal(t, core.PartText, assistant[1].Type)
	}
	require.Len(t, req.Messages[1].Content, 3, "the client's request is not modified")
}
