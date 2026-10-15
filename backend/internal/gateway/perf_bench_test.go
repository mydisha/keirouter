package gateway

// Benchmarks for the direct (zero-copy) stream path: copySanitizedStream /
// writeSanitizedFrame over a 300-frame SSE body, the error-marker scan
// (hasStreamErrorMarker + isProviderStreamError), and heartbeatWriter.Write.
// The "error_words" variants model a model writing Go error-handling code,
// where nearly every frame contains the substring "error" or "failed".

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
)

func perfGwWords(errWords bool) []string {
	if errWords {
		return []string{"if", "err", "!=", "nil", "{", "return", "fmt.Errorf(\"handler", "failed:", "%w\",", "err)", "}", "// error path"}
	}
	return []string{"refactor", "the", "handler", "so", "that", "every", "request", "is", "validated", "before", "dispatch"}
}

func perfGwDelta(i int, errWords bool) string {
	w := perfGwWords(errWords)
	return w[i%len(w)] + " " + w[(i*7)%len(w)] + " "
}

func perfGwArgs() string {
	var b strings.Builder
	for i := 0; b.Len() < 1900; i++ {
		fmt.Fprintf(&b, "\tif err := step%d(ctx); err != nil {\n\t\treturn fmt.Errorf(\"step %d failed: %%w\", err)\n\t}\n", i, i)
	}
	c, _ := json.Marshal(b.String())
	return `{"file_path":"/tmp/handler.go","content":` + string(c) + `}`
}

func perfGwJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// perfGwOpenAIBody builds a 300-frame OpenAI SSE body ("data: ...\n\n").
func perfGwOpenAIBody(errWords bool) []byte {
	const pre = `data: {"id":"chatcmpl-perf","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","system_fingerprint":"fp_perf","choices":[{"index":0,"delta":`
	var b strings.Builder
	b.WriteString(pre + `{"role":"assistant","content":""},"logprobs":null,"finish_reason":null}]}` + "\n\n")
	for i := 0; i < 246; i++ {
		b.WriteString(pre + `{"content":` + perfGwJSON(perfGwDelta(i, errWords)) + `},"logprobs":null,"finish_reason":null}]}` + "\n\n")
	}
	b.WriteString(pre + `{"tool_calls":[{"index":0,"id":"call_perf","type":"function","function":{"name":"Write","arguments":""}}]},"logprobs":null,"finish_reason":null}]}` + "\n\n")
	args := perfGwArgs()
	size := (len(args) + 49) / 50
	for i := 0; i < len(args); i += size {
		end := i + size
		if end > len(args) {
			end = len(args)
		}
		b.WriteString(pre + `{"tool_calls":[{"index":0,"function":{"arguments":` + perfGwJSON(args[i:end]) + `}}]},"logprobs":null,"finish_reason":null}]}` + "\n\n")
	}
	b.WriteString(pre + `{},"logprobs":null,"finish_reason":"tool_calls"}]}` + "\n\n")
	b.WriteString(`data: {"id":"chatcmpl-perf","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":31000,"completion_tokens":640,"total_tokens":31640}}` + "\n\n")
	b.WriteString("data: [DONE]\n\n")
	return []byte(b.String())
}

// perfGwAnthropicBody builds a 300-frame Anthropic SSE body with event names.
func perfGwAnthropicBody(errWords bool) []byte {
	var b strings.Builder
	ev := func(name, data string) { b.WriteString("event: " + name + "\ndata: " + data + "\n\n") }
	ev("message_start", `{"type":"message_start","message":{"id":"msg_perf","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":3000,"cache_read_input_tokens":28000,"output_tokens":1}}}`)
	ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	for i := 0; i < 243; i++ {
		ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":`+perfGwJSON(perfGwDelta(i, errWords))+`}}`)
	}
	ev("content_block_stop", `{"type":"content_block_stop","index":0}`)
	ev("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_perf","name":"Write","input":{}}}`)
	args := perfGwArgs()
	size := (len(args) + 49) / 50
	for i := 0; i < len(args); i += size {
		end := i + size
		if end > len(args) {
			end = len(args)
		}
		ev("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":`+perfGwJSON(args[i:end])+`}}`)
	}
	ev("content_block_stop", `{"type":"content_block_stop","index":1}`)
	ev("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":640}}`)
	ev("message_stop", `{"type":"message_stop"}`)
	return []byte(b.String())
}

func perfGwFrames(body []byte) [][]byte {
	var out [][]byte
	for _, f := range bytes.Split(body, []byte("\n\n")) {
		if len(bytes.TrimSpace(f)) > 0 {
			out = append(out, append(f, '\n', '\n'))
		}
	}
	return out
}

func BenchmarkCopySanitizedStream(b *testing.B) {
	cases := []struct {
		name    string
		dialect core.Dialect
		body    []byte
	}{
		{"openai_clean", core.DialectOpenAI, perfGwOpenAIBody(false)},
		{"openai_error_words", core.DialectOpenAI, perfGwOpenAIBody(true)},
		{"anthropic_clean", core.DialectAnthropic, perfGwAnthropicBody(false)},
		{"anthropic_error_words", core.DialectAnthropic, perfGwAnthropicBody(true)},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.SetBytes(int64(len(tc.body)))
			b.ReportAllocs()
			flushes := 0
			flush := func() { flushes++ }
			for i := 0; i < b.N; i++ {
				n, err := copySanitizedStream(io.Discard, bytes.NewReader(tc.body), tc.dialect, flush)
				if err != nil || n != int64(len(tc.body)) {
					b.Fatalf("n=%d err=%v", n, err)
				}
			}
			b.ReportMetric(float64(flushes)/float64(b.N), "frames/op")
		})
	}
}

func BenchmarkWriteSanitizedFrame(b *testing.B) {
	cases := []struct {
		name  string
		frame []byte
	}{
		{"text_clean", []byte(`data: {"id":"chatcmpl-perf","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"validated before "},"logprobs":null,"finish_reason":null}]}` + "\n\n")},
		{"text_error_word", []byte(`data: {"id":"chatcmpl-perf","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"return fmt.Errorf(\"handler failed: %w\", err)"},"logprobs":null,"finish_reason":null}]}` + "\n\n")},
		{"anthropic_event_error_word", []byte(`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"// error path\n\tif err != nil {"}}` + "\n\n")},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.SetBytes(int64(len(tc.frame)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := writeSanitizedFrame(io.Discard, tc.frame, core.DialectOpenAI, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkIsProviderStreamError(b *testing.B) {
	event := `data: {"id":"chatcmpl-perf","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"return fmt.Errorf(\"handler failed: %w\", err)"},"logprobs":null,"finish_reason":null}]}` + "\n\n"
	b.SetBytes(int64(len(event)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if isProviderStreamError([]byte(event)) {
			b.Fatal("false positive")
		}
	}
}

func BenchmarkHasStreamErrorMarker(b *testing.B) {
	frame := []byte(`data: {"id":"chatcmpl-perf","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"validated before "},"logprobs":null,"finish_reason":null}]}` + "\n\n")
	b.SetBytes(int64(len(frame)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if hasStreamErrorMarker(frame) {
			b.Fatal("false positive")
		}
	}
}

func BenchmarkHeartbeatWriterWrite(b *testing.B) {
	frames := perfGwFrames(perfGwOpenAIBody(false))
	hw := newHeartbeatWriter(io.Discard, func() {}, time.Hour)
	defer hw.stop()
	var total int64
	for _, f := range frames {
		total += int64(len(f))
	}
	b.SetBytes(total)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, f := range frames {
			if _, err := hw.Write(f); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ReportMetric(float64(len(frames)), "frames/op")
}
