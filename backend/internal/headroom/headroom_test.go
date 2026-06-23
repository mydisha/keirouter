package headroom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/stretchr/testify/require"
)

func TestCompressNoopsWhenDisabled(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	req := textRequest("gpt-4o", "long context")
	stats, err := New().Compress(context.Background(), req, Config{Enabled: false, BaseURL: srv.URL})

	require.NoError(t, err)
	require.Nil(t, stats)
	require.False(t, called)
	require.Equal(t, "long context", req.Messages[0].TextContent())
}

func TestCompressCallsProxyAndAppliesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/compress", r.URL.Path)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "gpt-4o", body["model"])
		require.EqualValues(t, 12000, body["tokenBudget"])
		require.EqualValues(t, 12000, body["token_budget"])
		require.Len(t, body["messages"], 2) // system + user

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"messages":[
				{"role":"system","content":"compressed system"},
				{"role":"user","content":"short context"}
			],
			"tokens_before":100,
			"tokens_after":25,
			"tokens_saved":75,
			"compression_ratio":0.25,
			"transforms_applied":["smart-crusher"],
			"ccr_hashes":["abc123"]
		}`))
	}))
	defer srv.Close()

	req := textRequest("gpt-4o", "long context")
	req.System = "verbose system"
	stats, err := New().Compress(context.Background(), req, Config{
		Enabled:     true,
		BaseURL:     srv.URL + "/",
		APIKey:      "test-key",
		Timeout:     time.Second,
		TokenBudget: 12000,
	})

	require.NoError(t, err)
	require.NotNil(t, stats)
	require.Equal(t, 100, stats.TokensBefore)
	require.Equal(t, 25, stats.TokensAfter)
	require.Equal(t, 75, stats.TokensSaved)
	require.Equal(t, []string{"smart-crusher"}, stats.Transforms)
	require.Equal(t, []string{"abc123"}, stats.CCRHashes)
	require.Equal(t, "compressed system", req.System)
	require.Len(t, req.Messages, 1)
	require.Equal(t, "short context", req.Messages[0].TextContent())
}

func TestCompressDerivesTokensSavedWhenMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"messages":[{"role":"user","content":"short"}],
			"tokens_before":50,
			"tokens_after":20
		}`))
	}))
	defer srv.Close()

	req := textRequest("gpt-4o", "long")
	stats, err := New().Compress(context.Background(), req, Config{Enabled: true, BaseURL: srv.URL})

	require.NoError(t, err)
	require.NotNil(t, stats)
	require.Equal(t, 30, stats.TokensSaved)
}

func TestCompressReturnsErrorWithoutMutatingOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "proxy unavailable", http.StatusBadGateway)
	}))
	defer srv.Close()

	req := textRequest("gpt-4o", "original context")
	stats, err := New().Compress(context.Background(), req, Config{Enabled: true, BaseURL: srv.URL})

	require.Error(t, err)
	require.Nil(t, stats)
	require.Equal(t, "original context", req.Messages[0].TextContent())
}

func TestCompressSkipsUnsupportedMultimodalParts(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	req := &core.ChatRequest{
		Model: "gpt-4o",
		Messages: []core.Message{{
			Role: core.RoleUser,
			Content: []core.ContentPart{
				{Type: core.PartText, Text: "describe this"},
				{Type: core.PartImage, Media: &core.MediaPayload{URL: "https://example.com/image.png"}},
			},
		}},
	}
	stats, err := New().Compress(context.Background(), req, Config{Enabled: true, BaseURL: srv.URL})

	require.NoError(t, err)
	require.Nil(t, stats)
	require.False(t, called)
}

func textRequest(model, text string) *core.ChatRequest {
	return &core.ChatRequest{
		Model: model,
		Messages: []core.Message{{
			Role:    core.RoleUser,
			Content: []core.ContentPart{{Type: core.PartText, Text: text}},
		}},
	}
}
