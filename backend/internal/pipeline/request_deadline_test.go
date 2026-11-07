package pipeline

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// hangingConnector blocks every Chat call until its context ends.
type hangingConnector struct {
	plannerConnector
	calls atomic.Int32
}

func (c *hangingConnector) Chat(ctx context.Context, _ *core.ChatRequest, _ core.Credentials) (*core.ChatResponse, error) {
	c.calls.Add(1)
	<-ctx.Done()
	return nil, &core.ProviderError{Kind: core.ErrTimeout, Message: "upstream hung", Cause: ctx.Err()}
}

func TestChatRequestTimeoutBoundsWholeFallbackChain(t *testing.T) {
	conn := &hangingConnector{}
	d := newPlannerDispatcherWithConnector(t, conn, "acc-1", "acc-2", "acc-3")
	p := New(Deps{Dispatcher: d, RequestTimeout: 80 * time.Millisecond})
	req := &core.ChatRequest{
		Model: "gpt-4o",
		Messages: []core.Message{{
			Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hello"}},
		}},
		Metadata: core.RequestMetadata{TenantID: store.DefaultTenantID},
	}

	started := time.Now()
	_, err := p.Chat(context.Background(), req, Options{
		Targets: []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}},
	})
	elapsed := time.Since(started)

	require.Error(t, err)
	pe := core.AsProviderError(err)
	require.NotNil(t, pe)
	require.Equal(t, core.ErrTimeout, pe.Kind)
	require.Equal(t, int32(1), conn.calls.Load(), "no fallback may start after the request deadline")
	require.Less(t, elapsed, time.Second, "three accounts must not each get a full request timeout")
}

// streamOnlyConnector refuses unary calls the way stream-only providers do,
// then streams text and closes the channel quietly when its context ends.
type streamOnlyConnector struct {
	plannerConnector
}

func (c *streamOnlyConnector) Chat(context.Context, *core.ChatRequest, core.Credentials) (*core.ChatResponse, error) {
	return nil, &core.ProviderError{Kind: core.ErrBadRequest, StatusCode: 400, Message: "Stream must be set to true"}
}

func (c *streamOnlyConnector) Stream(ctx context.Context, _ *core.ChatRequest, _ core.Credentials, _ core.StreamConfig) (<-chan core.StreamChunk, error) {
	out := make(chan core.StreamChunk)
	go func() {
		defer close(out)
		select {
		case out <- core.StreamChunk{Type: core.ChunkText, Delta: "partial"}:
		case <-ctx.Done():
			return
		}
		<-ctx.Done()
	}()
	return out, nil
}

func TestChat_DrainEndedByTheDeadlineIsATimeout(t *testing.T) {
	conn := &streamOnlyConnector{}
	d := newPlannerDispatcherWithConnector(t, conn, "acc-1")
	p := New(Deps{Dispatcher: d, RequestTimeout: 80 * time.Millisecond})
	req := &core.ChatRequest{
		Model:    "gpt-4o",
		Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hello"}}}},
		Metadata: core.RequestMetadata{TenantID: store.DefaultTenantID},
	}
	_, err := p.Chat(context.Background(), req, Options{
		Targets: []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}},
	})
	require.Error(t, err, "a truncated answer must not be returned as a success")
	require.Equal(t, core.ErrTimeout, core.AsProviderError(err).Kind)
}
