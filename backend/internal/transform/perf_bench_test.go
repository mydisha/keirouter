package transform

// Performance benchmarks modelling a Claude Code-style agentic request:
// ~60 messages (20 tool_use/tool_result pairs with ~2 KB JSON args/results),
// 40 tools with ~1 KB JSON schemas, a 6 KB system prompt, and a 300-frame SSE
// stream (text deltas + one tool call split over 50 argument fragments).

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// ---- fixture generators -----------------------------------------------------

var perfWords = []string{"refactor", "the", "handler", "so", "that", "every", "request", "is", "validated",
	"before", "dispatch", "and", "the", "retry", "budget", "is", "shared", "across", "attempts", "while",
	"keeping", "latency", "low", "for", "streaming", "clients"}

func perfProse(n int) string {
	var b strings.Builder
	b.Grow(n + 32)
	for i := 0; b.Len() < n; i++ {
		b.WriteString(perfWords[i%len(perfWords)])
		if i%17 == 16 {
			b.WriteString(".\n")
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()[:n]
}

func perfCode(n int) string {
	var b strings.Builder
	b.Grow(n + 128)
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "\tif err := step%d(ctx, \"arg-%d\"); err != nil {\n\t\treturn fmt.Errorf(\"step %d: %%w\", err)\n\t}\n", i, i, i)
	}
	return b.String()[:n]
}

func perfSystemPrompt() string { return perfProse(6 * 1024) }

func perfToolName(i int) string {
	names := []string{"Read", "Write", "Edit", "Bash", "Glob", "Grep", "WebFetch", "TodoWrite"}
	if i < len(names) {
		return names[i]
	}
	return fmt.Sprintf("mcp__server__tool_%d", i)
}

// perfToolSchema is ~1.1 KB and carries keywords Gemini strips
// ($schema, additionalProperties, default, minimum, pattern, anyOf, type arrays).
func perfToolSchema(i int) string {
	d := perfProse(60)
	return fmt.Sprintf(`{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","additionalProperties":false,"properties":{"file_path":{"type":"string","description":%q},"offset":{"type":"integer","minimum":0,"description":%q},"limit":{"type":"integer","minimum":1,"maximum":2000,"default":2000,"description":%q},"pattern":{"type":"string","pattern":"^.*$","description":%q},"mode":{"type":"string","enum":["fast","full","dry_run"],"default":"fast","description":%q},"options":{"type":"object","additionalProperties":false,"properties":{"recursive":{"type":"boolean","default":false,"description":%q},"depth":{"type":["integer","null"],"description":%q},"tag_%d":{"anyOf":[{"type":"string"},{"type":"null"}],"description":%q}}},"paths":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"description":%q}},"required":["file_path","pattern"]}`,
		d, d, d, d, d, d, d, i, d, d)
}

// perfToolArgsJSON is a ~2 KB JSON object (Write-style args).
func perfToolArgsJSON(i int) string {
	c, _ := json.Marshal(perfCode(1900))
	return fmt.Sprintf(`{"file_path":"/home/user/project/internal/pkg_%d/handler.go","content":%s}`, i, c)
}

// perfToolResultText is a ~2 KB tool result string.
func perfToolResultText(i int) string {
	return fmt.Sprintf("     1\tpackage pkg_%d\n%s", i, perfCode(1900))
}

func perfOpenAIRequestBody() []byte {
	type m = map[string]any
	msgs := []any{m{"role": "system", "content": perfSystemPrompt()},
		m{"role": "user", "content": perfProse(300)},
		m{"role": "assistant", "content": perfProse(200)}}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("call_%03d", i)
		msgs = append(msgs,
			m{"role": "assistant", "content": nil, "tool_calls": []any{m{"id": id, "type": "function",
				"function": m{"name": perfToolName(i % 40), "arguments": perfToolArgsJSON(i)}}}},
			m{"role": "tool", "tool_call_id": id, "content": perfToolResultText(i)})
		if i%2 == 1 {
			msgs = append(msgs, m{"role": "user", "content": perfProse(120)}, m{"role": "assistant", "content": perfProse(150)})
		}
	}
	tools := make([]any, 0, 40)
	for i := 0; i < 40; i++ {
		tools = append(tools, m{"type": "function", "function": m{"name": perfToolName(i), "description": perfProse(80),
			"parameters": json.RawMessage(perfToolSchema(i))}})
	}
	body, err := json.Marshal(m{"model": "gpt-4o", "stream": true, "max_tokens": 8192, "temperature": 0.2,
		"messages": msgs, "tools": tools})
	if err != nil {
		panic(err)
	}
	return body
}

func perfAnthropicRequestBody() []byte {
	type m = map[string]any
	msgs := []any{m{"role": "user", "content": perfProse(300)},
		m{"role": "assistant", "content": []any{m{"type": "text", "text": perfProse(200)}}}}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("toolu_%03d", i)
		msgs = append(msgs,
			m{"role": "assistant", "content": []any{m{"type": "text", "text": "Let me check that."},
				m{"type": "tool_use", "id": id, "name": perfToolName(i % 40), "input": json.RawMessage(perfToolArgsJSON(i))}}},
			m{"role": "user", "content": []any{m{"type": "tool_result", "tool_use_id": id, "content": perfToolResultText(i)}}})
		if i%2 == 1 {
			msgs = append(msgs, m{"role": "user", "content": perfProse(120)},
				m{"role": "assistant", "content": []any{m{"type": "text", "text": perfProse(150)}}})
		}
	}
	tools := make([]any, 0, 40)
	for i := 0; i < 40; i++ {
		tools = append(tools, m{"name": perfToolName(i), "description": perfProse(80),
			"input_schema": json.RawMessage(perfToolSchema(i))})
	}
	body, err := json.Marshal(m{"model": "claude-sonnet-4-5", "stream": true, "max_tokens": 8192,
		"system": perfSystemPrompt(), "messages": msgs, "tools": tools, "metadata": m{"user_id": "perf"}})
	if err != nil {
		panic(err)
	}
	return body
}

func perfTextDelta(i int) string {
	return perfWords[i%len(perfWords)] + " " + perfWords[(i*7)%len(perfWords)] + " "
}

// perfArgFragments splits a ~2 KB args object into 50 streaming fragments.
func perfArgFragments() []string {
	args := perfToolArgsJSON(7)
	const n = 50
	size := (len(args) + n - 1) / n
	out := make([]string, 0, n)
	for i := 0; i < len(args); i += size {
		end := i + size
		if end > len(args) {
			end = len(args)
		}
		out = append(out, args[i:end])
	}
	return out
}

// perfOpenAIFrames returns 300 OpenAI SSE data payloads (no "data: " prefix).
func perfOpenAIFrames() [][]byte {
	const pre = `{"id":"chatcmpl-perf","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","system_fingerprint":"fp_perf","choices":[{"index":0,"delta":`
	frames := make([][]byte, 0, 300)
	add := func(delta, finish string) {
		frames = append(frames, []byte(pre+delta+`,"logprobs":null,"finish_reason":`+finish+`}]}`))
	}
	add(`{"role":"assistant","content":""}`, "null")
	for i := 0; i < 246; i++ {
		t, _ := json.Marshal(perfTextDelta(i))
		add(`{"content":`+string(t)+`}`, "null")
	}
	add(`{"tool_calls":[{"index":0,"id":"call_perf","type":"function","function":{"name":"Write","arguments":""}}]}`, "null")
	for _, frag := range perfArgFragments() {
		f, _ := json.Marshal(frag)
		add(`{"tool_calls":[{"index":0,"function":{"arguments":`+string(f)+`}}]}`, "null")
	}
	add(`{}`, `"tool_calls"`)
	frames = append(frames, []byte(`{"id":"chatcmpl-perf","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[],"usage":{"prompt_tokens":31000,"completion_tokens":640,"total_tokens":31640,"prompt_tokens_details":{"cached_tokens":28000}}}`))
	return frames
}

// perfAnthropicFrames returns 300 Anthropic SSE data payloads.
func perfAnthropicFrames() [][]byte {
	frames := make([][]byte, 0, 300)
	add := func(s string) { frames = append(frames, []byte(s)) }
	add(`{"type":"message_start","message":{"id":"msg_perf","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":3000,"cache_creation_input_tokens":0,"cache_read_input_tokens":28000,"output_tokens":1}}}`)
	add(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	for i := 0; i < 243; i++ {
		t, _ := json.Marshal(perfTextDelta(i))
		add(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":` + string(t) + `}}`)
	}
	add(`{"type":"content_block_stop","index":0}`)
	add(`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_perf","name":"Write","input":{}}}`)
	for _, frag := range perfArgFragments() {
		f, _ := json.Marshal(frag)
		add(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":` + string(f) + `}}`)
	}
	add(`{"type":"content_block_stop","index":1}`)
	add(`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":640}}`)
	add(`{"type":"message_stop"}`)
	return frames
}

// perfGeminiFrames returns 300 Gemini SSE data payloads.
func perfGeminiFrames() [][]byte {
	frames := make([][]byte, 0, 300)
	for i := 0; i < 298; i++ {
		t, _ := json.Marshal(perfTextDelta(i))
		frames = append(frames, []byte(`{"candidates":[{"content":{"parts":[{"text":`+string(t)+`}],"role":"model"},"index":0}],"modelVersion":"gemini-2.5-pro"}`))
	}
	frames = append(frames, []byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"Write","args":`+perfToolArgsJSON(7)+`}}],"role":"model"},"index":0}]}`))
	frames = append(frames, []byte(`{"candidates":[{"content":{"parts":[{"text":""}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":31000,"candidatesTokenCount":640,"totalTokenCount":31640}}`))
	return frames
}

var (
	perfChunksOnce sync.Once
	perfChunksVal  []core.StreamChunk
)

// perfChunks is the canonical chunk sequence produced by parsing the OpenAI
// stream: ~300 chunks (text, tool_call start + 50 fragments, finish, usage).
func perfChunks() []core.StreamChunk {
	perfChunksOnce.Do(func() {
		for _, f := range perfOpenAIFrames() {
			cs, err := (OpenAICodec{}).ParseStreamLine(f, "gpt-4o")
			if err != nil {
				panic(err)
			}
			perfChunksVal = append(perfChunksVal, cs...)
		}
	})
	return perfChunksVal
}

func perfMustParseOpenAI(b *testing.B) *core.ChatRequest {
	req, err := (OpenAICodec{}).ParseRequest(perfOpenAIRequestBody())
	if err != nil {
		b.Fatal(err)
	}
	return req
}

// ---- request path ------------------------------------------------------------

func BenchmarkAnthropicParseRequest(b *testing.B) {
	body := perfAnthropicRequestBody()
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := (AnthropicCodec{}).ParseRequest(body); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAnthropicRenderRequest(b *testing.B) {
	req, err := (AnthropicCodec{}).ParseRequest(perfAnthropicRequestBody())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := (AnthropicCodec{}).RenderRequest(req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAnthropicParseRender(b *testing.B) {
	body := perfAnthropicRequestBody()
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req, err := (AnthropicCodec{}).ParseRequest(body)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := (AnthropicCodec{}).RenderRequest(req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOpenAIParseRequest(b *testing.B) {
	body := perfOpenAIRequestBody()
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := (OpenAICodec{}).ParseRequest(body); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOpenAIRenderRequestForProvider(b *testing.B) {
	req := perfMustParseOpenAI(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := (OpenAICodec{}).RenderRequestForProvider(req, "openai"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOpenAIParseRender(b *testing.B) {
	body := perfOpenAIRequestBody()
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req, err := (OpenAICodec{}).ParseRequest(body)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := (OpenAICodec{}).RenderRequestForProvider(req, "openai"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOpenAIParseAnthropicRender(b *testing.B) {
	body := perfOpenAIRequestBody()
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req, err := (OpenAICodec{}).ParseRequest(body)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := (AnthropicCodec{}).RenderRequest(req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOpenAIParseGeminiRender(b *testing.B) {
	body := perfOpenAIRequestBody()
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req, err := (OpenAICodec{}).ParseRequest(body)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := (GeminiCodec{}).RenderRequest(req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGeminiRenderRequest(b *testing.B) {
	req := perfMustParseOpenAI(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := (GeminiCodec{}).RenderRequest(req); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCleanGeminiToolSchema40 cleans all 40 tool schemas per op.
func BenchmarkCleanGeminiToolSchema40(b *testing.B) {
	schemas := make([]json.RawMessage, 40)
	for i := range schemas {
		schemas[i] = json.RawMessage(perfToolSchema(i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, s := range schemas {
			if out := cleanGeminiToolSchema(s); len(out) == 0 {
				b.Fatal("empty schema")
			}
		}
	}
}

// ---- stream path (one op == one full 300-frame stream) -----------------------

func BenchmarkParseStreamLine(b *testing.B) {
	cases := []struct {
		name   string
		codec  StreamCodec
		frames [][]byte
	}{
		{"openai", OpenAICodec{}, perfOpenAIFrames()},
		{"anthropic", AnthropicCodec{}, perfAnthropicFrames()},
		{"gemini", GeminiCodec{}, perfGeminiFrames()},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			var total int64
			for _, f := range tc.frames {
				total += int64(len(f))
			}
			b.SetBytes(total)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for _, f := range tc.frames {
					if _, err := tc.codec.ParseStreamLine(f, "perf-model"); err != nil {
						b.Fatal(err)
					}
				}
			}
			b.ReportMetric(float64(len(tc.frames)), "frames/op")
		})
	}
}

func BenchmarkRenderStreamChunk(b *testing.B) {
	chunks := perfChunks()
	codecs := []struct {
		name  string
		codec StreamCodec
	}{
		{"openai", OpenAICodec{}},
		{"anthropic", AnthropicCodec{}},
		{"gemini", GeminiCodec{}},
	}
	for _, tc := range codecs {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				state := &StreamState{MessageID: "msg_perf", Model: "perf-model", Custom: map[string]any{}}
				for _, ch := range chunks {
					if _, err := tc.codec.RenderStreamChunk(ch, state); err != nil {
						b.Fatal(err)
					}
				}
				tc.codec.RenderStreamDone(state)
			}
			b.ReportMetric(float64(len(chunks)), "chunks/op")
		})
	}
}

func BenchmarkToolArgSanitizerProcess(b *testing.B) {
	chunks := perfChunks()
	emit := func(core.StreamChunk) {}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := NewToolArgSanitizer()
		for _, ch := range chunks {
			s.Process(ch, emit)
		}
		s.Flush(emit)
	}
	b.ReportMetric(float64(len(chunks)), "chunks/op")
}

func BenchmarkStripThinkTags(b *testing.B) {
	content := "<think>" + perfProse(2048) + "</think>" + perfProse(4096)
	b.SetBytes(int64(len(content)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		StripThinkTags(content)
	}
}

func BenchmarkThinkTagState(b *testing.B) {
	plain := make([]string, 300)
	for i := range plain {
		plain[i] = perfTextDelta(i)
	}
	tagged := append([]string{"<thi", "nk>" + perfProse(40)}, plain[:148]...)
	tagged = append(tagged, "</thi", "nk>")
	tagged = append(tagged, plain[150:]...)
	b.Run("no_tags_fast_path", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var ts ThinkTagState
			for _, d := range plain {
				ts.ProcessFeed(d)
			}
			ts.Flush()
		}
	})
	b.Run("with_split_tags", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var ts ThinkTagState
			for _, d := range tagged {
				ts.ProcessFeed(d)
			}
			ts.Flush()
		}
	})
}
