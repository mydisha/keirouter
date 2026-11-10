package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// sseData returns the data of each SSE event in body.
func sseData(t *testing.T, body string) []string {
	t.Helper()
	var out []string
	for _, ev := range strings.Split(body, "\n\n") {
		if strings.TrimSpace(ev) == "" {
			continue
		}
		var data []string
		for _, line := range strings.Split(ev, "\n") {
			if v, ok := strings.CutPrefix(line, "data: "); ok {
				data = append(data, v)
			}
		}
		require.Len(t, data, 1, "one single-line data field per event: %q", ev)
		require.True(t, json.Valid([]byte(data[0])), data[0])
		out = append(out, data[0])
	}
	return out
}

// progressCall builds a tools/call carrying a progress token.
func progressCall(t *testing.T, modern bool, token any, accept string) *http.Request {
	t.Helper()
	meta := map[string]any{"progressToken": token}
	if modern {
		meta[MetaProtocolVersion] = VersionModern
		meta[MetaClientInfo] = map[string]string{"name": "test-client", "version": "1"}
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 5, "method": "tools/call",
		"params": map[string]any{"name": "wx__get_weather", "arguments": map[string]any{"city": "Bandung"}, "_meta": meta}})
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(body)))
	if modern {
		r.Header.Set(HeaderProtocolVersion, VersionModern)
		r.Header.Set(HeaderMethod, "tools/call")
		r.Header.Set(HeaderName, "wx__get_weather")
	}
	if accept != "" {
		r.Header.Set("Accept", accept)
	}
	return r
}

func TestServe_RelaysProgressForTheClientToken(t *testing.T) {
	for _, modern := range []bool{true, false} {
		name := "legacy"
		if modern {
			name = "modern"
		}
		t.Run(name, func(t *testing.T) {
			e := newServeEnv(t, &fakeServer{modern: true, legacy: true, sse: true, tools: []string{toolWeather}})
			w := httptest.NewRecorder()
			out := e.m.Serve(w, progressCall(t, modern, "tok-1", "application/json, text/event-stream"), Caller{KeyID: e.keyID}, "")
			require.Equal(t, http.StatusOK, w.Code)
			require.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
			require.Equal(t, 1, out.Progress)
			require.Equal(t, StatusSuccess, out.CallStatus)

			events := sseData(t, w.Body.String())
			require.Len(t, events, 2, "only the progress for tok-1, then the response: %s", w.Body.String())
			require.JSONEq(t, `{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"tok-1","progress":1,"total":2}}`, events[0])
			var final struct {
				ID     json.RawMessage `json:"id"`
				Result json.RawMessage `json:"result"`
			}
			require.NoError(t, json.Unmarshal([]byte(events[1]), &final))
			require.JSONEq(t, `5`, string(final.ID), "the client's id, not the upstream one")
			require.Contains(t, string(final.Result), `"text":"ok"`)

			calls := e.upstreamCalls()
			require.Contains(t, string(calls[len(calls)-1].Body.Params), `"progressToken":"tok-1"`, "the token reaches the upstream")
		})
	}
}

func TestServe_ProgressTokenMatchesByValue(t *testing.T) {
	e := newServeEnv(t, &fakeServer{legacy: true, sse: true, tools: []string{toolWeather}})
	w := httptest.NewRecorder()
	out := e.m.Serve(w, progressCall(t, false, 42, "text/event-stream;q=0.9, application/json"), Caller{KeyID: e.keyID}, "")
	require.Equal(t, 1, out.Progress)
	require.Contains(t, sseData(t, w.Body.String())[0], `"progressToken":42`)
}

func TestServe_PlainJSONWithoutTokenOrSSE(t *testing.T) {
	e := newServeEnv(t, &fakeServer{legacy: true, sse: true, tools: []string{toolWeather}})
	cases := map[string]*http.Request{
		"no SSE in Accept": progressCall(t, false, "tok-1", "application/json"),
		"no Accept":        progressCall(t, false, "tok-1", ""),
		"no token":         legacyReq(5, "tools/call", map[string]any{"name": "wx__get_weather"}),
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			out := e.m.Serve(w, r, Caller{KeyID: e.keyID}, "")
			require.Equal(t, "application/json", w.Header().Get("Content-Type"))
			require.Zero(t, out.Progress)
			require.True(t, json.Valid(w.Body.Bytes()), w.Body.String())
		})
	}
}

func TestServe_NoProgressStaysJSON(t *testing.T) {
	// The upstream answers with plain JSON, so nothing is relayed and the
	// client gets a JSON response even though it accepts SSE.
	e := newServeEnv(t, &fakeServer{legacy: true, tools: []string{toolWeather}})
	w := httptest.NewRecorder()
	out := e.m.Serve(w, progressCall(t, false, "tok-1", "application/json, text/event-stream"), Caller{KeyID: e.keyID}, "")
	require.Equal(t, "application/json", w.Header().Get("Content-Type"))
	require.Zero(t, out.Progress)
}

func TestServe_UpstreamErrorAfterProgressIsStreamed(t *testing.T) {
	e := newServeEnv(t, &fakeServer{legacy: true, sse: true, tools: []string{toolWeather, toolExplode}})
	body := `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"wx__explode","_meta":{"progressToken":"t"}}}`
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	r.Header.Set("Accept", "application/json, text/event-stream")
	w := httptest.NewRecorder()
	out := e.m.Serve(w, r, Caller{KeyID: e.keyID}, "")
	require.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
	require.Equal(t, StatusUpstreamError, out.CallStatus)
	events := sseData(t, w.Body.String())
	require.Len(t, events, 2)
	require.JSONEq(t, `{"jsonrpc":"2.0","id":8,"error":{"code":-32602,"message":"bad arguments for explode"}}`, events[1],
		"an upstream error after progress ends the stream with the client's id")
}

func TestAcceptsSSE(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want bool
	}{
		{[]string{"application/json, text/event-stream"}, true},
		{[]string{"application/json", "Text/Event-Stream; q=0.5"}, true},
		{[]string{"text/event-streamx"}, false},
		{[]string{"application/json"}, false},
		{nil, false},
		{[]string{""}, false},
	} {
		require.Equal(t, c.want, acceptsSSE(c.in), "%q", c.in)
	}
	require.Zero(t, testing.AllocsPerRun(100, func() { acceptsSSE([]string{"application/json, text/event-stream"}) }))
}
