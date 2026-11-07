package transform

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/core"
)

func TestCollectStream_ToolCallContinuationsJoinTheirCall(t *testing.T) {
	for _, shape := range decoderShapes {
		chunks := decoderChunks(t, shape, fidelityArgs)
		stream := make(chan core.StreamChunk, len(chunks))
		for _, c := range chunks {
			stream <- c
		}
		close(stream)

		resp, err := CollectStream(stream, "m")
		require.NoError(t, err)
		require.Equal(t, core.FinishToolCalls, resp.FinishReason, shape)
		require.Len(t, resp.Message.Content, 1, shape)
		tc := resp.Message.Content[0].ToolCall
		require.Equal(t, "save", tc.Name, shape)
		require.JSONEq(t, fidelityArgs, string(tc.Arguments), shape)
	}
}
