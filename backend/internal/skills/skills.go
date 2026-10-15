// Package skills injects operator-defined instruction blocks ("custom skills")
// into the system prompt of chat requests.
//
// Skills are managed from the dashboard and stored as one JSON blob in the
// settings store. The gateway compiles the enabled ones into a Set once per
// change; per request, Set.Block picks the skills whose scope matches the
// caller and joins their prompts, so the hot path is a few set lookups and one
// string join.
//
// Scope has two dimensions that must both match:
//   - audience: the request's API key is listed in Keys OR its plan in Plans
//     (both empty = every caller);
//   - models: the requested or primary resolved model matches a pattern in
//     Models (empty = every model). Patterns allow a trailing '*'.
//
// Prompts may contain {{name}} placeholders filled from the skill's Variables
// at compile time, so operators edit values in form fields instead of the text.
//
// Like caveman, Apply adds no visible marker: the rendered block itself is the
// idempotency probe, so retries and chained fallbacks never inject it twice.
// The block is PREPENDED so it forms a stable prefix ahead of the client's own
// system text, which keeps provider prompt caches warm across requests.
package skills

import (
	"regexp"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// Config carries the rendered skills block for one request. An empty Block
// disables injection.
type Config struct {
	Block string
}

// Scope limits which requests a skill applies to. The zero value applies to
// every request.
type Scope struct {
	Plans  []string `json:"plans,omitempty"`
	Keys   []string `json:"keys,omitempty"`
	Models []string `json:"models,omitempty"`
}

// Skill is the subset of a stored skill that affects injection.
type Skill struct {
	Prompt  string
	Enabled bool
	// Optional skills are dropped when the client sends the skip header;
	// mandatory ones always apply.
	Optional bool
	Scope    Scope
	// Variables fill {{name}} placeholders in Prompt.
	Variables map[string]string
}

// Request describes the caller a block is selected for.
type Request struct {
	KeyID  string
	PlanID string
	// Models are the names the request is known by (requested model, primary
	// resolved model); a model pattern matches if it matches any of them.
	Models []string
	// SkipOptional drops optional skills (client sent the skip header).
	SkipOptional bool
}

type compiled struct {
	prompt   string
	optional bool
	keys     map[string]struct{}
	plans    map[string]struct{}
	models   []string // lower-cased patterns
}

// Set is an immutable, pre-compiled list of enabled skills with a prompt.
type Set struct {
	skills []compiled
}

// Compile keeps the enabled skills that have a prompt, in stored order.
func Compile(list []Skill) *Set {
	s := &Set{}
	for _, sk := range list {
		p := strings.TrimSpace(Render(sk.Prompt, sk.Variables))
		if !sk.Enabled || p == "" {
			continue
		}
		c := compiled{prompt: p, optional: sk.Optional, keys: toSet(sk.Scope.Keys), plans: toSet(sk.Scope.Plans)}
		for _, m := range sk.Scope.Models {
			if m = strings.ToLower(strings.TrimSpace(m)); m != "" {
				c.models = append(c.models, m)
			}
		}
		s.skills = append(s.skills, c)
	}
	return s
}

// Len reports how many skills the set can inject.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.skills)
}

// Block joins the prompts of every skill that applies to r. It returns "" when
// none does.
func (s *Set) Block(r Request) string {
	if s.Len() == 0 {
		return ""
	}
	var parts []string
	for i := range s.skills {
		c := &s.skills[i]
		if c.optional && r.SkipOptional {
			continue
		}
		if c.audienceMatches(r) && c.modelMatches(r.Models) {
			parts = append(parts, c.prompt)
		}
	}
	return strings.Join(parts, "\n\n")
}

func (c *compiled) audienceMatches(r Request) bool {
	if len(c.keys) == 0 && len(c.plans) == 0 {
		return true
	}
	if _, ok := c.keys[r.KeyID]; ok && r.KeyID != "" {
		return true
	}
	_, ok := c.plans[r.PlanID]
	return ok && r.PlanID != ""
}

func (c *compiled) modelMatches(models []string) bool {
	if len(c.models) == 0 {
		return true
	}
	for _, m := range models {
		if m != "" && MatchModel(m, c.models) {
			return true
		}
	}
	return false
}

// MatchModel reports whether model matches any lower-cased pattern. A pattern
// ending in '*' matches by prefix; otherwise the match is exact. Case is
// ignored.
func MatchModel(model string, patterns []string) bool {
	lower := strings.ToLower(model)
	for _, p := range patterns {
		if prefix, ok := strings.CutSuffix(p, "*"); ok {
			if strings.HasPrefix(lower, prefix) {
				return true
			}
		} else if p == lower {
			return true
		}
	}
	return false
}

// placeholderRE matches a {{name}} placeholder; spaces inside the braces are
// allowed.
var placeholderRE = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.-]+)\s*\}\}`)

// Render replaces each {{name}} placeholder that has a non-empty value in vars.
// Placeholders without a value are left as written.
func Render(prompt string, vars map[string]string) string {
	if len(vars) == 0 || !strings.Contains(prompt, "{{") {
		return prompt
	}
	return placeholderRE.ReplaceAllStringFunc(prompt, func(m string) string {
		name := placeholderRE.FindStringSubmatch(m)[1]
		if v := vars[name]; v != "" {
			return v
		}
		return m
	})
}

func toSet(ids []string) map[string]struct{} {
	if len(ids) == 0 {
		return nil
	}
	m := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			m[id] = struct{}{}
		}
	}
	return m
}

// Apply prepends cfg.Block to req.System. It is a no-op when the block is empty
// or already present, so it is safe across retries and fallback attempts.
func Apply(req *core.ChatRequest, cfg Config) {
	if req == nil || cfg.Block == "" {
		return
	}
	if strings.Contains(req.System, cfg.Block) {
		return
	}
	if strings.TrimSpace(req.System) == "" {
		req.System = cfg.Block
		return
	}
	req.System = cfg.Block + "\n\n" + req.System
}
