package transform

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"

	json "github.com/mydisha/keirouter/backend/internal/fastjson"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/errclass"
)

// Gemini streams partial generateContent responses: each SSE data line is a
// full GenerateContentResponse fragment carrying incremental candidate parts
// and (on the final line) usageMetadata + finishReason. ParseStreamLine maps
// each fragment to canonical chunks; RenderStreamChunk produces the same wire
// shape for a client that speaks Gemini.

// gemStreamChunk is one SSE "data:" payload from streamGenerateContent.
type gemStreamChunk struct {
	Candidates []struct {
		Content           gemContent      `json:"content"`
		FinishReason      string          `json:"finishReason"`
		GroundingMetadata json.RawMessage `json:"groundingMetadata"`
	} `json:"candidates"`
	UsageMetadata *gemUsageMetadata `json:"usageMetadata"`
}

// ParseStreamLine converts one Gemini SSE data payload into canonical chunks.
func (GeminiCodec) ParseStreamLine(line []byte, _ string) ([]core.StreamChunk, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, nil
	}

	// {"error":{"code":429,"status":"RESOURCE_EXHAUSTED",...}} may arrive
	// as a stream frame; classify it instead of treating it as empty output.
	if pe, ok := errclass.StreamErrorFrame(line); ok {
		return []core.StreamChunk{{Type: core.ChunkError, Err: pe}}, nil
	}

	var raw gemStreamChunk
	if err := json.UnmarshalNoCopy(line, &raw); err != nil {
		return nil, fmt.Errorf("gemini: parse stream chunk: %w", err)
	}

	var chunks []core.StreamChunk
	if len(raw.Candidates) > 0 {
		cand := raw.Candidates[0]
		for partIdx, p := range cand.Content.Parts {
			switch {
			case p.FunctionCall != nil:
				chunks = append(chunks, core.StreamChunk{
					Type:  core.ChunkToolCall,
					Index: partIdx,
					ToolCall: &core.ToolCall{
						ID:        geminiEncodeCallID(p.FunctionCall, p.ThoughtSignature),
						Name:      p.FunctionCall.Name,
						Arguments: p.FunctionCall.Args,
					},
				})
			case p.Text != "":
				if p.Thought {
					chunks = append(chunks, core.StreamChunk{Type: core.ChunkThinking, Delta: p.Text})
				} else {
					chunks = append(chunks, core.StreamChunk{Type: core.ChunkText, Delta: p.Text})
				}
			}
		}
		if cand.FinishReason != "" {
			chunks = append(chunks, core.StreamChunk{
				Type:         core.ChunkFinish,
				FinishReason: mapGemCandidateFinish(cand.Content, cand.FinishReason),
			})
		}
	}

	if raw.UsageMetadata != nil {
		var grounding json.RawMessage
		if len(raw.Candidates) > 0 {
			grounding = raw.Candidates[0].GroundingMetadata
		}
		u := gemUsageToCore(*raw.UsageMetadata, grounding)
		chunks = append(chunks, core.StreamChunk{Type: core.ChunkUsage, Usage: &u})
	}
	return chunks, nil
}

// Typed wire shapes for streamed GenerateContentResponse fragments.
type gemStreamOut struct {
	Candidates    []gemCandidateOut `json:"candidates"`
	UsageMetadata *gemUsageOut      `json:"usageMetadata,omitempty"`
}

type gemCandidateOut struct {
	Content      *gemContentOut `json:"content,omitempty"`
	FinishReason string         `json:"finishReason,omitempty"`
	Index        int            `json:"index"`
}

type gemContentOut struct {
	Role  string       `json:"role"`
	Parts []gemPartOut `json:"parts"`
}

type gemPartOut struct {
	Text             *string          `json:"text,omitempty"`
	Thought          bool             `json:"thought,omitempty"`
	ThoughtSignature string           `json:"thoughtSignature,omitempty"`
	FunctionCall     *gemFunctionCall `json:"functionCall,omitempty"`
}

type gemUsageOut struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount,omitempty"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
}

func gemUsageFromCore(u core.Usage) *gemUsageOut {
	candidates := u.CompletionTokens - u.ReasoningTokens
	if candidates < 0 {
		candidates = u.CompletionTokens
	}
	return &gemUsageOut{
		PromptTokenCount:        u.PromptTokens,
		CandidatesTokenCount:    candidates,
		TotalTokenCount:         u.TotalTokens,
		ThoughtsTokenCount:      u.ReasoningTokens,
		CachedContentTokenCount: u.CachedTokens,
	}
}

func gemModelPart(part gemPartOut) []byte {
	return sseData(&gemStreamOut{Candidates: []gemCandidateOut{{
		Content: &gemContentOut{Role: "model", Parts: []gemPartOut{part}},
		Index:   0,
	}}})
}

// RenderStreamChunk encodes a canonical chunk as a Gemini SSE event. Gemini
// streams each fragment as a standalone GenerateContentResponse, so text,
// tool-call, finish, and usage chunks each become one "data:" line.
func (GeminiCodec) RenderStreamChunk(chunk core.StreamChunk, _ *StreamState) ([][]byte, error) {
	switch chunk.Type {
	case core.ChunkThinking:
		return [][]byte{gemModelPart(gemPartOut{Text: &chunk.Delta, Thought: true})}, nil

	case core.ChunkText:
		return [][]byte{gemModelPart(gemPartOut{Text: &chunk.Delta})}, nil

	case core.ChunkToolCall:
		if chunk.ToolCall == nil {
			return nil, nil
		}
		args := chunk.ToolCall.Arguments
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}

		idStr := chunk.ToolCall.ID
		idStr = strings.TrimPrefix(idStr, "call_")

		var thoughtSig string
		if idx := strings.Index(idStr, "__sig__"); idx >= 0 {
			if sigB, err := base64.RawURLEncoding.DecodeString(idStr[idx+7:]); err == nil {
				thoughtSig = string(sigB)
			}
			idStr = idStr[:idx]
		}

		idToSend := idStr
		if _, synthetic := stripGeminiSyntheticID(idStr); synthetic {
			idToSend = ""
		}
		if idToSend == chunk.ToolCall.Name || idToSend == strings.ReplaceAll(chunk.ToolCall.Name, ":", "_") {
			idToSend = ""
		}

		return [][]byte{gemModelPart(gemPartOut{
			ThoughtSignature: thoughtSig,
			FunctionCall:     &gemFunctionCall{ID: idToSend, Name: chunk.ToolCall.Name, Args: args},
		})}, nil

	case core.ChunkFinish:
		return [][]byte{sseData(&gemStreamOut{Candidates: []gemCandidateOut{{
			Content:      &gemContentOut{Role: "model", Parts: []gemPartOut{}},
			FinishReason: renderGemFinish(chunk.FinishReason),
			Index:        0,
		}}})}, nil

	case core.ChunkUsage:
		if chunk.Usage == nil {
			return nil, nil
		}
		return [][]byte{sseData(&gemStreamOut{
			Candidates:    []gemCandidateOut{},
			UsageMetadata: gemUsageFromCore(*chunk.Usage),
		})}, nil

	default:
		return nil, nil
	}
}

// RenderStreamDone: Gemini streams have no terminal sentinel.
func (GeminiCodec) RenderStreamDone(_ *StreamState) [][]byte { return nil }
