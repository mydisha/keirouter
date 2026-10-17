package dokploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClientSendsKeyAndDecodes(t *testing.T) {
	var gotPath, gotKey string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.Path, r.Header.Get("x-api-key")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"applicationId":"app-1","appName":"nr-abc"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL+"/api/", "secret")
	app, err := c.CreateApplication(context.Background(), "9router abc", "nr-abc", "env-1", "srv-1")
	require.NoError(t, err)
	require.Equal(t, "app-1", app.ApplicationID)
	require.Equal(t, "/api/application.create", gotPath)
	require.Equal(t, "secret", gotKey)
	require.Equal(t, "env-1", gotBody["environmentId"])
	require.Equal(t, "srv-1", gotBody["serverId"])
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	leaked := false
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("x-api-key") != ""
	}))
	defer evil.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	err := NewClient(srv.URL, "secret").Deploy(context.Background(), "app-1")
	require.ErrorContains(t, err, "redirected")
	require.False(t, leaked, "api key must not follow a redirect")
}

func TestClientErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	err := NewClient(srv.URL, "bad").Deploy(context.Background(), "app-1")
	require.ErrorContains(t, err, "status 401")
}

func TestCheckEnvironment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		_, _ = w.Write([]byte(`[{"projectId":"p","environments":[{"environmentId":"env-1"}]}]`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "k")
	require.NoError(t, c.CheckEnvironment(context.Background(), "env-1"))
	require.ErrorContains(t, c.CheckEnvironment(context.Background(), "env-2"), "not found")
}
