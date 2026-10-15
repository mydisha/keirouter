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
