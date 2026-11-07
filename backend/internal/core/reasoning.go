package core

import "strings"

// ReasoningOrigin names who can verify the thinking signatures a provider
// returns. Claude signatures verify on every first-party Claude host.
func ReasoningOrigin(provider, model string) string {
	switch provider {
	case "anthropic", "claude", "bedrock", "vertex", "vertex-partner":
		if strings.Contains(strings.ToLower(model), "claude") {
			return "anthropic"
		}
	case "openai", "azure", "codex":
		return "openai"
	}
	return provider
}

// NativeReasoningOrigin is the origin assumed for an untagged signature from a
// client of dialect d, or "" when the dialect carries no signatures.
func NativeReasoningOrigin(d Dialect) string {
	switch d {
	case DialectAnthropic:
		return "anthropic"
	case DialectOpenAIResponses:
		return "openai"
	}
	return ""
}

const signatureTag = "krsig1."

// TagSignature marks a signature for a client whose native origin differs, so
// the next turn knows which provider can verify it. Provider ids hold no dots.
func TagSignature(sig, origin, native string) string {
	if sig == "" || origin == "" || origin == native {
		return sig
	}
	return signatureTag + origin + "." + sig
}

// UntagSignature returns a client-sent signature and its origin.
func UntagSignature(sig, native string) (string, string) {
	if rest, ok := strings.CutPrefix(sig, signatureTag); ok {
		if origin, s, ok := strings.Cut(rest, "."); ok {
			return s, origin
		}
	}
	return sig, native
}

// DropForeignReasoning removes thinking signed by another origin from the
// request's messages in place; the target would reject it as invalid.
func DropForeignReasoning(req *ChatRequest, target string) {
	for i := range req.Messages {
		content := req.Messages[i].Content
		kept := content[:0]
		for _, p := range content {
			if (p.Type == PartThinking || p.Type == PartRedactedThinking) && p.ReasoningOrigin != "" && p.ReasoningOrigin != target {
				continue
			}
			kept = append(kept, p)
		}
		req.Messages[i].Content = kept
	}
}

// WithReasoningOrigin returns resp with its thinking marked as produced by
// origin. It copies rather than mutates: cached responses are shared.
func WithReasoningOrigin(resp *ChatResponse, origin string) *ChatResponse {
	if resp == nil {
		return nil
	}
	var out *ChatResponse
	for i, p := range resp.Message.Content {
		if p.Type != PartThinking && p.Type != PartRedactedThinking {
			continue
		}
		if out == nil {
			clone := *resp
			clone.Message.Content = append([]ContentPart(nil), resp.Message.Content...)
			out = &clone
		}
		out.Message.Content[i].ReasoningOrigin = origin
	}
	if out == nil {
		return resp
	}
	return out
}
