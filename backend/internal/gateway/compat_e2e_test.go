package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// Client-compatibility regressions found by driving coding-agent CLIs
// against a Cloudflare Workers AI deployment.

func TestE2E_UnconfiguredModelIsNotFoundInEveryDialect(t *testing.T) {
	h := newE2E(t, openAIUpstream())
	for _, model := range []string{"nope/nonexistent", "mistral/mistral-large"} {
		for _, tc := range []struct {
			path, body, errType string
		}{
			{"/v1/chat/completions", `{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, "invalid_request_error"},
			{"/v1/chat/completions", `{"model":%q,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, "invalid_request_error"},
			{"/v1/messages", `{"model":%q,"max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`, "not_found_error"},
			{"/v1/responses", `{"model":%q,"input":"hi"}`, "invalid_request_error"},
		} {
			resp := h.post(t, tc.path, fmt.Sprintf(tc.body, model), h.apiKey)
			raw, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			require.NoError(t, err)
			require.Equal(t, http.StatusNotFound, resp.StatusCode, "%s %s: %s", tc.path, model, raw)
			require.NotContains(t, string(raw), "internal error", "%s %s", tc.path, model)
			if tc.path == "/v1/messages" {
				require.Contains(t, string(raw), tc.errType)
			}
		}
	}
}

// cloudflareToolStream replays the frame shape Cloudflare's vLLM upstream sends
// for a named tool_choice: usage on every frame (interim prompt_tokens 0), the
// tool call, then finish_reason "stop" repeated on trailing frames.
func cloudflareToolStream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flush, _ := w.(http.Flusher)
		for _, l := range []string{
			`data: {"id":"u1","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"use the tool"},"finish_reason":null}],"usage":{"prompt_tokens":0,"completion_tokens":4,"total_tokens":4}}`,
			`data: {"id":"u1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"chatcmpl-tool-1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}]},"finish_reason":null}],"usage":{"prompt_tokens":0,"completion_tokens":9,"total_tokens":9}}`,
			`data: {"id":"u1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
			`data: {"id":"u1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`data: {"id":"u1","choices":[],"usage":{"prompt_tokens":291,"completion_tokens":61,"total_tokens":352,"prompt_tokens_details":{"cached_tokens":192}}}`,
			`data: [DONE]`,
		} {
			fmt.Fprintf(w, "%s\n\n", l)
			if flush != nil {
				flush.Flush()
			}
		}
	}
}

const forcedToolBody = `{"model":"openai/gpt-4o","stream":true,"stream_options":{"include_usage":true},` +
	`"messages":[{"role":"user","content":"hello"}],` +
	`"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}],` +
	`"tool_choice":{"type":"function","function":{"name":"get_weather"}}}`

func TestE2E_OpenAIStream_CloudflareToolTurnIsWellFormed(t *testing.T) {
	h := newE2E(t, cloudflareToolStream())
	resp := h.post(t, "/v1/chat/completions", forcedToolBody, h.apiKey)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var finishes []string
	var usageEvents, events int
	var created int64
	var lastData string
	for line := range strings.SplitSeq(string(raw), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		lastData = data
		events++
		var ev struct {
			Created int64 `json:"created"`
			Choices []struct {
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage map[string]any `json:"usage"`
		}
		require.NoError(t, json.Unmarshal([]byte(data), &ev), data)
		require.Positive(t, ev.Created, "chat.completion.chunk requires created: %s", data)
		if created == 0 {
			created = ev.Created
		}
		require.Equal(t, created, ev.Created)
		for _, c := range ev.Choices {
			if c.FinishReason != nil {
				finishes = append(finishes, *c.FinishReason)
			}
		}
		if ev.Usage != nil && len(ev.Choices) == 0 {
			usageEvents++
		}
	}
	require.Equal(t, []string{"tool_calls"}, finishes, "exactly one finish, and it must say tool_calls")
	require.Equal(t, 1, usageEvents, "one usage event, not one per upstream frame (%d events)", events)
	require.Contains(t, lastData, `"prompt_tokens":291`)
	require.Contains(t, lastData, `"cached_tokens":192`)

	// The metered row must keep the cached split the client was shown, or
	// cached input is billed at the full input rate.
	require.Eventually(t, func() bool {
		recs, err := h.db.Usage().Recent(context.Background(), store.DefaultTenantID, 5)
		return err == nil && len(recs) > 0 && recs[0].PromptTokens == 291
	}, 3*time.Second, 20*time.Millisecond)
	recs, err := h.db.Usage().Recent(context.Background(), store.DefaultTenantID, 5)
	require.NoError(t, err)
	require.Equal(t, 192, recs[0].CachedTokens)
}

func TestE2E_AnthropicStream_CloudflareToolTurnStopsForToolUse(t *testing.T) {
	h := newE2E(t, cloudflareToolStream())
	body := `{"model":"openai/gpt-4o","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hello"}],` +
		`"tools":[{"name":"get_weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}}],` +
		`"tool_choice":{"type":"tool","name":"get_weather"}}`
	resp := h.post(t, "/v1/messages", body, h.apiKey)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	out := string(raw)
	require.Equal(t, 1, strings.Count(out, "event: message_delta"))
	require.Contains(t, out, `"stop_reason":"tool_use"`)
}

// unaryToolStopUpstream answers a named tool_choice the way vLLM does: the
// message carries tool_calls but finish_reason is "stop".
func unaryToolStopUpstream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"u2","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":null,`+
			`"tool_calls":[{"id":"chatcmpl-tool-2","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}]},`+
			`"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":8,"total_tokens":28}}`)
	}
}

func TestE2E_Unary_ToolCallWithStopFinishIsNormalized(t *testing.T) {
	h := newE2E(t, unaryToolStopUpstream())
	tools := `"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]`

	resp := h.post(t, "/v1/chat/completions", `{"model":"openai/gpt-4o","messages":[{"role":"user","content":"hi"}],`+tools+`}`, h.apiKey)
	var oa struct {
		Created int64 `json:"created"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				ToolCalls []json.RawMessage `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&oa))
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Positive(t, oa.Created, "chat.completion requires created")
	require.Len(t, oa.Choices[0].Message.ToolCalls, 1)
	require.Equal(t, "tool_calls", oa.Choices[0].FinishReason)

	resp = h.post(t, "/v1/messages", `{"model":"openai/gpt-4o","max_tokens":100,"messages":[{"role":"user","content":"hi"}],`+
		`"tools":[{"name":"get_weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}}]}`, h.apiKey)
	var an struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
		} `json:"content"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&an))
	resp.Body.Close()
	require.Equal(t, "tool_use", an.StopReason)
}
