package connectors

// Benchmarks for the shared SSE pump: newSSEPump(...).run over a 300-frame
// OpenAI SSE body with (a) a trivial parse func (isolates scanner + per-frame
// copy + channel send), (b) the real OpenAICodec.ParseStreamLine, and (c) the
// trivial parse with a concurrent consumer goroutine (models the pipeline).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/transform"
)

func perfSSEJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func perfSSEOpenAIBody() []byte {
	words := []string{"refactor", "the", "handler", "so", "that", "every", "request", "is", "validated", "before", "dispatch"}
	const pre = `data: {"id":"chatcmpl-perf","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","system_fingerprint":"fp_perf","choices":[{"index":0,"delta":`
	var b strings.Builder
	b.WriteString(pre + `{"role":"assistant","content":""},"logprobs":null,"finish_reason":null}]}` + "\n\n")
	for i := 0; i < 246; i++ {
		b.WriteString(pre + `{"content":` + perfSSEJSON(words[i%len(words)]+" "+words[(i*7)%len(words)]+" ") + `},"logprobs":null,"finish_reason":null}]}` + "\n\n")
	}
	var code strings.Builder
	for i := 0; code.Len() < 1900; i++ {
		fmt.Fprintf(&code, "\tif err := step%d(ctx); err != nil {\n\t\treturn fmt.Errorf(\"step %d: %%w\", err)\n\t}\n", i, i)
	}
	args := `{"file_path":"/tmp/handler.go","content":` + perfSSEJSON(code.String()) + `}`
	b.WriteString(pre + `{"tool_calls":[{"index":0,"id":"call_perf","type":"function","function":{"name":"Write","arguments":""}}]},"logprobs":null,"finish_reason":null}]}` + "\n\n")
	size := (len(args) + 49) / 50
	for i := 0; i < len(args); i += size {
		end := i + size
		if end > len(args) {
			end = len(args)
		}
		b.WriteString(pre + `{"tool_calls":[{"index":0,"function":{"arguments":` + perfSSEJSON(args[i:end]) + `}}]},"logprobs":null,"finish_reason":null}]}` + "\n\n")
	}
	b.WriteString(pre + `{},"logprobs":null,"finish_reason":"tool_calls"}]}` + "\n\n")
	b.WriteString(`data: {"id":"chatcmpl-perf","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":31000,"completion_tokens":640,"total_tokens":31640}}` + "\n\n")
	b.WriteString("data: [DONE]\n\n")
	return []byte(b.String())
}

func BenchmarkSSEPumpTrivialParse(b *testing.B) {
	body := perfSSEOpenAIBody()
	one := []core.StreamChunk{{Type: core.ChunkText, Delta: "x"}}
	parse := func(p []byte) ([]core.StreamChunk, error) { return one, nil }
	out := make(chan core.StreamChunk, 2048)
	ctx := context.Background()
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		newSSEPump(ctx, "perf", "gpt-4o", out, core.StreamConfig{}).run(bytes.NewReader(body), sseDataLines, parse)
		for len(out) > 0 {
			<-out
		}
	}
}

func BenchmarkSSEPumpOpenAICodec(b *testing.B) {
	body := perfSSEOpenAIBody()
	codec := transform.OpenAICodec{}
	parse := func(p []byte) ([]core.StreamChunk, error) { return codec.ParseStreamLine(p, "gpt-4o") }
	out := make(chan core.StreamChunk, 2048)
	ctx := context.Background()
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		newSSEPump(ctx, "perf", "gpt-4o", out, core.StreamConfig{}).run(bytes.NewReader(body), sseDataLines, parse)
		for len(out) > 0 {
			<-out
		}
	}
}

// BenchmarkSSEPumpTrivialParseConcurrentDrain uses the pipeline's 16-slot
// channel with a draining goroutine, so channel hand-off cost is included.
func BenchmarkSSEPumpTrivialParseConcurrentDrain(b *testing.B) {
	body := perfSSEOpenAIBody()
	one := []core.StreamChunk{{Type: core.ChunkText, Delta: "x"}}
	parse := func(p []byte) ([]core.StreamChunk, error) { return one, nil }
	ctx := context.Background()
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := make(chan core.StreamChunk, 16)
		done := make(chan struct{})
		go func() {
			for range out {
			}
			close(done)
		}()
		newSSEPump(ctx, "perf", "gpt-4o", out, core.StreamConfig{}).run(bytes.NewReader(body), sseDataLines, parse)
		close(out)
		<-done
	}
}
