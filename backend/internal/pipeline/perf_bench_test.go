package pipeline

// Benchmarks for the direct-stream usage capture helpers on a ~300 KB captured
// SSE stream, estimateStreamUsage on a Claude Code-sized request, and
// safeBuffer.Write with 4 KB writes over 2 MB.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
)

func perfProse(n int) string {
	words := []string{"refactor", "the", "handler", "so", "that", "every", "request", "is", "validated", "before", "dispatch"}
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		b.WriteString(words[i%len(words)])
		b.WriteByte(' ')
	}
	return b.String()[:n]
}

func perfCode(n int) string {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "\tif err := step%d(ctx, \"arg-%d\"); err != nil {\n\t\treturn fmt.Errorf(\"step %d: %%w\", err)\n\t}\n", i, i, i)
	}
	return b.String()[:n]
}

// perfOpenAIStream builds an OpenAI SSE body of at least minBytes with text
// deltas, one 50-fragment tool call, finish and usage frames.
func perfOpenAIStream(minBytes int) []byte {
	const pre = `data: {"id":"chatcmpl-perf","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","system_fingerprint":"fp_perf","choices":[{"index":0,"delta":`
	var b strings.Builder
	for i := 0; b.Len() < minBytes-12*1024; i++ {
		t, _ := json.Marshal(perfProse(20 + i%40))
		b.WriteString(pre + `{"content":` + string(t) + `},"logprobs":null,"finish_reason":null}]}` + "\n\n")
	}
	args := fmt.Sprintf(`{"file_path":"/tmp/handler.go","content":%s}`, mustJSON(perfCode(1900)))
	size := (len(args) + 49) / 50
	b.WriteString(pre + `{"tool_calls":[{"index":0,"id":"call_perf","type":"function","function":{"name":"Write","arguments":""}}]},"finish_reason":null}]}` + "\n\n")
	for i := 0; i < len(args); i += size {
		end := i + size
		if end > len(args) {
			end = len(args)
		}
		b.WriteString(pre + `{"tool_calls":[{"index":0,"function":{"arguments":` + mustJSON(args[i:end]) + `}}]},"finish_reason":null}]}` + "\n\n")
	}
	b.WriteString(pre + `{},"finish_reason":"tool_calls"}]}` + "\n\n")
	b.WriteString(`data: {"id":"chatcmpl-perf","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":31000,"completion_tokens":640,"total_tokens":31640,"prompt_tokens_details":{"cached_tokens":28000}}}` + "\n\n")
	b.WriteString("data: [DONE]\n\n")
	return []byte(b.String())
}

func perfAnthropicStream(minBytes int) []byte {
	var b strings.Builder
	ev := func(name, data string) {
		b.WriteString("event: " + name + "\ndata: " + data + "\n\n")
	}
	ev("message_start", `{"type":"message_start","message":{"id":"msg_perf","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":3000,"cache_creation_input_tokens":0,"cache_read_input_tokens":28000,"output_tokens":1}}}`)
	ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	for i := 0; b.Len() < minBytes-12*1024; i++ {
		ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":`+mustJSON(perfProse(20+i%40))+`}}`)
	}
	ev("content_block_stop", `{"type":"content_block_stop","index":0}`)
	ev("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_perf","name":"Write","input":{}}}`)
	args := fmt.Sprintf(`{"file_path":"/tmp/handler.go","content":%s}`, mustJSON(perfCode(1900)))
	size := (len(args) + 49) / 50
	for i := 0; i < len(args); i += size {
		end := i + size
		if end > len(args) {
			end = len(args)
		}
		ev("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":`+mustJSON(args[i:end])+`}}`)
	}
	ev("content_block_stop", `{"type":"content_block_stop","index":1}`)
	ev("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":640}}`)
	ev("message_stop", `{"type":"message_stop"}`)
	return []byte(b.String())
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func perfChatRequest() *core.ChatRequest {
	req := &core.ChatRequest{Model: "gpt-4o", System: perfProse(6 * 1024)}
	for i := 0; i < 40; i++ {
		req.Tools = append(req.Tools, core.Tool{Name: fmt.Sprintf("tool_%d", i), Description: perfProse(80),
			Parameters: json.RawMessage(`{"type":"object","properties":{"a":{"type":"string","description":"` + perfProse(900) + `"}}}`)})
	}
	req.Messages = append(req.Messages, core.Message{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: perfProse(300)}}})
	args := json.RawMessage(fmt.Sprintf(`{"file_path":"/tmp/x.go","content":%s}`, mustJSON(perfCode(1900))))
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("call_%03d", i)
		req.Messages = append(req.Messages,
			core.Message{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: id, Name: "Write", Arguments: args}}}},
			core.Message{Role: core.RoleTool, Content: []core.ContentPart{{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: id, Content: perfCode(1900)}}}})
		if i%2 == 1 {
			req.Messages = append(req.Messages,
				core.Message{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: perfProse(120)}}},
				core.Message{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartText, Text: perfProse(150)}}})
		}
	}
	return req
}

func BenchmarkExtractUsageFromStream(b *testing.B) {
	for _, tc := range []struct {
		name string
		raw  []byte
	}{{"openai_300KB", perfOpenAIStream(300 * 1024)}, {"anthropic_300KB", perfAnthropicStream(300 * 1024)}} {
		b.Run(tc.name, func(b *testing.B) {
			b.SetBytes(int64(len(tc.raw)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if u := extractUsageFromStream(tc.raw); u.PromptTokens == 0 {
					b.Fatal("usage not found")
				}
			}
		})
	}
}

func BenchmarkCompletionCharsFromStream(b *testing.B) {
	for _, tc := range []struct {
		name string
		raw  []byte
	}{{"openai_300KB", perfOpenAIStream(300 * 1024)}, {"anthropic_300KB", perfAnthropicStream(300 * 1024)}} {
		b.Run(tc.name, func(b *testing.B) {
			b.SetBytes(int64(len(tc.raw)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if n := completionCharsFromStream(tc.raw); n == 0 {
					b.Fatal("no chars counted")
				}
			}
		})
	}
}

func BenchmarkEstimateStreamUsage(b *testing.B) {
	req := perfChatRequest()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if u := estimateStreamUsage(req, 120_000); u.PromptTokens == 0 {
			b.Fatal("zero")
		}
	}
}

// BenchmarkSafeBufferWrite streams 2 MB through safeBuffer in 4 KB writes
// (512 writes per op), exercising the head capture and the amortized tail trim.
func BenchmarkSafeBufferWrite(b *testing.B) {
	chunk := []byte(perfProse(4096))
	const total = 2 << 20
	b.SetBytes(total)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var sb safeBuffer
		for n := 0; n < total; n += len(chunk) {
			if _, err := sb.Write(chunk); err != nil {
				b.Fatal(err)
			}
		}
		if len(sb.Bytes()) == 0 {
			b.Fatal("empty")
		}
	}
}
