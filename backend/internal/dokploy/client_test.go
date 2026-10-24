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

func TestListServersSkipsBuildServers(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_, _ = w.Write([]byte(`[
			{"serverId":"srv-my","name":"KL 1","ipAddress":"203.0.113.30","serverStatus":"active","serverType":"deploy","sshKeyId":"k"},
			{"serverId":"srv-b","name":"Builder","ipAddress":"203.0.113.40","serverStatus":"active","serverType":"build"},
			{"serverId":"srv-old","name":"Old","ipAddress":"203.0.113.50","serverStatus":"inactive"}
		]`))
	}))
	defer srv.Close()

	servers, err := NewClient(srv.URL, "k").ListServers(context.Background())
	require.NoError(t, err)
	require.Equal(t, "/api/server.all", gotPath)
	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, []Server{
		{ServerID: "srv-my", Name: "KL 1", IPAddress: "203.0.113.30", Status: "active"},
		{ServerID: "srv-old", Name: "Old", IPAddress: "203.0.113.50", Status: "inactive"},
	}, servers)
}
