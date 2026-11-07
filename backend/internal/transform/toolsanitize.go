package transform

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// ToolArgSanitizer buffers streaming tool call arguments and emits each call
// once it is complete. Arguments reach the client byte-identical to what the
// upstream sent, except for the Claude Code repairs, which only run when
// ClaudeCodeRepairs is set.
//
// Usage: call Process() for each chunk. Call Flush() when the stream ends.
// Flush is idempotent — calling it multiple times is safe and will not
// re-emit already-flushed tool calls.
type ToolArgSanitizer struct {
	// buffers tracks in-flight tool calls by their stream index.
	buffers map[int]*toolBuffer
	// flushed prevents double-emission when Flush is called multiple times
	// (e.g. on duplicate ChunkFinish from upstream providers).
	flushed     bool
	sawToolCall bool

	// ClaudeCodeRepairs fixes argument types that non-Anthropic models get
	// wrong for Claude Code's built-in tools. Only set it for Anthropic clients:
	// another client's tool named "Read" has its own schema.
	ClaudeCodeRepairs bool
}

type toolBuffer struct {
	id     string
	name   string
	custom bool
	args   strings.Builder
	// argsLen tracks the length of args accumulated so far, used to detect
	// snapshot-style argument retransmissions (some providers re-send the full
	// accumulated arguments on every chunk instead of just the new delta).
	argsLen int
}

// NewToolArgSanitizer creates a new sanitizer.
func NewToolArgSanitizer() *ToolArgSanitizer {
	return &ToolArgSanitizer{buffers: make(map[int]*toolBuffer)}
}

// Process handles one streaming chunk. For ChunkToolCall, it buffers arguments
// and emits sanitized chunks via the emit callback. All other chunk types are
// passed through directly.
func (s *ToolArgSanitizer) Process(chunk core.StreamChunk, emit func(core.StreamChunk)) {
	if chunk.Type != core.ChunkToolCall || chunk.ToolCall == nil {
		// Flush buffered tool calls BEFORE emitting finish, so clients see
		// tool call data before the finish_reason signal. Without this,
		// ChunkFinish passes through immediately while tool calls are still
		// buffered — causing parse errors in CLI tools like Cline.
		if chunk.Type == core.ChunkFinish {
			s.Flush(emit)
			// Gemini can put functionCall and STOP in separate SSE frames. Once
			// this stream has emitted a tool call, STOP completes the tool-call
			// turn rather than the whole conversation.
			if s.sawToolCall && chunk.FinishReason == core.FinishStop {
				chunk.FinishReason = core.FinishToolCalls
			}
			s.sawToolCall = false
		}
		emit(chunk)
		return
	}
	s.sawToolCall = true

	idx := chunk.Index

	// Start a new buffer only when this chunk opens a genuinely new tool call.
	// A chunk opens a new call when it carries an ID that differs from the
	// buffer currently at this index (or no buffer exists yet). Some providers
	// (e.g. Kiro) repeat the same tool-use ID on the argument-delta chunks that
	// follow the opening chunk; treating those as new calls would prematurely
	// flush the still-empty buffer (emitting a tool call with "{}" args) and
	// then accumulate the real arguments into a second, name-less buffer —
	// which is exactly what makes a client see "read_file" with no parameters.
	if chunk.ToolCall.ID != "" {
		if existing, ok := s.buffers[idx]; !ok || existing.id != chunk.ToolCall.ID {
			s.flushIndex(idx, emit)
			s.buffers[idx] = &toolBuffer{
				id:     chunk.ToolCall.ID,
				name:   chunk.ToolCall.Name,
				custom: chunk.ToolCall.Custom,
			}
		}
	}

	// Accumulate arguments.
	buf, ok := s.buffers[idx]
	if !ok {
		// No buffer for this index (shouldn't happen). Pass through.
		emit(chunk)
		return
	}

	args := string(chunk.ToolCall.Arguments)
	if args == "{}" && isPlaceholderPosition(buf) {
		return
	}
	appendToolArgs(buf, args)
}

// isPlaceholderPosition reports whether a "{}" fragment is the empty input
// Anthropic decoders emit when a tool_use block opens (or a repeat of it),
// rather than an empty object inside the arguments.
func isPlaceholderPosition(buf *toolBuffer) bool {
	existing := buf.args.String()
	return existing == "" || isCompleteJSONObject(existing)
}

// appendToolArgs accumulates tool-call argument fragments into the buffer.
// Providers send arguments in two shapes:
//
//  1. Incremental deltas — each chunk carries only the NEW fragment. Concatenate
//     verbatim, even when a fragment's leading bytes repeat the tail (e.g. "l" + "l").
//  2. Full snapshots — each chunk re-sends the ENTIRE accumulated arguments.
//     Concatenating would duplicate (e.g. `{"a":1}{"a":1}` → invalid JSON).
//
// Detection strategy: replace when a fragment extends the existing prefix, or
// when both the existing value and fragment are complete objects. A complete
// object can also be a legitimate nested delta, so it is not sufficient on its
// own to classify a fragment as a snapshot.
func appendToolArgs(buf *toolBuffer, fragment string) {
	if fragment == "" {
		return
	}
	existing := buf.args.String()
	if existing == "" {
		buf.args.WriteString(fragment)
		buf.argsLen = len(fragment)
		return
	}
	if strings.HasPrefix(fragment, existing) ||
		(isCompleteJSONObject(existing) && isCompleteJSONObject(fragment)) {
		buf.args.Reset()
		buf.args.WriteString(fragment)
		buf.argsLen = len(fragment)
		return
	}
	// True incremental delta: append verbatim.
	buf.args.WriteString(fragment)
	buf.argsLen += len(fragment)
}

// isCompleteJSONObject reports whether the fragment is a standalone, complete
// JSON object (starts with '{', ends with '}', and parses as valid JSON).
// This is used to distinguish snapshot-style argument retransmissions from
// true incremental deltas.
func isCompleteJSONObject(fragment string) bool {
	trimmed := strings.TrimSpace(fragment)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return false
	}
	// Parse it to confirm it's valid JSON (not just braces around garbage).
	var v map[string]any
	return json.Unmarshal([]byte(trimmed), &v) == nil
}

// Flush emits all remaining buffered tool calls. Call this when the stream ends
// (before ChunkFinish or after the channel closes). Safe to call multiple times;
// subsequent calls are no-ops.
func (s *ToolArgSanitizer) Flush(emit func(core.StreamChunk)) {
	if s.flushed {
		return
	}
	s.flushed = true
	for _, idx := range slices.Sorted(maps.Keys(s.buffers)) {
		s.flushIndex(idx, emit)
	}
}

// flushIndex emits a single sanitized tool call for the given index.
func (s *ToolArgSanitizer) flushIndex(idx int, emit func(core.StreamChunk)) {
	buf, ok := s.buffers[idx]
	if !ok {
		return
	}
	delete(s.buffers, idx)

	args := buf.args.String()
	if args == "" {
		args = "{}"
	}

	if s.ClaudeCodeRepairs {
		args = sanitizeToolArgs(buf.name, args)
	}

	emit(core.StreamChunk{
		Type:  core.ChunkToolCall,
		Index: idx,
		ToolCall: &core.ToolCall{
			ID:        buf.id,
			Name:      buf.name,
			Arguments: json.RawMessage(args),
			Custom:    buf.custom,
		},
	})
}

// sanitizeToolArgs fixes Read arguments that non-Anthropic models send with
// the wrong types. Arguments that need no fix are returned unchanged.
func sanitizeToolArgs(toolName, argsJSON string) string {
	if toolName != "Read" {
		return argsJSON
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return argsJSON
	}
	if !sanitizeReadArgs(args) {
		return argsJSON
	}
	out, err := json.Marshal(args)
	if err != nil {
		return argsJSON
	}
	return string(out)
}

// sanitizeReadArgs coerces limit and offset to clamped integers and drops pages
// for non-PDF files. It reports whether anything changed.
func sanitizeReadArgs(args map[string]any) bool {
	before := len(args)
	changed := false
	if v, ok := args["limit"]; ok {
		switch val := v.(type) {
		case string:
			if n, err := strconv.Atoi(val); err == nil {
				args["limit"] = clampInt(n, 1, 2000)
			} else {
				delete(args, "limit")
			}
			changed = true
		case float64:
			if n := clampInt(int(val), 1, 2000); float64(n) != val {
				args["limit"] = n
				changed = true
			}
		}
	}
	if v, ok := args["offset"]; ok {
		switch val := v.(type) {
		case string:
			if n, err := strconv.Atoi(val); err == nil {
				args["offset"] = maxInt(n, 0)
			} else {
				delete(args, "offset")
			}
			changed = true
		case float64:
			if n := maxInt(int(val), 0); float64(n) != val {
				args["offset"] = n
				changed = true
			}
		}
	}
	if pages, ok := args["pages"]; ok {
		fp, _ := args["file_path"].(string)
		if _, isString := pages.(string); !isString || !strings.HasSuffix(strings.ToLower(fp), ".pdf") {
			delete(args, "pages")
		}
	}
	return changed || len(args) != before
}

func clampInt(val, min, max int) int {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
