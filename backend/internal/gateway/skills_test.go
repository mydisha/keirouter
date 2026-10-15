package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/mydisha/keirouter/backend/internal/config"
	"github.com/mydisha/keirouter/backend/internal/skills"
	"github.com/mydisha/keirouter/backend/internal/store"
)

func newSkillsTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx))
	t.Cleanup(func() { _ = db.Close() })

	s := &Server{settings: db.Settings()}
	r := chi.NewRouter()
	r.Get("/skills", s.adminListSkills)
	r.Post("/skills", s.adminCreateSkill)
	r.Post("/skills/{id}", s.adminUpdateSkill)
	r.Delete("/skills/{id}", s.adminDeleteSkill)
	return s, r
}

func doSkills(t *testing.T, h http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	}
	return rec.Code, out
}

func TestSkillsLifecycleUpdatesInjectedBlock(t *testing.T) {
	s, h := newSkillsTestServer(t)
	require.Empty(t, s.skillSet().Block(skills.Request{}))

	code, created := doSkills(t, h, http.MethodPost, "/skills", `{"name":"  Reviewer ","prompt":" Prefer small diffs. "}`)
	require.Equal(t, http.StatusCreated, code)
	require.Equal(t, "Reviewer", created["name"])
	id := created["id"].(string)
	require.Equal(t, "Prefer small diffs.", s.skillSet().Block(skills.Request{}))

	code, updated := doSkills(t, h, http.MethodPost, "/skills/"+id, `{"prompt":"Prefer tiny diffs.","description":"style"}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "Reviewer", updated["name"])
	require.Equal(t, "style", updated["description"])
	require.Equal(t, "Prefer tiny diffs.", s.skillSet().Block(skills.Request{}))

	code, _ = doSkills(t, h, http.MethodPost, "/skills/"+id, `{"enabled":false}`)
	require.Equal(t, http.StatusOK, code)
	require.Empty(t, s.skillSet().Block(skills.Request{}))

	code, _ = doSkills(t, h, http.MethodDelete, "/skills/"+id, "")
	require.Equal(t, http.StatusNoContent, code)
	_, list := doSkills(t, h, http.MethodGet, "/skills", "")
	require.Empty(t, list["skills"])
}

func TestSkillsValidation(t *testing.T) {
	_, h := newSkillsTestServer(t)

	code, _ := doSkills(t, h, http.MethodPost, "/skills", `{"name":"   "}`)
	require.Equal(t, http.StatusBadRequest, code)

	long := strings.Repeat("x", maxSkillPromptLen+1)
	code, _ = doSkills(t, h, http.MethodPost, "/skills", `{"name":"a","prompt":"`+long+`"}`)
	require.Equal(t, http.StatusBadRequest, code)

	_, created := doSkills(t, h, http.MethodPost, "/skills", `{"name":"a"}`)
	code, _ = doSkills(t, h, http.MethodPost, "/skills/"+created["id"].(string), `{"name":""}`)
	require.Equal(t, http.StatusBadRequest, code)

	code, _ = doSkills(t, h, http.MethodPost, "/skills/missing", `{"enabled":true}`)
	require.Equal(t, http.StatusNotFound, code)
}

func TestSkillsConfigLoadsPersistedSkills(t *testing.T) {
	s, _ := newSkillsTestServer(t)
	require.NoError(t, s.settings.Set(context.Background(), skillsKey,
		`[{"id":"1","name":"a","prompt":"One.","enabled":true,"optional":false,"scope":{}},`+
			`{"id":"2","name":"b","prompt":"Two.","enabled":false,"optional":false,"scope":{}}]`))
	require.Equal(t, "One.", s.skillSet().Block(skills.Request{}))
}

// Skills saved by earlier versions were stored but never applied; upgrading
// must not start injecting them into every request.
func TestSkillsLegacyEntriesAreDisabledOnce(t *testing.T) {
	s, h := newSkillsTestServer(t)
	ctx := context.Background()
	require.NoError(t, s.settings.Set(ctx, skillsKey,
		`[{"id":"old","name":"Old","prompt":"Legacy.","enabled":true,"created_at":"2026-01-01T00:00:00Z"},`+
			`{"id":"new","name":"New","prompt":"Current.","enabled":true,"optional":false,"scope":{}}]`))

	require.Equal(t, "Current.", s.skillSet().Block(skills.Request{}), "legacy skill must not be injected")

	// The migration is persisted, so re-enabling the skill afterwards sticks.
	raw, err := s.settings.Get(ctx, skillsKey)
	require.NoError(t, err)
	require.Contains(t, raw, `"scope"`)
	code, _ := doSkills(t, h, http.MethodPost, "/skills/old", `{"enabled":true}`)
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, s.skillSet().Block(skills.Request{}), "Legacy.")
	require.Len(t, s.loadSkills(ctx), 2)
	for _, sk := range s.loadSkills(ctx) {
		require.True(t, sk.Enabled, sk.ID)
	}
}

// upstreamSystem posts a chat request and returns the system message the fake
// upstream received.
func upstreamSystem(t *testing.T, h *e2eHarness, captured *[]byte, model string, header http.Header) string {
	t.Helper()
	body := `{"model":"` + model + `","messages":[{"role":"system","content":"Client rules."},{"role":"user","content":"hi"}]}`
	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/v1/chat/completions", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.apiKey)
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var sent struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(*captured, &sent))
	require.NotEmpty(t, sent.Messages)
	require.Equal(t, "system", sent.Messages[0].Role)
	return fmt.Sprint(sent.Messages[0].Content)
}

func newCapturingE2E(t *testing.T) (*e2eHarness, *[]byte) {
	t.Helper()
	captured := new([]byte)
	h := newE2E(t, func(w http.ResponseWriter, r *http.Request) {
		*captured, _ = io.ReadAll(r.Body)
		openAIUpstream()(w, r)
	})
	return h, captured
}

func TestE2E_EnabledSkillReachesUpstream(t *testing.T) {
	h, captured := newCapturingE2E(t)
	h.gateway.cacheSkills([]skill{
		{ID: "1", Name: "house style", Prompt: "Answer in British English.", Enabled: true},
		{ID: "2", Name: "off", Prompt: "Never seen.", Enabled: false},
	})

	system := upstreamSystem(t, h, captured, "openai/gpt-4o", nil)
	require.Contains(t, system, "Answer in British English.")
	require.Contains(t, system, "Client rules.")
	require.NotContains(t, string(*captured), "Never seen.")
	require.Less(t, strings.Index(system, "Answer in British English."), strings.Index(system, "Client rules."))
}

func TestE2E_SkillScopes(t *testing.T) {
	h, captured := newCapturingE2E(t)
	h.gateway.cacheSkills([]skill{
		{ID: "1", Name: "other key", Prompt: "Only for another key.", Enabled: true, Scope: skills.Scope{Keys: []string{"some-other-key"}}},
		{ID: "2", Name: "other plan", Prompt: "Only for another plan.", Enabled: true, Scope: skills.Scope{Plans: []string{"some-plan"}}},
		{ID: "3", Name: "gpt family", Prompt: "GPT rule.", Enabled: true, Scope: skills.Scope{Models: []string{"gpt-4*"}}},
		{ID: "4", Name: "claude family", Prompt: "Claude rule.", Enabled: true, Scope: skills.Scope{Models: []string{"claude-*"}}},
	})

	system := upstreamSystem(t, h, captured, "openai/gpt-4o", nil)
	require.Contains(t, system, "GPT rule.")
	require.NotContains(t, system, "Claude rule.")
	require.NotContains(t, system, "Only for another key.")
	require.NotContains(t, system, "Only for another plan.")
}

func TestE2E_SkipHeaderDropsOnlyOptionalSkills(t *testing.T) {
	h, captured := newCapturingE2E(t)
	h.gateway.cacheSkills([]skill{
		{ID: "1", Name: "policy", Prompt: "Mandatory rule.", Enabled: true},
		{ID: "2", Name: "style", Prompt: "Optional rule.", Enabled: true, Optional: true},
	})

	system := upstreamSystem(t, h, captured, "openai/gpt-4o", http.Header{"X-Keirouter-Skip-Skills": {"true"}})
	require.Contains(t, system, "Mandatory rule.")
	require.NotContains(t, system, "Optional rule.")

	system = upstreamSystem(t, h, captured, "openai/gpt-4o", nil)
	require.Contains(t, system, "Optional rule.")
}

func TestSkillsScopeNormalized(t *testing.T) {
	_, h := newSkillsTestServer(t)
	code, created := doSkills(t, h, http.MethodPost, "/skills",
		`{"name":"a","prompt":"x","optional":true,"scope":{"plans":[" p1 ","p1",""],"models":["claude-*"]}}`)
	require.Equal(t, http.StatusCreated, code)
	require.Equal(t, true, created["optional"])
	scope := created["scope"].(map[string]any)
	require.Equal(t, []any{"p1"}, scope["plans"])
	require.Nil(t, scope["keys"])
	require.Equal(t, []any{"claude-*"}, scope["models"])

	code, updated := doSkills(t, h, http.MethodPost, "/skills/"+created["id"].(string), `{"scope":{}}`)
	require.Equal(t, http.StatusOK, code)
	require.Empty(t, updated["scope"])
}

func TestSkillsVariables(t *testing.T) {
	s, h := newSkillsTestServer(t)
	code, created := doSkills(t, h, http.MethodPost, "/skills",
		`{"name":"a","prompt":"Support for {{company}} via {{contact}}.","variables":{" company ":" Acme ","contact":"  "}}`)
	require.Equal(t, http.StatusCreated, code)
	require.Equal(t, map[string]any{"company": "Acme"}, created["variables"])
	require.Equal(t, "Support for Acme via {{contact}}.", s.skillSet().Block(skills.Request{}))
	id := created["id"].(string)

	code, updated := doSkills(t, h, http.MethodPost, "/skills/"+id, `{"variables":{"company":"Globex","contact":"help@globex.test"}}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "Support for {{company}} via {{contact}}.", updated["prompt"])
	require.Equal(t, "Support for Globex via help@globex.test.", s.skillSet().Block(skills.Request{}))

	code, _ = doSkills(t, h, http.MethodPost, "/skills/"+id, `{"variables":{"bad name":"x"}}`)
	require.Equal(t, http.StatusBadRequest, code)

	long := strings.Repeat("x", maxSkillPromptLen-5)
	code, _ = doSkills(t, h, http.MethodPost, "/skills", `{"name":"b","prompt":"`+long+`{{v}}","variables":{"v":"0123456789"}}`)
	require.Equal(t, http.StatusBadRequest, code)

	code, updated = doSkills(t, h, http.MethodPost, "/skills/"+id, `{"variables":{}}`)
	require.Equal(t, http.StatusOK, code)
	require.Nil(t, updated["variables"])
}
