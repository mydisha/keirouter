package injection

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// ClassifierConfig points at an ML prompt-injection classifier served over
// HTTP with the Hugging Face text-classification contract:
//
//	POST <URL>  {"inputs": ["text", ...]}
//	→ [[{"label": "INJECTION", "score": 0.98}, {"label": "SAFE", ...}], ...]
//
// Text Embeddings Inference (/predict), Hugging Face Inference Endpoints and
// the bundled deploy/promptguard sidecar all speak it. Recommended models:
// meta-llama/Llama-Prompt-Guard-2-86M (multilingual) or
// protectai/deberta-v3-base-prompt-injection-v2 (English).
type ClassifierConfig struct {
	URL     string
	Timeout time.Duration
	// Labels that mean "injection". Default: INJECTION, JAILBREAK,
	// MALICIOUS, UNSAFE and LABEL_1 (Prompt Guard 2's raw label).
	Labels []string
	// APIKey, when set, is sent as a bearer token.
	APIKey string
}

// Classifier scores text with the configured model.
type Classifier struct {
	url    string
	key    string
	labels map[string]bool
	http   *http.Client
	cache  *resultCache[float64]
}

const (
	// classifierChunk keeps each input within the 512-token window of the
	// DeBERTa-based models; chunks overlap so a phrase is never split.
	classifierChunk   = 1800
	classifierOverlap = 200
	// classifierMaxChunks bounds one request; the newest text is kept.
	classifierMaxChunks = 32
)

// NewClassifier returns nil when no URL is configured.
func NewClassifier(cfg ClassifierConfig) *Classifier {
	if strings.TrimSpace(cfg.URL) == "" {
		return nil
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Second
	}
	labels := cfg.Labels
	if len(labels) == 0 {
		labels = []string{"INJECTION", "JAILBREAK", "MALICIOUS", "UNSAFE", "LABEL_1"}
	}
	set := make(map[string]bool, len(labels))
	for _, l := range labels {
		set[strings.ToUpper(strings.TrimSpace(l))] = true
	}
	return &Classifier{
		url:    strings.TrimRight(cfg.URL, "/"),
		key:    cfg.APIKey,
		labels: set,
		http:   &http.Client{Timeout: cfg.Timeout},
		cache:  newResultCache[float64](4096, 30*time.Minute),
	}
}

// chunkScore is the injection probability of one chunk of the input.
type chunkScore struct {
	start, end int
	score      float64
}

// Score returns the injection probability of every chunk of text.
func (c *Classifier) Score(ctx context.Context, text string) ([]chunkScore, error) {
	chunks := splitChunks(text, classifierChunk, classifierOverlap, classifierMaxChunks)
	out := make([]chunkScore, len(chunks))
	var pending []int
	for i, ch := range chunks {
		out[i] = chunkScore{start: ch.start, end: ch.end, score: -1}
		if v, ok := c.cache.get(cacheKey("clf", ch.text)); ok {
			out[i].score = v
		} else {
			pending = append(pending, i)
		}
	}
	if len(pending) == 0 {
		return out, nil
	}
	inputs := make([]string, len(pending))
	for k, i := range pending {
		inputs[k] = chunks[i].text
	}
	scores, err := c.call(ctx, inputs)
	if err != nil {
		return nil, err
	}
	for k, i := range pending {
		out[i].score = scores[k]
		c.cache.put(cacheKey("clf", chunks[i].text), scores[k])
	}
	return out, nil
}

type classLabel struct {
	Label string  `json:"label"`
	Score float64 `json:"score"`
}

func (c *Classifier) call(ctx context.Context, inputs []string) ([]float64, error) {
	body, _ := json.Marshal(map[string]any{"inputs": inputs, "truncate": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("injection/classifier: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("injection/classifier: read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("injection/classifier: status %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	// A batch answers with one label list per input; some servers answer a
	// single input with a flat list.
	var batch [][]classLabel
	if err := json.Unmarshal(raw, &batch); err != nil {
		var flat []classLabel
		if err2 := json.Unmarshal(raw, &flat); err2 != nil {
			return nil, fmt.Errorf("injection/classifier: parse: %w", err)
		}
		batch = [][]classLabel{flat}
	}
	if len(batch) != len(inputs) {
		return nil, fmt.Errorf("injection/classifier: %d results for %d inputs", len(batch), len(inputs))
	}
	out := make([]float64, len(batch))
	for i, labels := range batch {
		out[i] = c.injectionProb(labels)
	}
	return out, nil
}

// injectionProb sums the scores of injection labels. When the server only
// returned the top label and it is benign, the complement is used.
func (c *Classifier) injectionProb(labels []classLabel) float64 {
	var p float64
	seen := false
	for _, l := range labels {
		if c.labels[strings.ToUpper(l.Label)] {
			p += l.Score
			seen = true
		}
	}
	if !seen && len(labels) == 1 {
		return 1 - labels[0].Score
	}
	return min(p, 1)
}

type chunk struct {
	text       string
	start, end int
}

// splitChunks cuts text into overlapping windows of at most size bytes on
// rune boundaries, keeping the last max windows.
func splitChunks(text string, size, overlap, maxChunks int) []chunk {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var out []chunk
	for start := 0; start < len(text); {
		end := min(start+size, len(text))
		for end < len(text) && !utf8.RuneStart(text[end]) {
			end--
		}
		out = append(out, chunk{text: text[start:end], start: start, end: end})
		if end == len(text) {
			break
		}
		next := end - overlap
		for next > start && !utf8.RuneStart(text[next]) {
			next--
		}
		if next <= start {
			next = end
		}
		start = next
	}
	if len(out) > maxChunks {
		out = out[len(out)-maxChunks:]
	}
	return out
}
