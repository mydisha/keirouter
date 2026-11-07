package connectors

import (
	"bytes"
	"encoding/json"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// qoderToolArgWrappers are the keys under which Qoder nests the real tool
// arguments ({"input": {...}}).
var qoderToolArgWrappers = []string{
	"input", "arguments", "parameters", "args",
	"tool_input", "toolInput", "tool_arguments", "toolArguments",
	"tool_parameters", "toolParameters", "payload", "data",
}

// unwrapQoderToolArgs replaces wrapped tool arguments with the inner object.
// Only complete objects are unwrapped; streamed fragments pass through.
func unwrapQoderToolArgs(chunks []core.StreamChunk) []core.StreamChunk {
	for i := range chunks {
		tc := chunks[i].ToolCall
		if chunks[i].Type != core.ChunkToolCall || tc == nil {
			continue
		}
		args := bytes.TrimSpace(tc.Arguments)
		if len(args) == 0 || args[0] != '{' || !json.Valid(args) {
			continue
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(args, &obj) != nil {
			continue
		}
		if inner := unwrapQoderObject(obj); inner != nil {
			tc.Arguments = inner
		}
	}
	return chunks
}

func unwrapQoderObject(obj map[string]json.RawMessage) json.RawMessage {
	for _, key := range qoderToolArgWrappers {
		if inner := qoderInnerObject(obj[key]); inner != nil {
			return inner
		}
	}
	// A single unknown key holding an object is also a wrapper.
	if len(obj) == 1 {
		for _, v := range obj {
			return qoderInnerObject(v)
		}
	}
	return nil
}

func qoderInnerObject(raw json.RawMessage) json.RawMessage {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil
		}
		raw = bytes.TrimSpace([]byte(s))
	}
	if len(raw) == 0 || raw[0] != '{' {
		return nil
	}
	var nested map[string]json.RawMessage
	if json.Unmarshal(raw, &nested) != nil || len(nested) == 0 {
		return nil
	}
	if deeper := unwrapQoderObject(nested); deeper != nil {
		return deeper
	}
	return raw
}
