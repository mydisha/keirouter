package connectors

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/errclass"
)

// OCI Generative AI chat responses: non-streaming bodies, in-band errors and
// SSE stream frames for both the GENERIC and COHERE formats.

// ---- responses --------------------------------------------------------------

type ociChatResult struct {
	ModelID      string          `json:"modelId"`
	ChatResponse json.RawMessage `json:"chatResponse"`
}

type ociGenericResponse struct {
	APIFormat string `json:"apiFormat"`
	Choices   []struct {
		Message      *ociRespMessage `json:"message"`
		FinishReason string          `json:"finishReason"`
	} `json:"choices"`
	Usage *ociUsage `json:"usage"`
}

type ociRespMessage struct {
	Content          []ociRespPart `json:"content"`
	ToolCalls        []ociToolCall `json:"toolCalls"`
	ReasoningContent string        `json:"reasoningContent"`
}

type ociRespPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ociUsage struct {
	PromptTokens            int `json:"promptTokens"`
	CompletionTokens        int `json:"completionTokens"`
	TotalTokens             int `json:"totalTokens"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoningTokens"`
	} `json:"completionTokensDetails"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cachedTokens"`
	} `json:"promptTokensDetails"`
}

func (u *ociUsage) canonical() *core.Usage {
	if u == nil {
		return nil
	}
	out := &core.Usage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens,
		TotalTokens: u.TotalTokens, Source: core.UsageSourceProvider}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
	}
	if u.CompletionTokensDetails != nil {
		out.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
	if u.PromptTokensDetails != nil {
		out.CachedTokens = u.PromptTokensDetails.CachedTokens
	}
	return out
}

type ociCohereResponse struct {
	APIFormat    string              `json:"apiFormat"`
	Text         string              `json:"text"`
	FinishReason string              `json:"finishReason"`
	ToolCalls    []ociCohereToolCall `json:"toolCalls"`
	ChatHistory  json.RawMessage     `json:"chatHistory"`
	ErrorMessage string              `json:"errorMessage"`
	Usage        *ociUsage           `json:"usage"`
}

// ociFinishReason maps OCI finish reasons (COMPLETE, MAX_TOKENS, TOOL_CALLS,
// ERROR_TOXIC, ... and the lowercase OpenAI words some vendors pass through).
func ociFinishReason(raw string) core.FinishReason {
	switch strings.ToUpper(raw) {
	case "", "COMPLETE", "STOP", "END_TURN", "STOP_SEQUENCE":
		return core.FinishStop
	case "MAX_TOKENS", "LENGTH":
		return core.FinishLength
	case "TOOL_CALL", "TOOL_CALLS":
		return core.FinishToolCalls
	case "CONTENT_FILTERED", "CONTENT_FILTER", "ERROR_TOXIC":
		return core.FinishFilter
	}
	return core.FinishStop
}

// ociToolCallID derives a stable id for tool calls the upstream leaves
// unnamed (always for the Cohere format, sometimes for generic vendors), so a
// re-emitted call keeps its identity.
func ociToolCallID(position int, name, args string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s", position, name, args)))
	return "call_" + hex.EncodeToString(sum[:])[:24]
}

// parseOCIChatResponse converts a non-streaming /actions/chat body.
func parseOCIChatResponse(body []byte, model string) (*core.ChatResponse, error) {
	var res ociChatResult
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if len(res.ChatResponse) == 0 {
		return nil, fmt.Errorf("response carries no chatResponse")
	}
	var probe struct {
		APIFormat string `json:"apiFormat"`
	}
	_ = json.Unmarshal(res.ChatResponse, &probe)
	out := &core.ChatResponse{Model: model, Message: core.Message{Role: core.RoleAssistant}}

	if probe.APIFormat == ociFormatCohere {
		var cr ociCohereResponse
		if err := json.Unmarshal(res.ChatResponse, &cr); err != nil {
			return nil, fmt.Errorf("parse cohere response: %w", err)
		}
		if cr.ErrorMessage != "" && strings.HasPrefix(strings.ToUpper(cr.FinishReason), "ERROR") {
			return nil, ociInBandError("", 0, cr.ErrorMessage)
		}
		if cr.Text != "" {
			out.Message.Content = append(out.Message.Content, core.ContentPart{Type: core.PartText, Text: cr.Text})
		}
		for i, tc := range cr.ToolCalls {
			args := string(ociCohereParams(tc.Parameters))
			out.Message.Content = append(out.Message.Content, core.ContentPart{Type: core.PartToolCall, ToolCall: &core.ToolCall{
				ID: ociToolCallID(i, tc.Name, args), Name: tc.Name, Arguments: json.RawMessage(args)}})
		}
		out.FinishReason = ociFinishReason(cr.FinishReason)
		if len(cr.ToolCalls) > 0 && out.FinishReason == core.FinishStop {
			out.FinishReason = core.FinishToolCalls
		}
		if u := cr.Usage.canonical(); u != nil {
			out.Usage = *u
		}
		return out, nil
	}

	var gr ociGenericResponse
	if err := json.Unmarshal(res.ChatResponse, &gr); err != nil {
		return nil, fmt.Errorf("parse generic response: %w", err)
	}
	if len(gr.Choices) == 0 {
		return nil, fmt.Errorf("response contained no choices")
	}
	choice := gr.Choices[0]
	sawCall := false
	if msg := choice.Message; msg != nil {
		if msg.ReasoningContent != "" {
			out.Message.Content = append(out.Message.Content, core.ContentPart{Type: core.PartThinking, Text: msg.ReasoningContent})
		}
		var text strings.Builder
		for _, p := range msg.Content {
			if strings.EqualFold(p.Type, "TEXT") {
				text.WriteString(p.Text)
			}
		}
		if text.Len() > 0 {
			out.Message.Content = append(out.Message.Content, core.ContentPart{Type: core.PartText, Text: text.String()})
		}
		for i, tc := range msg.ToolCalls {
			id := tc.ID
			if id == "" {
				id = ociToolCallID(i, tc.Name, tc.Arguments)
			}
			out.Message.Content = append(out.Message.Content, core.ContentPart{Type: core.PartToolCall, ToolCall: &core.ToolCall{
				ID: id, Name: tc.Name, Arguments: json.RawMessage(ociArgs(json.RawMessage(tc.Arguments)))}})
			sawCall = true
		}
	}
	out.FinishReason = ociFinishReason(choice.FinishReason)
	if sawCall && out.FinishReason == core.FinishStop {
		out.FinishReason = core.FinishToolCalls
	}
	if u := gr.Usage.canonical(); u != nil {
		out.Usage = *u
	}
	return out, nil
}

// ociInBandError classifies an error reported inside a successful response or
// stream frame (OCI error code vocabulary: TooManyRequests, NotAuthenticated,
// NotAuthorizedOrNotFound, InvalidParameter, InternalServerError, ...).
func ociInBandError(code string, status int, message string) *core.ProviderError {
	errType := ""
	switch strings.ToLower(code) {
	case "toomanyrequests":
		errType = "rate_limit_error"
	case "notauthenticated", "signaturenotvalid", "notauthorized":
		errType = "authentication_error"
	case "notauthorizedornotfound", "notfound":
		errType = "not_found_error"
	case "internalservererror", "serviceunavailable":
		errType = "api_error"
	case "invalidparameter", "missingparameter", "badrequest":
		if status == 0 {
			status = 400
		}
	}
	return errclass.ClassifyErrorDetail(errType, status, "", message)
}

// ---- streaming --------------------------------------------------------------

// ociStreamState carries cross-frame state for one OCI chat stream: open
// generic tool calls, Cohere restatement suppression, and the finish reason
// and usage held back until the stream ends.
type ociStreamState struct {
	calls      []ociOpenCall
	sawCall    bool
	cohereText bool
	cohereCall bool
	finish     core.FinishReason
	usage      *core.Usage
}

type ociOpenCall struct{ id, name string }

type ociStreamFrame struct {
	APIFormat    string          `json:"apiFormat"`
	FinishReason string          `json:"finishReason"`
	Message      json.RawMessage `json:"message"`
	Usage        *ociUsage       `json:"usage"`
	Code         string          `json:"code"`
	// Cohere fields.
	Text         *string             `json:"text"`
	ToolCalls    []ociCohereToolCall `json:"toolCalls"`
	ChatHistory  json.RawMessage     `json:"chatHistory"`
	ErrorMessage string              `json:"errorMessage"`
}

type ociStreamDelta struct {
	Content          []ociRespPart `json:"content"`
	ToolCalls        []ociToolCall `json:"toolCalls"`
	ReasoningContent string        `json:"reasoningContent"`
}

// parse converts one SSE data payload into canonical chunks. Finish and
// usage are recorded, not emitted; the caller flushes them at stream end.
func (s *ociStreamState) parse(payload []byte) ([]core.StreamChunk, error) {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return nil, nil
	}
	if string(payload) == "[DONE]" {
		return nil, errStopStream
	}
	if pe, ok := errclass.StreamErrorFrame(payload); ok {
		return []core.StreamChunk{{Type: core.ChunkError, Err: pe}}, nil
	}
	var f ociStreamFrame
	if err := json.Unmarshal(payload, &f); err != nil {
		return nil, err
	}
	// OCI service errors: {"code": "...", "message": "<string>"}.
	if f.Code != "" && len(f.Message) > 0 && f.Message[0] == '"' {
		var msg string
		_ = json.Unmarshal(f.Message, &msg)
		return []core.StreamChunk{{Type: core.ChunkError, Err: ociInBandError(f.Code, 0, msg)}}, nil
	}
	if u := f.Usage.canonical(); u != nil {
		s.usage = u
	}
	if f.FinishReason != "" {
		s.finish = ociFinishReason(f.FinishReason)
	}
	if f.APIFormat == ociFormatCohere {
		return s.parseCohere(&f)
	}

	if len(f.Message) == 0 || f.Message[0] != '{' {
		return nil, nil
	}
	var d ociStreamDelta
	if err := json.Unmarshal(f.Message, &d); err != nil {
		return nil, err
	}
	var out []core.StreamChunk
	if d.ReasoningContent != "" {
		out = append(out, core.StreamChunk{Type: core.ChunkThinking, Delta: d.ReasoningContent})
	}
	for _, p := range d.Content {
		if strings.EqualFold(p.Type, "TEXT") && p.Text != "" {
			out = append(out, core.StreamChunk{Type: core.ChunkText, Delta: p.Text})
		}
	}
	for i, tc := range d.ToolCalls {
		out = append(out, s.genericToolDelta(i, tc))
	}
	return out, nil
}

// genericToolDelta maps one streamed generic tool-call fragment. A fragment
// with a new id (or a name but no id) opens a call; an id-less, name-less
// fragment continues the most recent call's arguments.
func (s *ociStreamState) genericToolDelta(position int, tc ociToolCall) core.StreamChunk {
	s.sawCall = true
	idx := -1
	switch {
	case tc.ID != "":
		for i, c := range s.calls {
			if c.id == tc.ID {
				idx = i
			}
		}
	case tc.Name == "" && len(s.calls) > 0:
		idx = len(s.calls) - 1
	}
	if idx < 0 {
		id := tc.ID
		if id == "" {
			id = ociToolCallID(len(s.calls)+position, tc.Name, tc.Arguments)
		}
		s.calls = append(s.calls, ociOpenCall{id: id, name: tc.Name})
		idx = len(s.calls) - 1
		return core.StreamChunk{Type: core.ChunkToolCall, Index: idx,
			ToolCall: &core.ToolCall{ID: id, Name: tc.Name, Arguments: json.RawMessage(tc.Arguments)}}
	}
	return core.StreamChunk{Type: core.ChunkToolCall, Index: idx,
		ToolCall: &core.ToolCall{ID: s.calls[idx].id, Arguments: json.RawMessage(tc.Arguments)}}
}

// parseCohere handles a Cohere-format frame. The service streams single
// tokens in "text", then restates the whole answer on the frame carrying
// toolCalls and again on the terminal frame carrying chatHistory; those
// restatements are dropped once the content has already been emitted.
func (s *ociStreamState) parseCohere(f *ociStreamFrame) ([]core.StreamChunk, error) {
	if f.ErrorMessage != "" && strings.HasPrefix(strings.ToUpper(f.FinishReason), "ERROR") {
		return []core.StreamChunk{{Type: core.ChunkError, Err: ociInBandError("", 0, f.ErrorMessage)}}, nil
	}
	restatesText := len(f.ChatHistory) > 0 || f.ToolCalls != nil
	restatesCalls := len(f.ChatHistory) > 0
	var out []core.StreamChunk
	if f.Text != nil && *f.Text != "" && !(restatesText && s.cohereText) {
		out = append(out, core.StreamChunk{Type: core.ChunkText, Delta: *f.Text})
		s.cohereText = true
	}
	if len(f.ToolCalls) > 0 && !(restatesCalls && s.cohereCall) {
		for i, tc := range f.ToolCalls {
			args := string(ociCohereParams(tc.Parameters))
			id := ociToolCallID(i, tc.Name, args)
			out = append(out, core.StreamChunk{Type: core.ChunkToolCall, Index: len(s.calls),
				ToolCall: &core.ToolCall{ID: id, Name: tc.Name, Arguments: json.RawMessage(args)}})
			s.calls = append(s.calls, ociOpenCall{id: id, name: tc.Name})
		}
		s.cohereCall = true
		s.sawCall = true
	}
	return out, nil
}

// tail returns the chunks that close a cleanly finished stream.
func (s *ociStreamState) tail() []core.StreamChunk {
	finish := s.finish
	if finish == "" {
		finish = core.FinishStop
	}
	if s.sawCall && finish == core.FinishStop {
		finish = core.FinishToolCalls
	}
	var out []core.StreamChunk
	if s.usage != nil {
		out = append(out, core.StreamChunk{Type: core.ChunkUsage, Usage: s.usage})
	}
	return append(out, core.StreamChunk{Type: core.ChunkFinish, FinishReason: finish})
}
