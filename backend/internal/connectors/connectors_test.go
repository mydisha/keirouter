package connectors

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/stretchr/testify/require"
)

func textReq(model string, stream bool) *core.ChatRequest {
	return &core.ChatRequest{
		Model:  model,
		Stream: stream,
		Messages: []core.Message{
			{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "hello"}}},
		},
	}
}

func TestOpenAICompatible_Chat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/chat/completions", r.URL.Path)
		require.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"c1","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"hi there"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
	}))
	defer srv.Close()

	c := NewOpenAICompatible("openai", srv.URL)
	resp, err := c.Chat(context.Background(), textReq("gpt-4o", false), core.Credentials{APIKey: "sk-test"})
	require.NoError(t, err)
	require.Equal(t, "hi there", resp.Message.TextContent())
	require.Equal(t, core.FinishStop, resp.FinishReason)
	require.Equal(t, 5, resp.Usage.TotalTokens)
}

func TestOpenAICompatible_Stream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flush, _ := w.(http.Flusher)
		lines := []string{
			`data: {"choices":[{"delta":{"role":"assistant","content":"he"}}]}`,
			`data: {"choices":[{"delta":{"content":"llo"}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}
		for _, l := range lines {
			fmt.Fprintf(w, "%s\n\n", l)
			if flush != nil {
				flush.Flush()
			}
		}
	}))
	defer srv.Close()

	c := NewOpenAICompatible("openai", srv.URL)
	ch, err := c.Stream(context.Background(), textReq("gpt-4o", true), core.Credentials{APIKey: "sk-test"}, core.StreamConfig{})
	require.NoError(t, err)

	var text string
	var finished bool
	for chunk := range ch {
		switch chunk.Type {
		case core.ChunkText:
			text += chunk.Delta
		case core.ChunkFinish:
			finished = true
		case core.ChunkError:
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
	}
	require.Equal(t, "hello", text)
	require.True(t, finished)
}

func TestOpenAICompatible_MapsRateLimitError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":"rate limited"}`)
	}))
	defer srv.Close()

	c := NewOpenAICompatible("openai", srv.URL)
	_, err := c.Chat(context.Background(), textReq("gpt-4o", false), core.Credentials{APIKey: "sk-test"})
	require.Error(t, err)

	pe := core.AsProviderError(err)
	require.Equal(t, core.ErrRateLimit, pe.Kind)
	require.Equal(t, 429, pe.StatusCode)
	require.Equal(t, "openai", pe.Provider)
	require.True(t, pe.Fallbackable())
}

func TestHTTPStatusErrorParsesRetryAfterHTTPDate(t *testing.T) {
	retryAt := time.Now().Add(90 * time.Second).UTC().Truncate(time.Second)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{retryAt.Format(http.TimeFormat)}},
	}

	pe := core.AsProviderError(httpStatusError("kiro", "model", resp, []byte("limited")))
	require.Greater(t, pe.RetryAfter, 85*time.Second)
	require.LessOrEqual(t, pe.RetryAfter, 90*time.Second)
}

func TestOpenAICompatible_BadRequestNotFallbackable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"bad model"}`)
	}))
	defer srv.Close()

	c := NewOpenAICompatible("openai", srv.URL)
	_, err := c.Chat(context.Background(), textReq("bogus", false), core.Credentials{APIKey: "k"})
	pe := core.AsProviderError(err)
	require.Equal(t, core.ErrBadRequest, pe.Kind)
	require.False(t, pe.Fallbackable(), "4xx request errors must not trigger fallback")
}

func TestOpenAICompatible_ValidateAcceptsReachedNonAuthProbeError(t *testing.T) {
	var chatProbed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":"models endpoint not supported"}`)
		case "/chat/completions":
			chatProbed = true
			require.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"probe model not found"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewOpenAICompatible("sumopod", srv.URL)
	require.NoError(t, c.Validate(context.Background(), core.Credentials{APIKey: "sk-test"}))
	require.True(t, chatProbed, "validation should fall back to a chat probe")
}

func TestOpenAICompatible_ValidateRejectsAuthError(t *testing.T) {
	var chatProbed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chat/completions" {
			chatProbed = true
		}
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"bad key"}`)
	}))
	defer srv.Close()

	c := NewOpenAICompatible("sumopod", srv.URL)
	err := c.Validate(context.Background(), core.Credentials{APIKey: "bad-key"})
	require.Error(t, err)
	require.False(t, chatProbed, "auth failures should not fall back")
}

func TestOpenAICompatible_ValidateXAIStatusHandling(t *testing.T) {
	t.Run("forbidden is accepted", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/models", r.URL.Path)
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"error":"no credits"}`)
		}))
		defer srv.Close()

		c := NewOpenAICompatible("xai", srv.URL)
		require.NoError(t, c.Validate(context.Background(), core.Credentials{APIKey: "xai-key"}))
	})

	t.Run("bad request is rejected", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/models", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"bad key"}`)
		}))
		defer srv.Close()

		c := NewOpenAICompatible("xai", srv.URL)
		require.Error(t, c.Validate(context.Background(), core.Credentials{APIKey: "bad-key"}))
	})
}

func TestAzureOpenAI_ChatUsesDeploymentURLAndAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/openai/deployments/prod-gpt/chat/completions", r.URL.Path)
		require.Equal(t, "2024-10-01-preview", r.URL.Query().Get("api-version"))
		require.Equal(t, "az-key", r.Header.Get("api-key"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"az1","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"azure ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`)
	}))
	defer srv.Close()

	c := NewOpenAICompatible("azure", "")
	resp, err := c.Chat(context.Background(), textReq("ignored-model", false), core.Credentials{
		APIKey: "az-key",
		Extra: map[string]string{
			"azure_endpoint": srv.URL,
			"deployment":     "prod-gpt",
			"api_version":    "2024-10-01-preview",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "azure ok", resp.Message.TextContent())
}

func TestAnthropic_Chat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/messages", r.URL.Path)
		require.Equal(t, "sk-ant", r.Header.Get("x-api-key"))
		require.Equal(t, anthropicVersion, r.Header.Get("anthropic-version"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"m1","model":"claude-x","content":[{"type":"text","text":"hello back"}],"stop_reason":"end_turn","usage":{"input_tokens":4,"output_tokens":3}}`)
	}))
	defer srv.Close()

	c := NewAnthropic("anthropic", srv.URL)
	resp, err := c.Chat(context.Background(), textReq("claude-x", false), core.Credentials{APIKey: "sk-ant"})
	require.NoError(t, err)
	require.Equal(t, "hello back", resp.Message.TextContent())
	require.Equal(t, core.FinishStop, resp.FinishReason)
	require.Equal(t, 7, resp.Usage.TotalTokens)
}

// TestAnthropic_Chat_HTMLResponse covers a custom anthropic-compatible base URL
// that points at the provider's web frontend (e.g. missing the "/v1" path
// segment). The frontend answers POST /messages with HTTP 200 and an HTML body;
// the connector must surface an actionable error instead of a raw JSON
// "Syntax error at index 0".
func TestAnthropic_Chat_HTMLResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "<!doctype html>\n<html lang=\"zh-CN\"><head><title>Gateway</title></head></html>")
	}))
	defer srv.Close()

	c := NewAnthropic("custom-anthropic-test", srv.URL)
	_, err := c.Chat(context.Background(), textReq("claude-opus-4-8", false), core.Credentials{APIKey: "sk-x"})
	require.Error(t, err)
	pe := core.AsProviderError(err)
	require.Equal(t, core.ErrUpstream, pe.Kind)
	require.Equal(t, 0, pe.StatusCode)
	require.Contains(t, err.Error(), "non-JSON response")
	require.Contains(t, err.Error(), "/v1")
	// The raw HTML must not leak into the JSON parser path.
	require.NotContains(t, err.Error(), "Syntax error")
}

// TestAnthropic_Validate_HTMLResponse ensures validation of a custom provider
// pointed at a web frontend fails rather than false-positively passing (the
// frontend returns HTTP 200 HTML for both GET /models and POST /messages).
func TestAnthropic_Validate_HTMLResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "<!doctype html><html><body>frontend</body></html>")
	}))
	defer srv.Close()

	c := NewAnthropic("custom-anthropic-test", srv.URL)
	err := c.Validate(context.Background(), core.Credentials{APIKey: "sk-x", BaseURL: srv.URL})
	require.Error(t, err)
}

func TestOllama_ValidateProbesTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/tags", r.URL.Path)
		require.Equal(t, "Bearer ollama-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"models":[]}`)
	}))
	defer srv.Close()

	c := NewOllama("ollama", srv.URL)
	require.NoError(t, c.Validate(context.Background(), core.Credentials{APIKey: "ollama-key"}))
}

func TestRegistry_DefaultProviders(t *testing.T) {
	reg := DefaultRegistry()
	require.True(t, reg.Has("openai"))
	require.True(t, reg.Has("anthropic"))

	c, err := reg.Get("anthropic")
	require.NoError(t, err)
	require.Equal(t, core.DialectAnthropic, c.Dialect())

	_, err = reg.Get("nonexistent")
	require.Error(t, err)
}

// publicModelsServer simulates a non-strict OpenAI-compatible gateway whose
// GET /models endpoint lists models WITHOUT checking auth, while the chat
// endpoint properly rejects a bad key with 401. This is the case that made
// invalid keys pass validation before the authenticated chat probe was added.
func publicModelsServer(t *testing.T, goodKey string) (*httptest.Server, *bool) {
	t.Helper()
	chatProbed := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			// No auth check — returns 200 for anyone.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"m1"}]}`))
		case "/chat/completions":
			chatProbed = true
			if r.Header.Get("Authorization") != "Bearer "+goodKey {
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, `{"error":"invalid api key"}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &chatProbed
}

func TestOpenAICompatible_ValidateConfirmsKeyWhenModelsIsPublic(t *testing.T) {
	t.Run("bad key is rejected via chat probe", func(t *testing.T) {
		srv, chatProbed := publicModelsServer(t, "good-key")
		c := NewOpenAICompatible("sumopod", srv.URL)
		err := c.Validate(context.Background(), core.Credentials{APIKey: "bad-key"})
		require.Error(t, err, "a bad key must fail even when /models returns 200 without auth")
		require.True(t, *chatProbed, "validation should confirm the key with a chat probe")
	})

	t.Run("good key passes", func(t *testing.T) {
		srv, chatProbed := publicModelsServer(t, "good-key")
		c := NewOpenAICompatible("sumopod", srv.URL)
		require.NoError(t, c.Validate(context.Background(), core.Credentials{APIKey: "good-key"}))
		require.True(t, *chatProbed)
	})
}

func TestOpenAICompatible_ValidateStrictTrustsModels200(t *testing.T) {
	// Strict providers (e.g. openai) auth-protect /models, so a 200 is conclusive
	// and the chat probe must NOT be issued (avoids consuming quota).
	srv, chatProbed := publicModelsServer(t, "good-key")
	c := NewOpenAICompatible("openai", srv.URL)
	require.NoError(t, c.Validate(context.Background(), core.Credentials{APIKey: "any-key"}))
	require.False(t, *chatProbed, "strict providers should trust a /models 200 without a chat probe")
}

func TestOpenAICompatible_ValidateNoAuthTrustsModels200(t *testing.T) {
	// No-auth accounts (no key/token) only get a reachability check.
	srv, chatProbed := publicModelsServer(t, "good-key")
	c := NewOpenAICompatible("local-gw", srv.URL)
	require.NoError(t, c.Validate(context.Background(), core.Credentials{}))
	require.False(t, *chatProbed, "no-auth accounts should not issue a chat probe")
}

func TestOpenAICompatibleModelSource_ListModelsPublicNoCreds(t *testing.T) {
	// A provider whose /models endpoint is public (e.g. sumopod) must be
	// discoverable without credentials, so the model catalog populates before
	// an account is connected.
	srv, _ := publicModelsServer(t, "good-key")
	src := &OpenAICompatibleModelSource{provider: "sumopod", defaultBase: srv.URL}

	models, err := src.ListModels(context.Background(), core.Credentials{})
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, "m1", models[0].ID)
	require.Equal(t, core.ServiceLLM, models[0].Kind)
}

func TestGetLiveModelSource_DynamicOpenAIProvider(t *testing.T) {
	id := "custom-openai-testdisco"
	RegisterDynamicProvider(DynamicProvider{
		ID:      id,
		Dialect: core.DialectOpenAI,
		BaseURL: "https://example.test/v1",
	})
	t.Cleanup(func() { UnregisterDynamicProvider(id) })

	src := GetLiveModelSource(id)
	require.NotNil(t, src, "dynamic OpenAI-compatible providers should get a live model source")

	// A dynamic Anthropic provider now gets an Anthropic-compatible model source
	// (GET /v1/models discovery), mirroring the OpenAI-compatible path.
	aid := "custom-anthropic-testdisco"
	RegisterDynamicProvider(DynamicProvider{ID: aid, Dialect: core.DialectAnthropic, BaseURL: "https://example.test"})
	t.Cleanup(func() { UnregisterDynamicProvider(aid) })
	asrc := GetLiveModelSource(aid)
	require.NotNil(t, asrc, "dynamic Anthropic-compatible providers should get a live model source")
	_, ok := asrc.(*AnthropicCompatibleModelSource)
	require.True(t, ok, "dynamic Anthropic provider should yield an AnthropicCompatibleModelSource")
}
