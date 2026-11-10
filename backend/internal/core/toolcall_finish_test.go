package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func toolCallResponse(finish FinishReason) *ChatResponse {
	return &ChatResponse{FinishReason: finish, Message: Message{Role: RoleAssistant, Content: []ContentPart{
		{Type: PartText, Text: "calling"},
		{Type: PartToolCall, ToolCall: &ToolCall{ID: "c1", Name: "get_weather"}},
	}}}
}

func TestWithToolCallFinish_StopWithToolCallBecomesToolCalls(t *testing.T) {
	in := toolCallResponse(FinishStop)
	out := WithToolCallFinish(in)
	require.Equal(t, FinishToolCalls, out.FinishReason)
	require.Equal(t, FinishStop, in.FinishReason, "cached responses are shared and must not be mutated")
}

func TestWithToolCallFinish_EmptyReasonWithToolCallBecomesToolCalls(t *testing.T) {
	require.Equal(t, FinishToolCalls, WithToolCallFinish(toolCallResponse("")).FinishReason)
}

func TestWithToolCallFinish_LeavesOtherReasonsAlone(t *testing.T) {
	for _, r := range []FinishReason{FinishLength, FinishFilter, FinishToolCalls} {
		in := toolCallResponse(r)
		require.Same(t, in, WithToolCallFinish(in), string(r))
	}
}

func TestWithToolCallFinish_NoToolCallUnchanged(t *testing.T) {
	in := &ChatResponse{FinishReason: FinishStop, Message: Message{Content: []ContentPart{{Type: PartText, Text: "hi"}}}}
	require.Same(t, in, WithToolCallFinish(in))
	require.Nil(t, WithToolCallFinish(nil))
}
