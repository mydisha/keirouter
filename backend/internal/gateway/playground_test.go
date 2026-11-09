package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/audit"
	"github.com/mydisha/keirouter/backend/internal/identity"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// dashboard sends a request with the harness's dashboard session cookie
// (session=false sends none).
func (h *e2eHarness) dashboard(t *testing.T, method, path, body string, session bool) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, h.server.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if session {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: h.session})
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

const playgroundChat = `{"model":"openai/gpt-4o","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`

func TestPlayground_RequiresDashboardSession(t *testing.T) {
	h := newE2E(t, streamNoUsageUpstream())

	resp := h.dashboard(t, http.MethodPost, "/api/playground/chat/completions", playgroundChat, false)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp = h.dashboard(t, http.MethodGet, "/api/playground/models", "", false)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// An API key is not a dashboard session.
	req, _ := http.NewRequest(http.MethodPost, h.server.URL+"/api/playground/chat/completions", strings.NewReader(playgroundChat))
	req.Header.Set("Authorization", "Bearer "+h.apiKey)
	r2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer r2.Body.Close()
	require.Equal(t, http.StatusUnauthorized, r2.StatusCode)

	keys, err := h.db.APIKeys().List(context.Background(), store.DefaultTenantID)
	require.NoError(t, err)
	require.Len(t, keys, 1, "a rejected request creates no playground key")
}

func TestPlayground_StreamsUnderPlaygroundKey(t *testing.T) {
	h := newE2E(t, streamNoUsageUpstream())

	resp := h.dashboard(t, http.MethodPost, "/api/playground/chat/completions", playgroundChat, true)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	out := string(raw)
	require.Contains(t, out, `"content":"par"`)
	require.Contains(t, out, "[DONE]")
	usage := lastStreamUsage(t, out)
	require.NotNil(t, usage, "the playground asks for the usage chunk")
	require.Greater(t, usage.TotalTokens, 0)

	ctx := context.Background()
	pg, err := h.db.APIKeys().FindByKind(ctx, store.DefaultTenantID, store.KeyKindPlayground)
	require.NoError(t, err)
	require.Equal(t, identity.PlaygroundKeyName, pg.Name)
	require.Eventually(t, func() bool {
		rows, err := h.db.Usage().RecentByKey(ctx, pg.ID, time.Now().Add(-time.Hour), 10)
		return err == nil && len(rows) == 1 && rows[0].Model == "gpt-4o"
	}, 5*time.Second, 20*time.Millisecond, "usage is recorded against the playground key")

	// A second turn reuses the same key.
	resp = h.dashboard(t, http.MethodPost, "/api/playground/chat/completions", playgroundChat, true)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_, _ = io.ReadAll(resp.Body)
	keys, err := h.db.APIKeys().List(ctx, store.DefaultTenantID)
	require.NoError(t, err)
	require.Len(t, keys, 2, "the test key plus one playground key")
}

func TestPlayground_KeyIsHiddenAndLockedInAdminAPI(t *testing.T) {
	h := newE2E(t, streamNoUsageUpstream())
	resp := h.dashboard(t, http.MethodPost, "/api/playground/chat/completions", playgroundChat, true)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_, _ = io.ReadAll(resp.Body)
	pg, err := h.db.APIKeys().FindByKind(context.Background(), store.DefaultTenantID, store.KeyKindPlayground)
	require.NoError(t, err)

	resp = h.dashboard(t, http.MethodGet, "/api/keys", "", true)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var list struct {
		Keys []struct {
			ID string `json:"id"`
		} `json:"keys"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	require.Len(t, list.Keys, 1)
	require.Equal(t, h.keyID, list.Keys[0].ID)

	resp = h.dashboard(t, http.MethodPatch, "/api/keys/"+pg.ID, `{"disabled":true}`, true)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp = h.dashboard(t, http.MethodDelete, "/api/keys/"+pg.ID, "", true)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	still, err := h.db.APIKeys().Get(context.Background(), pg.ID)
	require.NoError(t, err)
	require.False(t, still.Disabled)

	// Ordinary keys are still editable.
	resp = h.dashboard(t, http.MethodPatch, "/api/keys/"+h.keyID, `{"disabled":true}`, true)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestPlayground_ChatIsNotAudited(t *testing.T) {
	h := newE2E(t, streamNoUsageUpstream())
	rec := audit.New(h.db.Audit(), nil, audit.Config{DefaultTenantID: store.DefaultTenantID})
	t.Cleanup(func() { rec.Stop(time.Second) })
	h.gateway.audit = rec

	resp := h.dashboard(t, http.MethodPost, "/api/playground/chat/completions", playgroundChat, true)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_, _ = io.ReadAll(resp.Body)
	// An admin write on the same session is audited, so the recorder works.
	resp = h.dashboard(t, http.MethodPatch, "/api/keys/"+h.keyID, `{"disabled":true}`, true)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	rows := auditRows(t, h.db, rec)
	require.Len(t, rows, 1)
	require.NotContains(t, rows[0].Action, "playground")
}

func TestPlayground_ModelsListsConnectedChatModels(t *testing.T) {
	h := newE2E(t, openAIUpstream())
	resp := h.dashboard(t, http.MethodGet, "/api/playground/models", "", true)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		Models []playgroundModel `json:"models"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.NotEmpty(t, body.Models)
	byID := map[string]playgroundModel{}
	for _, m := range body.Models {
		require.Equal(t, "openai", m.Provider, "only the connected provider's models: %s", m.ID)
		require.True(t, strings.HasPrefix(m.ID, "openai/"))
		require.Equal(t, "/providers/openai.png", m.Icon)
		require.False(t, m.CanDisableReasoning && !m.Reasoning, "off without an effort control: %s", m.ID)
		byID[m.ID] = m
	}
	gpt4o, ok := byID["openai/gpt-4o"]
	require.True(t, ok)
	require.False(t, gpt4o.Reasoning, "gpt-4o takes no reasoning effort")
	require.True(t, gpt4o.Vision)
}
