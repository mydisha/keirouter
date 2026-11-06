package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecoverStream_PanicBecomesErrorChunk(t *testing.T) {
	out := make(chan StreamChunk, 1)
	go func() {
		defer close(out)
		defer RecoverStream(out)
		panic("bad frame")
	}()
	chunk, ok := <-out
	require.True(t, ok)
	require.Equal(t, ChunkError, chunk.Type)
	require.ErrorContains(t, chunk.Err, "bad frame")
	_, ok = <-out
	require.False(t, ok, "the channel is still closed")
}
