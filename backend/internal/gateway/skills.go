package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/skills"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// skillsKey is the settings key under which custom skills are stored. Skills
// are operator instructions prepended to the system prompt of matching chat
// requests while enabled.
const skillsKey = "skills"

// skipSkillsHeader lets a client opt out of skills marked optional. Mandatory
// skills still apply.
const skipSkillsHeader = "X-KeiRouter-Skip-Skills"

// Limits keep one skill from silently bloating every request.
const (
	maxSkillNameLen     = 80
	maxSkillPromptLen   = 20000
	maxSkillScopeValues = 200
	maxSkillVariables   = 50
	maxSkillVariableLen = 1000
)

// skillVariableNameRE mirrors the placeholder names skills.Render fills.
var skillVariableNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

type skill struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Prompt      string       `json:"prompt"`
	Enabled     bool         `json:"enabled"`
	Optional    bool         `json:"optional"`
	Scope       skills.Scope `json:"scope"`
	// Variables fill {{name}} placeholders in Prompt when the skill is
	// compiled; the stored prompt keeps the placeholders.
	Variables map[string]string `json:"variables,omitempty"`
	CreatedAt string            `json:"created_at"`
	UpdatedAt string            `json:"updated_at,omitempty"`
}

func (s *Server) loadSkills(ctx context.Context) []skill {
	list := []skill{}
	if s.settings == nil {
		return list
	}
	raw, err := s.settings.Get(ctx, skillsKey)
	if err != nil || raw == "" {
		return list
	}
	_ = json.Unmarshal([]byte(raw), &list)
	if legacy, disabled := disableLegacySkills(raw, list); legacy {
		if err := s.saveSkills(ctx, list); err != nil && s.log != nil {
			s.log.Warn("skills: saving migrated legacy skills failed", "err", err)
		}
		if disabled > 0 && s.log != nil {
			s.log.Warn("skills: disabled skills saved by an earlier version; enable them on the Skills page to apply them",
				"count", disabled)
		}
	}
	return list
}

// disableLegacySkills turns off skills saved before skills were injected into
// requests. Earlier versions stored skills (enabled by default) but never
// applied them, so leaving them on would silently start rewriting every
// request's system prompt after an upgrade. They are recognised by the
// missing "scope" key, which every current write includes. legacy reports
// whether any such entry was found (so the caller persists the migration
// once); disabled counts the ones that were switched off.
func disableLegacySkills(raw string, list []skill) (legacy bool, disabled int) {
	var objs []map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &objs) != nil || len(objs) != len(list) {
		return false, 0
	}
	for i, obj := range objs {
		if _, ok := obj["scope"]; ok {
			continue
		}
		legacy = true
		if list[i].Enabled {
			list[i].Enabled = false
			disabled++
		}
	}
	return legacy, disabled
}

func (s *Server) saveSkills(ctx context.Context, list []skill) error {
	raw, err := json.Marshal(list)
	if err != nil {
		return err
	}
	if err := s.settings.Set(ctx, skillsKey, string(raw)); err != nil {
		return err
	}
	s.cacheSkills(list)
	return nil
}

// skillsCacheTTL bounds how stale the in-memory skill set may get when
// another replica sharing the database edits skills.
const skillsCacheTTL = 30 * time.Second

// cachedSkills is the compiled skill set plus when it was loaded.
type cachedSkills struct {
	set      *skills.Set
	loadedAt time.Time
}

func (s *Server) cacheSkills(list []skill) *cachedSkills {
	in := make([]skills.Skill, len(list))
	for i, sk := range list {
		in[i] = skills.Skill{Prompt: sk.Prompt, Enabled: sk.Enabled, Optional: sk.Optional, Scope: sk.Scope, Variables: sk.Variables}
	}
	c := &cachedSkills{set: skills.Compile(in), loadedAt: time.Now()}
	s.skillsBlock.Store(c)
	return c
}

// skillSet returns the compiled skill set. It is cached in memory, refreshed
// on every admin write and at most every skillsCacheTTL, so the hot path
// rarely touches the settings store.
func (s *Server) skillSet() *skills.Set {
	c := s.skillsBlock.Load()
	if c == nil || time.Since(c.loadedAt) > skillsCacheTTL {
		c = s.cacheSkills(s.loadSkills(context.Background()))
	}
	return c.set
}

// skillsConfig selects the skills that apply to this request: the caller's API
// key and plan, the requested model and the primary resolved model. Fallback
// targets reuse the same block, since injection happens once per request.
func (s *Server) skillsConfig(r *http.Request, key store.APIKey, requested string, targets []dispatch.Target) skills.Config {
	set := s.skillSet()
	if set.Len() == 0 {
		return skills.Config{}
	}
	models := []string{requested}
	if len(targets) > 0 {
		models = append(models, targets[0].Model)
	}
	skip, _ := strconv.ParseBool(strings.TrimSpace(r.Header.Get(skipSkillsHeader)))
	return skills.Config{Block: set.Block(skills.Request{
		KeyID:        key.ID,
		PlanID:       key.PlanID,
		Models:       models,
		SkipOptional: skip,
	})}
}

// normalizeScope trims, de-duplicates and bounds every scope list.
func normalizeScope(sc skills.Scope) (skills.Scope, string) {
	clean := func(in []string, field string) ([]string, string) {
		seen := make(map[string]struct{}, len(in))
		out := make([]string, 0, len(in))
		for _, v := range in {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			if _, dup := seen[v]; dup {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
		if len(out) > maxSkillScopeValues {
			return nil, "too many " + field + " in scope (max 200)"
		}
		if len(out) == 0 {
			return nil, ""
		}
		return out, ""
	}
	var msg string
	if sc.Plans, msg = clean(sc.Plans, "plans"); msg != "" {
		return sc, msg
	}
	if sc.Keys, msg = clean(sc.Keys, "keys"); msg != "" {
		return sc, msg
	}
	sc.Models, msg = clean(sc.Models, "models")
	return sc, msg
}

// normalizeVariables trims names and values and drops empty values, since an
// empty value leaves its placeholder as written anyway.
func normalizeVariables(in map[string]string) (map[string]string, string) {
	out := make(map[string]string, len(in))
	for name, v := range in {
		name = strings.TrimSpace(name)
		if !skillVariableNameRE.MatchString(name) {
			return nil, "invalid variable name " + strconv.Quote(name) + " (use letters, digits, _ . -)"
		}
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if utf8.RuneCountInString(v) > maxSkillVariableLen {
			return nil, "variable " + name + " is too long (max 1000 characters)"
		}
		out[name] = v
	}
	if len(out) > maxSkillVariables {
		return nil, "too many variables (max 50)"
	}
	if len(out) == 0 {
		return nil, ""
	}
	return out, ""
}

// renderedTooLong reports whether the prompt exceeds the limit once its
// variables are filled in.
func renderedTooLong(sk skill) bool {
	return utf8.RuneCountInString(skills.Render(sk.Prompt, sk.Variables)) > maxSkillPromptLen
}

const renderedTooLongMsg = "prompt is too long once variables are filled in (max 20000 characters)"

// validateSkill normalizes name/prompt/scope/variables in place and returns a
// user-facing error message, or "" when valid.
func validateSkill(name, prompt *string, scope *skills.Scope, vars *map[string]string) string {
	if name != nil {
		*name = strings.TrimSpace(*name)
		if *name == "" {
			return "name is required"
		}
		if utf8.RuneCountInString(*name) > maxSkillNameLen {
			return "name is too long (max 80 characters)"
		}
	}
	if prompt != nil {
		*prompt = strings.TrimSpace(*prompt)
		if utf8.RuneCountInString(*prompt) > maxSkillPromptLen {
			return "prompt is too long (max 20000 characters)"
		}
	}
	if scope != nil {
		sc, msg := normalizeScope(*scope)
		if msg != "" {
			return msg
		}
		*scope = sc
	}
	if vars != nil {
		v, msg := normalizeVariables(*vars)
		if msg != "" {
			return msg
		}
		*vars = v
	}
	return ""
}

func (s *Server) adminListSkills(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"skills": s.loadSkills(r.Context())})
}

func (s *Server) adminCreateSkill(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}
	var body struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Prompt      string            `json:"prompt"`
		Enabled     *bool             `json:"enabled"`
		Optional    bool              `json:"optional"`
		Scope       skills.Scope      `json:"scope"`
		Variables   map[string]string `json:"variables"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if msg := validateSkill(&body.Name, &body.Prompt, &body.Scope, &body.Variables); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	sk := skill{
		ID:          uuid.NewString(),
		Name:        body.Name,
		Description: strings.TrimSpace(body.Description),
		Prompt:      body.Prompt,
		Enabled:     enabled,
		Optional:    body.Optional,
		Scope:       body.Scope,
		Variables:   body.Variables,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if renderedTooLong(sk) {
		writeError(w, http.StatusBadRequest, renderedTooLongMsg)
		return
	}
	s.skillsMu.Lock()
	defer s.skillsMu.Unlock()
	if err := s.saveSkills(r.Context(), append(s.loadSkills(r.Context()), sk)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sk)
}

// adminUpdateSkill patches any subset of name, description, prompt, enabled,
// optional, scope and variables. A scope or variables map, when sent, replaces
// the stored one whole.
func (s *Server) adminUpdateSkill(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}
	id := chi.URLParam(r, "id")
	var body struct {
		Name        *string            `json:"name"`
		Description *string            `json:"description"`
		Prompt      *string            `json:"prompt"`
		Enabled     *bool              `json:"enabled"`
		Optional    *bool              `json:"optional"`
		Scope       *skills.Scope      `json:"scope"`
		Variables   *map[string]string `json:"variables"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if msg := validateSkill(body.Name, body.Prompt, body.Scope, body.Variables); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	s.skillsMu.Lock()
	defer s.skillsMu.Unlock()
	list := s.loadSkills(r.Context())
	idx := -1
	for i := range list {
		if list[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeError(w, http.StatusNotFound, "skill not found")
		return
	}
	sk := &list[idx]
	if body.Name != nil {
		sk.Name = *body.Name
	}
	if body.Description != nil {
		sk.Description = strings.TrimSpace(*body.Description)
	}
	if body.Prompt != nil {
		sk.Prompt = *body.Prompt
	}
	if body.Enabled != nil {
		sk.Enabled = *body.Enabled
	}
	if body.Optional != nil {
		sk.Optional = *body.Optional
	}
	if body.Scope != nil {
		sk.Scope = *body.Scope
	}
	if body.Variables != nil {
		sk.Variables = *body.Variables
	}
	if renderedTooLong(*sk) {
		writeError(w, http.StatusBadRequest, renderedTooLongMsg)
		return
	}
	sk.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.saveSkills(r.Context(), list); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, *sk)
}

func (s *Server) adminDeleteSkill(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusInternalServerError, "settings store not configured")
		return
	}
	id := chi.URLParam(r, "id")
	s.skillsMu.Lock()
	defer s.skillsMu.Unlock()
	list := s.loadSkills(r.Context())
	out := list[:0]
	for _, sk := range list {
		if sk.ID != id {
			out = append(out, sk)
		}
	}
	if err := s.saveSkills(r.Context(), out); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
