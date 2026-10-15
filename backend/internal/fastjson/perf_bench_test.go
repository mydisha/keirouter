package fastjson

// Confirms whether sonic is active on this machine by comparing fastjson
// Marshal/Unmarshal against encoding/json on (a) a Claude Code-sized request
// body decoded into a wire struct mirroring transform.oaiRequest, (b) the
// map[string]any SSE-event payload shape the stream renderers build, and
// (c) a small SSE frame decoded into a struct (the ParseStreamLine shape).

import (
	stdjson "encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

type perfMessage struct {
	Role       string          `json:"role"`
	Content    stdjson.RawMessage `json:"content,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  []perfToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type perfToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string             `json:"name"`
		Arguments stdjson.RawMessage `json:"arguments"`
	} `json:"function"`
}

type perfTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string             `json:"name"`
		Description string             `json:"description,omitempty"`
		Parameters  stdjson.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

type perfRequest struct {
	Model       string        `json:"model"`
	Messages    []perfMessage `json:"messages"`
	Tools       []perfTool    `json:"tools,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

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

func perfRequestBody() []byte {
	type m = map[string]any
	d := perfProse(60)
	schema := fmt.Sprintf(`{"type":"object","properties":{"file_path":{"type":"string","description":%q},"offset":{"type":"integer","description":%q},"limit":{"type":"integer","description":%q},"pattern":{"type":"string","description":%q},"mode":{"type":"string","enum":["fast","full"],"description":%q},"options":{"type":"object","properties":{"recursive":{"type":"boolean","description":%q},"depth":{"type":"integer","description":%q}}},"paths":{"type":"array","items":{"type":"string"},"description":%q}},"required":["file_path"]}`, d, d, d, d, d, d, d, d)
	code, _ := stdjson.Marshal(perfCode(1900))
	msgs := []any{m{"role": "system", "content": perfProse(6 * 1024)}, m{"role": "user", "content": perfProse(300)}}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("call_%03d", i)
		args := fmt.Sprintf(`{"file_path":"/home/user/project/pkg_%d/handler.go","content":%s}`, i, code)
		msgs = append(msgs,
			m{"role": "assistant", "content": nil, "tool_calls": []any{m{"id": id, "type": "function", "function": m{"name": "Write", "arguments": args}}}},
			m{"role": "tool", "tool_call_id": id, "content": "     1\t" + perfCode(1900)})
		if i%2 == 1 {
			msgs = append(msgs, m{"role": "user", "content": perfProse(120)}, m{"role": "assistant", "content": perfProse(150)})
		}
	}
	tools := make([]any, 0, 40)
	for i := 0; i < 40; i++ {
		tools = append(tools, m{"type": "function", "function": m{"name": fmt.Sprintf("tool_%d", i), "description": perfProse(80), "parameters": stdjson.RawMessage(schema)}})
	}
	body, err := stdjson.Marshal(m{"model": "gpt-4o", "stream": true, "max_tokens": 8192, "messages": msgs, "tools": tools})
	if err != nil {
		panic(err)
	}
	return body
}

func perfSSEEventMap() map[string]any {
	return map[string]any{
		"id":     "chatcmpl-perf",
		"object": "chat.completion.chunk",
		"model":  "gpt-4o",
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         map[string]any{"content": "validated before "},
			"finish_reason": nil,
		}},
	}
}

type perfSSEChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string             `json:"name"`
					Arguments stdjson.RawMessage `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

const perfSSEFrame = `{"id":"chatcmpl-perf","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","system_fingerprint":"fp_perf","choices":[{"index":0,"delta":{"content":"validated before "},"logprobs":null,"finish_reason":null}]}`

func TestPerfRuntimeInfo(t *testing.T) {
	t.Logf("go=%s arch=%s (sonic v1.15.4 build tag: amd64 && go1.17 && !go1.28)", runtime.Version(), runtime.GOARCH)
}

func BenchmarkUnmarshalRequest(b *testing.B) {
	body := perfRequestBody()
	b.Run("fastjson", func(b *testing.B) {
		b.SetBytes(int64(len(body)))
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var r perfRequest
			if err := Unmarshal(body, &r); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("encoding_json", func(b *testing.B) {
		b.SetBytes(int64(len(body)))
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var r perfRequest
			if err := stdjson.Unmarshal(body, &r); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkMarshalRequest(b *testing.B) {
	var r perfRequest
	if err := stdjson.Unmarshal(perfRequestBody(), &r); err != nil {
		b.Fatal(err)
	}
	b.Run("fastjson", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			out, err := Marshal(&r)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(out)))
		}
	})
	b.Run("encoding_json", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			out, err := stdjson.Marshal(&r)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(out)))
		}
	})
}

func BenchmarkMarshalSSEEventMap(b *testing.B) {
	b.Run("fastjson", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := Marshal(perfSSEEventMap()); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("encoding_json", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := stdjson.Marshal(perfSSEEventMap()); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkUnmarshalSSEFrame(b *testing.B) {
	frame := []byte(perfSSEFrame)
	b.Run("fastjson", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var c perfSSEChunk
			if err := Unmarshal(frame, &c); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("encoding_json", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var c perfSSEChunk
			if err := stdjson.Unmarshal(frame, &c); err != nil {
				b.Fatal(err)
			}
		}
	})
}
