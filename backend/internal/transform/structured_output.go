package transform

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// The canonical structured-output request is OpenAI's response_format:
// {"type":"json_object"} or {"type":"json_schema","json_schema":{name,schema,strict}}.

// StructuredOutputTool is the forced tool that carries a JSON schema to
// Anthropic models when the client did not ask for Anthropic's native format.
const StructuredOutputTool = "keirouter_structured_output"

type responseFormat struct {
	Type       string `json:"type"`
	JSONSchema *struct {
		Name        string          `json:"name,omitempty"`
		Description string          `json:"description,omitempty"`
		Schema      json.RawMessage `json:"schema,omitempty"`
		Strict      *bool           `json:"strict,omitempty"`
	} `json:"json_schema,omitempty"`
}

func decodeResponseFormat(raw json.RawMessage) (responseFormat, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return responseFormat{}, false
	}
	var rf responseFormat
	if json.Unmarshal(raw, &rf) != nil {
		return rf, false
	}
	return rf, rf.Type == "json_object" || rf.Type == "json_schema"
}

func (rf responseFormat) schema() json.RawMessage {
	if rf.Type != "json_schema" || rf.JSONSchema == nil || len(rf.JSONSchema.Schema) == 0 {
		return nil
	}
	return rf.JSONSchema.Schema
}

// canonicalResponseFormat builds the canonical form from a schema. A nil
// schema means plain JSON mode.
func canonicalResponseFormat(name string, schema json.RawMessage, strict *bool) json.RawMessage {
	if len(bytes.TrimSpace(schema)) == 0 {
		return json.RawMessage(`{"type":"json_object"}`)
	}
	if name == "" {
		name = "response"
	}
	// A struct keeps the field order, and so the bytes, stable for prompt caching.
	out, _ := json.Marshal(struct {
		Type       string `json:"type"`
		JSONSchema struct {
			Name   string          `json:"name"`
			Schema json.RawMessage `json:"schema"`
			Strict *bool           `json:"strict,omitempty"`
		} `json:"json_schema"`
	}{Type: "json_schema", JSONSchema: struct {
		Name   string          `json:"name"`
		Schema json.RawMessage `json:"schema"`
		Strict *bool           `json:"strict,omitempty"`
	}{name, schema, strict}})
	return out
}

// structuredOutputInstruction asks for JSON in the prompt, for upstreams that
// cannot enforce the schema themselves.
func structuredOutputInstruction(rf responseFormat) string {
	if s := rf.schema(); s != nil {
		return "Respond with a single JSON value that matches this JSON schema. Output only the JSON, without prose or code fences.\n\n" + string(s)
	}
	return "Respond with a single valid JSON object. Output only the JSON, without prose or code fences."
}

func appendSystem(system, text string) string {
	if system == "" || text == "" {
		return system + text
	}
	return system + "\n\n" + text
}

// lowercaseSchemaTypes converts an OpenAPI-style schema (Gemini responseSchema,
// "type":"OBJECT") into standard JSON Schema type names.
func lowercaseSchemaTypes(raw json.RawMessage) json.RawMessage {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return raw
	}
	var walk func(any)
	walk = func(n any) {
		switch t := n.(type) {
		case map[string]any:
			for k, child := range t {
				if s, ok := child.(string); ok && k == "type" {
					t[k] = strings.ToLower(s)
					continue
				}
				walk(child)
			}
		case []any:
			for _, child := range t {
				walk(child)
			}
		}
	}
	walk(v)
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

// UnwrapStructuredOutput turns the forced structured-output tool call of a
// unary response back into the JSON text the client asked for.
func UnwrapStructuredOutput(resp *core.ChatResponse) {
	if resp == nil {
		return
	}
	for i, p := range resp.Message.Content {
		if p.Type != core.PartToolCall || p.ToolCall == nil || p.ToolCall.Name != StructuredOutputTool {
			continue
		}
		resp.Message.Content[i] = core.ContentPart{Type: core.PartText, Text: string(p.ToolCall.Arguments)}
		if resp.FinishReason == core.FinishToolCalls {
			resp.FinishReason = core.FinishStop
		}
	}
}

// StructuredOutputStream turns the forced structured-output tool call of a
// stream back into text deltas, so the client sees plain JSON content.
type StructuredOutputStream struct {
	index  int
	active bool
}

// Process rewrites a chunk of the forced tool; it reports false for a chunk to drop.
func (s *StructuredOutputStream) Process(chunk core.StreamChunk) (core.StreamChunk, bool) {
	switch chunk.Type {
	case core.ChunkToolCall:
		if chunk.ToolCall == nil {
			return chunk, true
		}
		if chunk.ToolCall.Name == StructuredOutputTool {
			s.active, s.index = true, chunk.Index
		} else if !s.active || chunk.ToolCall.ID != "" || chunk.Index != s.index {
			return chunk, true
		}
		args := string(chunk.ToolCall.Arguments)
		// Anthropic opens tool_use with an empty input placeholder.
		if args == "" || (args == "{}" && chunk.ToolCall.Name == StructuredOutputTool) {
			return core.StreamChunk{}, false
		}
		return core.StreamChunk{Type: core.ChunkText, Delta: args}, true
	case core.ChunkFinish:
		if s.active && chunk.FinishReason == core.FinishToolCalls {
			chunk.FinishReason = core.FinishStop
		}
	}
	return chunk, true
}
