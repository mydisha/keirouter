// Package topics is KeiRouter's topic-boundary detector. It enforces two
// modes against a free-form topic list configured per policy:
//
//   - block: the configured topics are off-limits. Any prompt that matches
//     one triggers the configured action (default warn).
//   - allow: only the configured topics are permitted. Any prompt that does
//     not match at least one triggers the configured action.
//
// Detection is layered:
//
//   - Layer 1 (always on): word-boundary keyword match over the folded word
//     view, tolerant of inflections, small typos and obfuscation. Cheap,
//     deterministic, covers the majority of cases.
//   - Layer 2 (opt-in via engine="embedding"): cosine-similarity between the
//     prompt embedding and a pre-computed topic embedding. Catches semantic
//     paraphrases the keyword path misses. Requires an Embedder wired at
//     construction; if none is wired the detector silently falls back to
//     keyword-only so existing policies keep working.
package topics

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mydisha/keirouter/backend/internal/guardrails"
)

// Embedder is the topic-detector's view of an embedding model. It is a
// strict subset of cache.Embedder so the cache package can be wired in
// directly without an import cycle.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// Detector implements guardrails.Detector for topic boundaries.
type Detector struct {
	embedder  Embedder
	completer atomic.Pointer[completerBox]
	llmCache  *verdictCache
	// vectorCache memoizes the embedding for each topic string so we don't
	// re-embed the same topic on every request. Keyed by the *normalized*
	// topic text.
	vectorCache *topicVectorCache
}

// Config configures the detector at construction time.
type Config struct {
	// Embedder enables the embedding path. If nil the detector runs in
	// keyword-only mode regardless of the policy's engine selection.
	Embedder Embedder
}

// New builds a topics detector.
func New(cfg Config) *Detector {
	return &Detector{
		embedder:    cfg.Embedder,
		vectorCache: newTopicVectorCache(),
		llmCache:    newVerdictCache(2048, 30*time.Minute),
	}
}

type completerBox struct{ c guardrails.Completer }

// SetCompleter enables the "llm" engine. The gateway calls it once its
// routing is ready.
func (d *Detector) SetCompleter(c guardrails.Completer) {
	if c != nil {
		d.completer.Store(&completerBox{c: c})
	}
}

// Capabilities reports which optional engines the server can run.
func (d *Detector) Capabilities() map[string]bool {
	return map[string]bool{
		"embedding": d.embedder != nil,
		"llm":       d.completer.Load() != nil,
	}
}

// Name identifies this detector in policy config and audit logs.
func (*Detector) Name() string { return "topics" }

// Inbound scans the user prompt. Action defaults to warn when none is set.
func (d *Detector) Inbound(ctx context.Context, in *guardrails.InboundRequest, p guardrails.Policy) (*guardrails.Decision, error) {
	cfg := p.Topics
	if cfg == nil || !cfg.Enabled || len(cfg.Topics) == 0 {
		return nil, nil
	}

	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" {
		mode = "block"
	}

	// Block lists look at every user message, so a forbidden topic can't be
	// slipped in through earlier turns. Allow lists look at the last few
	// user messages, so a follow-up such as "tell me more" inherits the
	// topic of the question before it. The system prompt is never matched:
	// it would put every request on (or off) topic.
	text := in.UserText()
	if mode == "allow" {
		text = in.RecentUserText()
	}
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}

	if strings.EqualFold(strings.TrimSpace(cfg.Engine), "llm") {
		if dec, ok := d.llmDecision(ctx, cfg, mode, in); ok {
			return dec, nil
		}
	}

	matches := d.matchTopics(ctx, text, cfg)
	triggered := false
	switch mode {
	case "allow":
		// Allowlist: anything NOT matching the list is off-policy.
		triggered = len(matches) == 0
	default: // "block"
		triggered = len(matches) > 0
	}
	if !triggered {
		return nil, nil
	}

	action := cfg.Action
	if action == "" {
		action = guardrails.DefaultTopicsAction
	}
	return &guardrails.Decision{
		Action:   action,
		Severity: guardrails.SeverityMedium,
		Reason:   reasonFor(mode, matches, cfg.Topics),
		Findings: toFindings(mode, matches, cfg.Topics),
	}, nil
}

// llmDecision runs the "llm" engine. ok is false when it can't run (no
// model, no completer, an error), and the caller then falls back: block lists
// to keyword matching, allow lists to letting the message through, since a
// keyword allow list would refuse ordinary chat.
func (d *Detector) llmDecision(ctx context.Context, cfg *guardrails.TopicsConfig, mode string, in *guardrails.InboundRequest) (*guardrails.Decision, bool) {
	box := d.completer.Load()
	if box == nil || strings.TrimSpace(cfg.Model) == "" || strings.TrimSpace(in.LastUserText()) == "" {
		return nil, mode == "allow"
	}
	v, err := d.llmClassify(ctx, box.c, cfg, mode, in)
	if err != nil {
		slog.Warn("guardrails: topics LLM engine failed", "model", cfg.Model, "err", err)
		return nil, mode == "allow"
	}
	action := cfg.Action
	if action == "" {
		action = guardrails.DefaultTopicsAction
	}
	switch {
	case v.Hijacked:
		return &guardrails.Decision{
			Action:   action,
			Severity: guardrails.SeverityHigh,
			Reason:   "topics: the message tried to override the topic check",
			Findings: []guardrails.Finding{{Entity: "TOPIC:llm:judge_hijacked", Score: 1, Start: -1, End: -1}},
		}, true
	case mode == "allow" && len(v.Matched) == 0 && !v.SmallTalk:
		return &guardrails.Decision{
			Action:   action,
			Severity: guardrails.SeverityMedium,
			Reason:   reasonFor(mode, nil, cfg.Topics),
			Findings: []guardrails.Finding{{Entity: "TOPIC:llm:off_allowlist", Score: 1, Start: -1, End: -1}},
		}, true
	case mode != "allow" && len(v.Matched) > 0:
		ms := make([]match, len(v.Matched))
		for i, t := range v.Matched {
			ms[i] = match{Topic: t, Layer: "llm", Score: 1}
		}
		return &guardrails.Decision{
			Action:   action,
			Severity: guardrails.SeverityMedium,
			Reason:   reasonFor(mode, ms, cfg.Topics),
			Findings: toFindings(mode, ms, cfg.Topics),
		}, true
	}
	return nil, true
}

// Outbound: topics are evaluated on the prompt only. Returning nil is
// correct — the engine treats nil as ActionAllow.
func (*Detector) Outbound(_ context.Context, _ *guardrails.OutboundResponse, _ guardrails.Policy) (*guardrails.Decision, error) {
	return nil, nil
}

// match is one topic that matched the prompt, with the highest-confidence
// score across both layers.
type match struct {
	Topic string
	// Layer is "keyword" or "embedding"; used in audit findings.
	Layer string
	Score float64
}

// matchTopics runs Layer 1 (keyword) for every topic and, if the embedder
// is wired and the policy opts in, Layer 2 (embedding similarity) for any
// topic that did not already match.
func (d *Detector) matchTopics(ctx context.Context, text string, cfg *guardrails.TopicsConfig) []match {
	index := newPromptIndex(text)
	out := make([]match, 0, len(cfg.Topics))
	embedRun := d.shouldUseEmbedding(cfg)

	var promptVec []float32
	threshold := cfg.SimilarityThreshold
	if threshold <= 0 {
		threshold = 0.6
	}

	for _, raw := range cfg.Topics {
		topic := strings.TrimSpace(raw)
		if topic == "" {
			continue
		}
		// Layer 1: keyword/ngram.
		if keywordHit(index, topic) {
			out = append(out, match{Topic: topic, Layer: "keyword", Score: 1.0})
			continue
		}
		// Layer 2: embedding similarity (only if not already a keyword hit).
		if !embedRun {
			continue
		}
		if promptVec == nil {
			vec, err := d.embedder.Embed(ctx, text)
			if err != nil || len(vec) == 0 {
				// Embedder failure is non-fatal: keyword results stand. We
				// stop trying the embedder for the remaining topics on this
				// request to avoid N error calls.
				embedRun = false
				continue
			}
			promptVec = vec
		}
		topicVec, err := d.vectorCache.get(ctx, d.embedder, topic)
		if err != nil || len(topicVec) == 0 {
			continue
		}
		sim := cosine(promptVec, topicVec)
		if sim >= threshold {
			out = append(out, match{Topic: topic, Layer: "embedding", Score: sim})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

func (d *Detector) shouldUseEmbedding(cfg *guardrails.TopicsConfig) bool {
	if d.embedder == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Engine)) {
	case "embedding", "hybrid":
		return true
	case "", "keyword":
		// Default: keyword-only. The embedding path is opt-in because it
		// adds a network call to the request critical path.
		return false
	}
	return false
}

func reasonFor(mode string, matches []match, topics []string) string {
	if mode == "allow" && len(matches) == 0 {
		return "topics: off-topic, the message mentions none of the allowed topics (" + strings.Join(topics, ", ") + ")"
	}
	parts := make([]string, 0, len(matches))
	for _, m := range matches {
		parts = append(parts, m.Topic)
	}
	return "topics: " + mode + " match: " + strings.Join(parts, ", ")
}

func toFindings(mode string, matches []match, topics []string) []guardrails.Finding {
	if mode == "allow" && len(matches) == 0 {
		return []guardrails.Finding{{
			Entity: "TOPIC:off_allowlist",
			Score:  1.0,
			Start:  -1,
			End:    -1,
		}}
	}
	out := make([]guardrails.Finding, 0, len(matches))
	for _, m := range matches {
		out = append(out, guardrails.Finding{
			Entity: "TOPIC:" + m.Layer + ":" + m.Topic,
			Score:  m.Score,
			Start:  -1,
			End:    -1,
		})
	}
	_ = topics
	return out
}
