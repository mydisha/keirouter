package gateway

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthErrors_UseTheClientDialect(t *testing.T) {
	h := newE2E(t, openAIUpstream())
	cases := map[string]string{
		"/v1/chat/completions":                `"error":{"message":"invalid API key","type":"authentication_error"}`,
		"/v1/messages":                        `"type":"error"`,
		"/v1beta/models/x:generateContent":    `"status":"UNAUTHENTICATED"`,
		"/v1/responses":                       `"error":{"message":"invalid API key"`,
		"/v1/messages/count_tokens":           `"type":"authentication_error"`,
		"/v1beta/models/openai/x:countTokens": `"code":401`,
	}
	for path, want := range cases {
		resp := h.post(t, path, `{}`, "kr_wrong")
		out := readAll(t, resp)
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode, path)
		require.Contains(t, out, want, path)
	}
}

func TestAuth_BearerIsCaseInsensitive(t *testing.T) {
	h := newE2E(t, openAIUpstream())
	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"openai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "bearer "+h.apiKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	readAll(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestGemini_AcceptsGoogAPIKey(t *testing.T) {
	h := newE2E(t, openAIUpstream())
	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/v1beta/models/openai/gpt-4o:generateContent", strings.NewReader(geminiBody))
	require.NoError(t, err)
	req.Header.Set("x-goog-api-key", h.apiKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	readAll(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp = h.post(t, "/v1beta/models/openai/gpt-4o:generateContent?key="+h.apiKey, geminiBody, "")
	readAll(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp = h.post(t, "/v1/chat/completions?key="+h.apiKey, `{"model":"openai/gpt-4o","messages":[]}`, "")
	readAll(t, resp)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "?key= is only for the Gemini routes")
}

func TestGemini_CountTokensDoesNotCallUpstream(t *testing.T) {
	var calls atomic.Int32
	h := newE2E(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		openAIUpstream()(w, r)
	})
	resp := h.post(t, "/v1beta/models/openai/gpt-4o:countTokens", geminiBody, h.apiKey)
	out := readAll(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode, out)
	require.Contains(t, out, `"totalTokens":`)
	require.Zero(t, calls.Load())
}

func TestUpstreamCredentialRejection_IsNotTheClientsAuthError(t *testing.T) {
	h := newE2E(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"invalid x-api-key","type":"authentication_error"}}`)
	})
	resp := h.post(t, "/v1/messages", `{"model":"openai/gpt-4o","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`, h.apiKey)
	out := readAll(t, resp)
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, out)
	require.NotContains(t, out, "authentication_error")
}

func TestResponses_PreviousResponseIDIsRefused(t *testing.T) {
	h := newE2E(t, openAIUpstream())
	resp := h.post(t, "/v1/responses", `{"model":"openai/gpt-4o","input":"hi","previous_response_id":"resp_1"}`, h.apiKey)
	out := readAll(t, resp)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Contains(t, out, "previous_response_id")
}
