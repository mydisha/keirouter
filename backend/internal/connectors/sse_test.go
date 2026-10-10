package connectors

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/stretchr/testify/require"
)

// TestStreamProducerExitsWhenConsumerLeaves guards against the old leak: a
// producer blocked on an unguarded terminal send after its consumer left,
// pinning the goroutine, the scanner buffer and the upstream connection.
func TestStreamProducerExitsWhenConsumerLeaves(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		// More chunks than the channel buffer holds, then an abrupt close.
		for i := 0; i < 64; i++ {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"c%d\"}}]}\n\n", i)
		}
		flusher.Flush()
		// Hijack and close so the client sees an unexpected EOF.
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, _ := hj.Hijack()
			conn.Close()
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c := NewOpenAICompatible("openai", srv.URL)
	out, err := c.Stream(ctx, textReq("gpt-4o", true), core.Credentials{APIKey: "k"}, core.StreamConfig{})
	require.NoError(t, err)

	// Read one chunk, then abandon the stream.
	<-out
	cancel()

	done := make(chan struct{})
	go func() {
		for range out { // drains until the producer closes the channel
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream producer did not exit after the consumer cancelled")
	}
}

func TestStreamKeepAliveCommentBecomesPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": keep-alive\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewOpenAICompatible("openai", srv.URL)
	out, err := c.Stream(context.Background(), textReq("gpt-4o", true), core.Credentials{APIKey: "k"}, core.StreamConfig{})
	require.NoError(t, err)
	var types []core.ChunkType
	for ch := range out {
		types = append(types, ch.Type)
	}
	require.Equal(t, []core.ChunkType{core.ChunkPing, core.ChunkText, core.ChunkFinish}, types)
}

func TestStreamInBandErrorFrameSurfacesAsChunkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"error\":{\"message\":\"Rate limit reached for requests\",\"type\":\"requests\",\"code\":\"429\"}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewOpenAICompatible("openai", srv.URL)
	out, err := c.Stream(context.Background(), textReq("gpt-4o", true), core.Credentials{APIKey: "k"}, core.StreamConfig{})
	require.NoError(t, err)
	var chunks []core.StreamChunk
	for ch := range out {
		chunks = append(chunks, ch)
	}
	require.Len(t, chunks, 1)
	require.Equal(t, core.ChunkError, chunks[0].Type)
	pe := core.AsProviderError(chunks[0].Err)
	require.Equal(t, core.ErrRateLimit, pe.Kind)
	require.Equal(t, "openai", pe.Provider)
	require.Equal(t, "gpt-4o", pe.Model)
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestScanErrorClassification(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := newSSEPump(ctx, "openai", "gpt-4o", make(chan core.StreamChunk, 1), core.StreamConfig{})

	require.Nil(t, p.scanError(context.Canceled))

	pe := p.scanError(bufio.ErrTooLong)
	require.Equal(t, core.ErrUpstream, pe.Kind)
	require.Equal(t, core.FailureScopeRequest, pe.Scope)

	pe = p.scanError(context.DeadlineExceeded)
	require.Equal(t, core.ErrTimeout, pe.Kind)
	require.Equal(t, core.FailureScopeRequest, pe.Scope)

	var ne net.Error = timeoutErr{}
	pe = p.scanError(ne)
	require.Equal(t, core.ErrTimeout, pe.Kind)
	require.Equal(t, core.FailureScopeProvider, pe.Scope)

	pe = p.scanError(errors.New("read tcp: connection reset by peer"))
	require.Equal(t, core.ErrUpstream, pe.Kind)
	require.Equal(t, core.FailureScopeNetwork, pe.Scope)
}
