package connectors

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/stretchr/testify/require"
)

func TestCloudflareModelSourceUsesAccountSearchEndpointWithPagination(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		require.Equal(t, "Bearer cf-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "1":
			// A full page: entries carry the model id in "name", a UUID in "id".
			fmt.Fprint(w, `{"success":true,"errors":[],"result":[`)
			for i := 0; i < cloudflareModelsPerPage; i++ {
				if i > 0 {
					fmt.Fprint(w, ",")
				}
				task := "Text Generation"
				name := fmt.Sprintf("@cf/vendor/model-%d", i)
				switch i {
				case 1:
					task, name = "Text-to-Image", "@cf/black-forest-labs/flux-1-schnell"
				case 2:
					task, name = "Text Embeddings", "@cf/baai/bge-m3"
				case 3:
					task, name = "Translation", "@cf/meta/m2m100-1.2b"
				}
				fmt.Fprintf(w, `{"id":"uuid-%d","name":"%s","task":{"name":"%s"},"properties":[{"property_id":"function_calling","value":"true"}]}`, i, name, task)
			}
			fmt.Fprint(w, `]}`)
		default:
			fmt.Fprint(w, `{"success":true,"errors":[],"result":[{"id":"uuid-last","name":"@hf/thebloke/codellama-7b-instruct-awq","task":{"name":"Text Generation"}}]}`)
		}
	}))
	defer srv.Close()

	src := &CloudflareModelSource{defaultBase: srv.URL + "/client/v4/accounts/{accountId}/ai/v1"}
	models, err := src.ListModels(context.Background(), core.Credentials{APIKey: "cf-token", Extra: map[string]string{"accountId": "acc123"}})
	require.NoError(t, err)
	require.Len(t, paths, 2, "a full first page triggers a second fetch")
	require.Equal(t, "/client/v4/accounts/acc123/ai/models/search?page=1&per_page=100", paths[0])

	byID := map[string]ModelSpec{}
	for _, m := range models {
		byID[m.ID] = m
	}
	require.Equal(t, core.ServiceLLM, byID["@cf/vendor/model-0"].Kind)
	require.Equal(t, core.ServiceImage, byID["@cf/black-forest-labs/flux-1-schnell"].Kind)
	require.Equal(t, core.ServiceEmbedding, byID["@cf/baai/bge-m3"].Kind)
	require.Equal(t, core.ServiceLLM, byID["@hf/thebloke/codellama-7b-instruct-awq"].Kind)
	_, hasTranslation := byID["@cf/meta/m2m100-1.2b"]
	require.False(t, hasTranslation, "non-routable tasks are dropped")
	for _, m := range models {
		require.NotContains(t, m.ID, "uuid-", "the opaque id must never be used as the model id")
	}
}

func TestCloudflareModelSourceSurfacesEnvelopeErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"success":false,"errors":[{"code":9106,"message":"Authentication failed (status: 400)"}],"messages":[],"result":null}`)
	}))
	defer srv.Close()
	src := &CloudflareModelSource{defaultBase: srv.URL + "/client/v4/accounts/{accountId}/ai/v1"}
	_, err := src.ListModels(context.Background(), core.Credentials{APIKey: "bad", Extra: map[string]string{"accountId": "acc"}})
	require.ErrorContains(t, err, "Authentication failed")
	require.ErrorContains(t, err, "9106")

	_, err = src.ListModels(context.Background(), core.Credentials{APIKey: "x"})
	require.ErrorContains(t, err, "account ID")
}

func TestCloudflareURLAndModelNormalisation(t *testing.T) {
	require.Equal(t, "https://api.cloudflare.com/client/v4/accounts/acc/ai/v1",
		NormalizeCloudflareBaseURL("https://api.cloudflare.com/client/v4/accounts/acc/ai/run/"))
	require.Equal(t, "https://gateway.ai.cloudflare.com/v1/acc/gw/workers-ai/v1",
		NormalizeCloudflareBaseURL("https://gateway.ai.cloudflare.com/v1/acc/gw/workers-ai"))
	require.Equal(t, "https://api.cloudflare.com/client/v4/accounts/acc/ai/v1",
		NormalizeCloudflareBaseURL("https://api.cloudflare.com/client/v4/accounts/acc/ai/v1"))
	require.Equal(t, "@cf/meta/llama-3.3-70b-instruct-fp8-fast", CanonicalCloudflareModel("meta/llama-3.3-70b-instruct-fp8-fast"))
	require.Equal(t, "@hf/thebloke/x", CanonicalCloudflareModel("@hf/thebloke/x"))

	c := NewOpenAICompatible("cloudflare-ai", "https://api.cloudflare.com/client/v4/accounts/{accountId}/ai/v1")
	creds := core.Credentials{APIKey: "k", BaseURL: "https://api.cloudflare.com/client/v4/accounts/{accountId}/ai/run", Extra: map[string]string{"accountId": "acc"}}
	require.Equal(t, "https://api.cloudflare.com/client/v4/accounts/acc/ai/v1/chat/completions", c.chatCompletionsURL(creds, "@cf/x"))
}

func TestCloudflareAuthFailureAs400IsAuthError(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}}
	body := []byte(`{"success":false,"errors":[{"code":10000,"message":"Authentication error"}],"messages":[],"result":null}`)
	pe := core.AsProviderError(httpStatusError("cloudflare-ai", "@cf/m", resp, body))
	require.Equal(t, core.ErrAuth, pe.Kind)
	require.Equal(t, core.FailureScopeAccount, pe.EffectiveScope())
	require.True(t, pe.Fallbackable(), "another account must be tried")
}
