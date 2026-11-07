package gateway

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// streamThinkTags streams reasoning inline in content, with the tags split
// across events the way QwQ and MiniMax M2 hosts send them.
func streamThinkTags(deltas ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flush, _ := w.(http.Flusher)
		for _, d := range deltas {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", d)
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		if flush != nil {
			flush.Flush()
		}
	}
}

func TestE2E_StreamedThinkTagsReachClientAsThinking(t *testing.T) {
	cases := []struct {
		name, path, body string
		thinking, text   []string
	}{
		{
			name: "anthropic", path: "/v1/messages",
			body:     `{"model":"openai/gpt-4o","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi"}]}`,
			thinking: []string{`"type":"thinking"`, `"thinking":"plan it"`},
			text:     []string{`"text":"Answer"`},
		},
		{
			name: "openai", path: "/v1/chat/completions",
			body:     `{"model":"openai/gpt-4o","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`,
			thinking: []string{`"reasoning_content":"plan it"`},
			text:     []string{`"content":"Answer"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newE2E(t, streamThinkTags("<thi", "nk>plan it</th", "ink>Answer"))
			resp := h.post(t, tc.path, tc.body, h.apiKey)
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)
			raw, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			out := string(raw)

			for _, want := range append(tc.thinking, tc.text...) {
				require.Contains(t, out, want)
			}
			require.NotContains(t, out, "think>")
		})
	}
}

func TestE2E_UnclosedThinkTagFlushesAsThinking(t *testing.T) {
	h := newE2E(t, streamThinkTags("<think>still reasoning"))
	resp := h.post(t, "/v1/messages", `{"model":"openai/gpt-4o","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, h.apiKey)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	out := string(raw)
	require.Contains(t, out, `"thinking":"still reasoning"`)
	require.Equal(t, 1, strings.Count(out, "event: message_stop"))
}

func TestE2E_HeldBackTagTextPrecedesFinish(t *testing.T) {
	h := newE2E(t, streamThinkTags("a <"))
	resp := h.post(t, "/v1/messages", `{"model":"openai/gpt-4o","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, h.apiKey)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	out := string(raw)
	tail := strings.Index(out, `"text":"<"`)
	require.Positive(t, tail, out)
	require.Less(t, tail, strings.Index(out, "event: message_delta"), out)
}
