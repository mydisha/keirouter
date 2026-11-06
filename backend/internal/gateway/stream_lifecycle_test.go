package gateway

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// failingToolStreamUpstream opens a tool call, then fails mid-arguments with an
// in-band error frame.
func failingToolStreamUpstream() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"save","arguments":"{\"path\":"}}]}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"error":{"message":"backend reset","type":"server_error"}}`+"\n\n")
	}
}

func textStreamUpstream() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"hi"}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
}

const toolDef = `"tools":[{"type":"function","function":{"name":"save","parameters":{"type":"object"}}}]`

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)
	return string(b)
}

func TestStream_FailingMidToolCallEndsAtTheError(t *testing.T) {
	h := newE2E(t, failingToolStreamUpstream())
	cases := []struct {
		path, body, errMark string
		absent              []string
	}{
		{
			"/v1/chat/completions",
			`{"model":"openai/gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}],` + toolDef + `}`,
			`"error"`, []string{"[DONE]", `"finish_reason":"tool_calls"`},
		},
		{
			"/v1/messages",
			`{"model":"openai/gpt-4o","stream":true,"max_tokens":100,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"save","input_schema":{"type":"object"}}]}`,
			"event: error", []string{"message_stop", `"tool_use"`},
		},
		{
			"/v1/responses",
			`{"model":"openai/gpt-4o","stream":true,"input":"hi","tools":[{"type":"function","name":"save","parameters":{"type":"object"}}]}`,
			"event: response.failed", []string{"response.completed", "function_call_arguments.done"},
		},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			resp := h.post(t, c.path, c.body, h.apiKey)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			out := readAll(t, resp)
			require.Contains(t, out, c.errMark)
			for _, a := range c.absent {
				require.NotContains(t, out, a, "a half-streamed call must not look finished")
			}
		})
	}
}

func TestStreamError_BeforeHeadersUsesClientDialect(t *testing.T) {
	h := newE2E(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"bad input","type":"invalid_request_error"}}`)
	})
	resp := h.post(t, "/v1/messages", `{"model":"openai/gpt-4o","stream":true,"max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`, h.apiKey)
	out := readAll(t, resp)
	require.GreaterOrEqual(t, resp.StatusCode, 400)
	require.Contains(t, out, `"type":"error"`, out)
}

func TestStream_IDIsUniquePerRequest(t *testing.T) {
	h := newE2E(t, textStreamUpstream())
	body := `{"model":"openai/gpt-4o","stream":true,"max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`
	idRe := regexp.MustCompile(`"id":"(msg_[0-9a-f]+)"`)
	var ids []string
	for range 2 {
		resp := h.post(t, "/v1/messages", body, h.apiKey)
		require.NotEmpty(t, resp.Header.Get("X-Request-Id"))
		m := idRe.FindStringSubmatch(readAll(t, resp))
		require.Len(t, m, 2)
		ids = append(ids, m[1])
	}
	require.NotEqual(t, ids[0], ids[1])
}
