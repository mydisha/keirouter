package pipeline

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/meter"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// hangupConnector simulates a client that disconnects while the upstream is
// still generating: it cancels the client context mid-call, then completes.
type hangupConnector struct {
	plannerConnector
	hangup      context.CancelFunc
	upstreamErr *error
	calls       *int
}

func (c hangupConnector) Chat(ctx context.Context, _ *core.ChatRequest, _ core.Credentials) (*core.ChatResponse, error) {
	*c.calls++
	c.hangup()
	*c.upstreamErr = ctx.Err()
	return &core.ChatResponse{
		Message: core.Message{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartText, Text: "done"}}},
		Usage:   core.Usage{PromptTokens: 19400, CompletionTokens: 3500, TotalTokens: 22900},
	}, nil
}

func TestChatClientDisconnectStillBillsCompletedUpstream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var upstreamErr error
	calls := 0
	conn := hangupConnector{hangup: cancel, upstreamErr: &upstreamErr, calls: &calls}

	m := meter.New(newPipelineTestDB(t).Usage(), nil, nil)
	var recs []store.UsageRecord
	m.SetRecordObserver(func(r store.UsageRecord) { recs = append(recs, r) })
	p := New(Deps{Dispatcher: newPlannerDispatcherWithConnector(t, conn, "acc-1"), Meter: m})

	_, err := p.Chat(ctx, guardrailStreamRequest("hello"), Options{
		Targets: []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}},
	})

	require.Equal(t, core.ErrClientCanceled, core.AsProviderError(err).Kind)
	require.NoError(t, upstreamErr, "client disconnect must not cancel the upstream call")
	require.Equal(t, 1, calls)
	require.Len(t, recs, 1)
	require.Equal(t, "cancelled", recs[0].Status)
	require.Equal(t, 19400, recs[0].PromptTokens)
	require.Equal(t, 3500, recs[0].CompletionTokens)
}

func TestChatSkipsUpstreamWhenClientAlreadyGone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var upstreamErr error
	calls := 0
	conn := hangupConnector{hangup: func() {}, upstreamErr: &upstreamErr, calls: &calls}
	m := meter.New(newPipelineTestDB(t).Usage(), nil, nil)
	p := New(Deps{Dispatcher: newPlannerDispatcherWithConnector(t, conn, "acc-1"), Meter: m})
	cancel()

	_, err := p.Chat(ctx, guardrailStreamRequest("hello"), Options{
		Targets: []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}},
	})

	require.Equal(t, core.ErrClientCanceled, core.AsProviderError(err).Kind)
	require.Zero(t, calls, "no upstream attempt should start after the client left")
}
