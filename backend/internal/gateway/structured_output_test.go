package gateway

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStructuredOutput_ResponseFormatReachesTheProvider(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	h := newE2E(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, string(b))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"up1","choices":[{"message":{"role":"assistant","content":"{\"n\":1}"},"finish_reason":"stop"}]}`)
	})
	format := `{"type":"json_schema","json_schema":{"name":"x","schema":{"type":"object","properties":{"n":{"type":"integer"}}}}}`
	cases := map[string]string{
		"/v1/chat/completions": `{"model":"openai/gpt-4o","messages":[{"role":"user","content":"hi"}],"response_format":` + format + `}`,
		"/v1/responses":        `{"model":"openai/gpt-4o","input":"hi","text":{"format":{"type":"json_schema","name":"x","schema":{"type":"object","properties":{"n":{"type":"integer"}}}}}}`,
	}
	for path, body := range cases {
		resp := h.post(t, path, body, h.apiKey)
		out := readAll(t, resp)
		require.Equal(t, http.StatusOK, resp.StatusCode, out)
	}
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, seen, 2)
	for _, b := range seen {
		require.Contains(t, b, `"response_format":{"type":"json_schema"`)
		require.Contains(t, b, `"properties":{"n":{"type":"integer"}}`)
	}
}
