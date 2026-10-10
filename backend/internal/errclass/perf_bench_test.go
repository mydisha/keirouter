package errclass

// Cost of StreamErrorFrame on the ordinary (non-error) frames of a 300-frame
// stream: text deltas, tool argument fragments, and the worst case where tool
// arguments legitimately contain the key "error" (forcing the full decode).

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func perfErrWords() []string {
	return []string{"refactor", "the", "handler", "so", "that", "every", "request", "is", "validated", "before", "dispatch"}
}

func perfErrOpenAIFrames(argsWithErrorKey bool) [][]byte {
	const pre = `{"id":"chatcmpl-perf","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":`
	w := perfErrWords()
	frames := make([][]byte, 0, 300)
	for i := 0; i < 248; i++ {
		t, _ := json.Marshal(w[i%len(w)] + " " + w[(i*7)%len(w)] + " ")
		frames = append(frames, []byte(pre+`{"content":`+string(t)+`},"finish_reason":null}]}`))
	}
	// One tool call split into 50 fragments of a ~2 KB args object.
	var sb strings.Builder
	for i := 0; sb.Len() < 1900; i++ {
		if argsWithErrorKey {
			fmt.Fprintf(&sb, "{\"error\":\"step %d failed\",\"retry\":%d},", i, i)
		} else {
			fmt.Fprintf(&sb, "{\"step\":\"step %d\",\"retry\":%d},", i, i)
		}
	}
	args := `{"file_path":"/tmp/x.go","items":[` + strings.TrimSuffix(sb.String(), ",") + `]}`
	size := (len(args) + 49) / 50
	for i := 0; i < len(args); i += size {
		end := i + size
		if end > len(args) {
			end = len(args)
		}
		f, _ := json.Marshal(args[i:end])
		frames = append(frames, []byte(pre+`{"tool_calls":[{"index":0,"function":{"arguments":`+string(f)+`}}]},"finish_reason":null}]}`))
	}
	frames = append(frames, []byte(pre+`{},"finish_reason":"tool_calls"}]}`))
	frames = append(frames, []byte(`{"id":"chatcmpl-perf","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":31000,"completion_tokens":640,"total_tokens":31640}}`))
	return frames
}

func perfErrAnthropicFrames() [][]byte {
	w := perfErrWords()
	frames := make([][]byte, 0, 300)
	frames = append(frames, []byte(`{"type":"message_start","message":{"id":"msg_perf","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"stop_reason":null,"usage":{"input_tokens":3000,"output_tokens":1}}}`))
	for i := 0; i < 297; i++ {
		t, _ := json.Marshal(w[i%len(w)] + " " + w[(i*7)%len(w)] + " ")
		frames = append(frames, []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":`+string(t)+`}}`))
	}
	frames = append(frames, []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":640}}`))
	frames = append(frames, []byte(`{"type":"message_stop"}`))
	return frames
}

func BenchmarkStreamErrorFrame(b *testing.B) {
	cases := []struct {
		name   string
		frames [][]byte
	}{
		{"openai_300_clean", perfErrOpenAIFrames(false)},
		{"openai_300_args_contain_error_key", perfErrOpenAIFrames(true)},
		{"anthropic_300_clean", perfErrAnthropicFrames()},
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
			hits := 0
			for i := 0; i < b.N; i++ {
				for _, f := range tc.frames {
					if _, ok := StreamErrorFrame(f); ok {
						hits++
					}
				}
			}
			if hits != 0 {
				b.Fatalf("unexpected error classification on clean frames: %d", hits)
			}
			b.ReportMetric(float64(len(tc.frames)), "frames/op")
		})
	}
}
