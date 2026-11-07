package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// floodConnector streams far more text than any channel buffer holds, then
// waits for its context like a live upstream.
type floodConnector struct{ plannerConnector }

func (floodConnector) Stream(ctx context.Context, _ *core.ChatRequest, _ core.Credentials, _ core.StreamConfig) (<-chan core.StreamChunk, error) {
	out := make(chan core.StreamChunk)
	go func() {
		defer close(out)
		for range 10_000 {
			select {
			case out <- core.StreamChunk{Type: core.ChunkText, Delta: "x"}:
			case <-ctx.Done():
				return
			}
		}
		<-ctx.Done()
	}()
	return out, nil
}

func TestStream_ClientThatStopsReadingIsNotAProviderStall(t *testing.T) {
	d := newPlannerDispatcherWithConnector(t, floodConnector{}, "acc-1")
	p := New(Deps{Dispatcher: d, StreamStallTimeout: 50 * time.Millisecond})
	req := &core.ChatRequest{
		Model:    "gpt-4o",
		Stream:   true,
		Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hi"}}}},
		Metadata: core.RequestMetadata{TenantID: store.DefaultTenantID},
	}
	res, err := p.Stream(context.Background(), req, Options{Targets: []dispatch.Target{{Provider: "openai", Model: "gpt-4o"}}})
	require.NoError(t, err)

	<-res.Chunks
	time.Sleep(300 * time.Millisecond) // the client stops reading

	var last core.StreamChunk
	for c := range res.Chunks {
		last = c
	}
	require.Equal(t, core.ChunkError, last.Type)
	pe := core.AsProviderError(last.Err)
	require.Contains(t, pe.Message, "client stopped reading")
	require.Equal(t, core.FailureScopeRequest, pe.Scope)
}
