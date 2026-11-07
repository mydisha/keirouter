package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReasoningOrigin_GroupsProvidersThatVerifyTheSameSignatures(t *testing.T) {
	require.Equal(t, "anthropic", ReasoningOrigin("anthropic", "claude-sonnet-4-5"))
	require.Equal(t, "anthropic", ReasoningOrigin("bedrock", "us.anthropic.claude-opus-4-1"))
	require.Equal(t, "anthropic", ReasoningOrigin("claude", "claude-opus-4-6"))
	require.Equal(t, "bedrock", ReasoningOrigin("bedrock", "amazon.nova-pro"))
	require.Equal(t, "openai", ReasoningOrigin("codex", "gpt-5-codex"))
	require.Equal(t, "openai", ReasoningOrigin("azure", "gpt-5"))
	// Anthropic- or Responses-shaped APIs from other vendors sign with their own keys.
	require.Equal(t, "glm", ReasoningOrigin("glm", "glm-4.6"))
	require.Equal(t, "grok-cli", ReasoningOrigin("grok-cli", "grok-4"))
}

func TestSignatureTag_RoundTrip(t *testing.T) {
	tagged := TagSignature("EqQBCkgIARABGAIi", "openai", "anthropic")
	require.Equal(t, "krsig1.openai.EqQBCkgIARABGAIi", tagged)
	sig, origin := UntagSignature(tagged, "anthropic")
	require.Equal(t, "EqQBCkgIARABGAIi", sig)
	require.Equal(t, "openai", origin)

	require.Equal(t, "native-sig", TagSignature("native-sig", "anthropic", "anthropic"))
	require.Equal(t, "no-origin", TagSignature("no-origin", "", "anthropic"))
	sig, origin = UntagSignature("plain", "anthropic")
	require.Equal(t, "plain", sig)
	require.Equal(t, "anthropic", origin)
}

func TestDropForeignReasoning_KeepsWhatTheTargetCanVerify(t *testing.T) {
	req := &ChatRequest{Messages: []Message{{Role: RoleAssistant, Content: []ContentPart{
		{Type: PartThinking, Text: "mine", Signature: "a", ReasoningOrigin: "openai"},
		{Type: PartThinking, Text: "theirs", Signature: "b", ReasoningOrigin: "anthropic"},
		{Type: PartRedactedThinking, Text: "blob", ReasoningOrigin: "anthropic"},
		{Type: PartThinking, Text: "unsigned"},
		{Type: PartText, Text: "answer"},
	}}}}
	DropForeignReasoning(req, "openai")
	require.Equal(t, []ContentPart{
		{Type: PartThinking, Text: "mine", Signature: "a", ReasoningOrigin: "openai"},
		{Type: PartThinking, Text: "unsigned"},
		{Type: PartText, Text: "answer"},
	}, req.Messages[0].Content)
}

func TestWithReasoningOrigin_LeavesTheSharedResponseAlone(t *testing.T) {
	shared := &ChatResponse{Message: Message{Content: []ContentPart{
		{Type: PartThinking, Text: "t", Signature: "s"}, {Type: PartText, Text: "x"},
	}}}
	out := WithReasoningOrigin(shared, "glm")
	require.Equal(t, "glm", out.Message.Content[0].ReasoningOrigin)
	require.Empty(t, shared.Message.Content[0].ReasoningOrigin)

	plain := &ChatResponse{Message: Message{Content: []ContentPart{{Type: PartText, Text: "x"}}}}
	require.Same(t, plain, WithReasoningOrigin(plain, "glm"))
}
