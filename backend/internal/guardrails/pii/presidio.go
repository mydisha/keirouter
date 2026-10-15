package pii

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mydisha/keirouter/backend/internal/guardrails"
)

// PresidioEngine calls a Microsoft Presidio HTTP analyzer to recognize PII.
// It is an *alternative* to the native Go recognizers — selected per-policy
// via PIIConfig.Engine = "presidio". When the analyzer is unreachable the
// engine returns no matches and the detector silently falls back to native
// (see pii.Detector.recognize).
//
// Only the /analyze endpoint is used. Anonymization is done in Go
// (applyStrategy), so the anonymizer sidecar is not needed.
type PresidioEngine struct {
	httpClient *http.Client
	analyzer   string // base URL, e.g. http://localhost:5001
	language   string // BCP-47, defaults to "en"
}

// PresidioConfig configures the engine.
type PresidioConfig struct {
	// AnalyzerURL is the base URL of the Presidio analyzer service. Empty
	// disables the engine.
	AnalyzerURL string
	// Timeout caps each analyze call. Defaults to 5s.
	Timeout time.Duration
	// Language is the BCP-47 language code passed to Presidio. Defaults to
	// "en"; set to "id" for Indonesian if the sidecar has the model loaded.
	Language string
}

// NewPresidioEngine builds a Presidio-backed engine. Returns nil when no
// analyzer URL is configured — callers should check before using.
func NewPresidioEngine(cfg PresidioConfig) *PresidioEngine {
	if strings.TrimSpace(cfg.AnalyzerURL) == "" {
		return nil
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.Language == "" {
		cfg.Language = "en"
	}
	return &PresidioEngine{
		httpClient: &http.Client{Timeout: cfg.Timeout},
		analyzer:   strings.TrimRight(cfg.AnalyzerURL, "/"),
		language:   cfg.Language,
	}
}

// presidioRequest mirrors the Presidio /analyze POST body.
type presidioRequest struct {
	Text           string   `json:"text"`
	Language       string   `json:"language"`
	Entities       []string `json:"entities,omitempty"`
	ScoreThreshold float64  `json:"score_threshold,omitempty"`
	ReturnDecision bool     `json:"return_decision_process,omitempty"`
}

// presidioFinding mirrors one row in the Presidio /analyze response.
type presidioFinding struct {
	EntityType string  `json:"entity_type"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Score      float64 `json:"score"`
}

// Recognize calls the analyzer and converts the response into the native
// Match shape so the rest of the PII pipeline (severity, applyStrategy, audit
// findings) keeps working unchanged.
func (e *PresidioEngine) Recognize(ctx context.Context, text string, allowed map[Entity]bool, minScore float64) ([]Match, error) {
	if text == "" {
		return nil, nil
	}
	body, err := json.Marshal(presidioRequest{
		Text:           text,
		Language:       e.language,
		Entities:       allowedEntityList(allowed),
		ScoreThreshold: minScore,
	})
	if err != nil {
		return nil, fmt.Errorf("pii/presidio: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.analyzer+"/analyze", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("pii/presidio: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pii/presidio: http: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("pii/presidio: read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pii/presidio: status %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var findings []presidioFinding
	if err := json.Unmarshal(raw, &findings); err != nil {
		return nil, fmt.Errorf("pii/presidio: parse: %w", err)
	}
	// Presidio reports offsets in code points; convert them to byte offsets.
	byteAt := runeOffsets(text)
	out := make([]Match, 0, len(findings))
	for _, f := range findings {
		if f.Start < 0 || f.End >= len(byteAt) || f.End <= f.Start {
			continue
		}
		f.Start, f.End = byteAt[f.Start], byteAt[f.End]
		ent := Entity(strings.ToUpper(f.EntityType))
		if allowed != nil && !allowed[ent] {
			continue
		}
		if f.Score < minScore {
			continue
		}
		out = append(out, Match{
			Entity: ent,
			Start:  f.Start,
			End:    f.End,
			Text:   text[f.Start:f.End],
			Score:  f.Score,
		})
	}
	return mergeOverlaps(out, text), nil
}

// batchSeparator keeps entities from different texts apart in a batch call.
const batchSeparator = "\n\n\u2063\n\n"

// RecognizeBatch analyzes several texts with a single /analyze call. Each
// text is folded first (zero-width characters, fullwidth digits, look-alike
// letters) and spans are mapped back onto the original text. A finding that
// straddles two texts is dropped.
func (e *PresidioEngine) RecognizeBatch(ctx context.Context, texts []string, allowed map[Entity]bool, minScore float64) ([][]Match, error) {
	out := make([][]Match, len(texts))
	folded := make([]*guardrails.Folded, len(texts))
	starts := make([]int, len(texts))
	var b strings.Builder
	for i, t := range texts {
		folded[i] = guardrails.Fold(t, guardrails.FoldOptions{})
		if i > 0 {
			b.WriteString(batchSeparator)
		}
		starts[i] = b.Len()
		b.WriteString(folded[i].Text)
	}
	joined := b.String()
	if strings.TrimSpace(joined) == "" {
		return out, nil
	}
	matches, err := e.Recognize(ctx, joined, allowed, minScore)
	if err != nil {
		return nil, err
	}
	for _, m := range matches {
		i := sort.Search(len(starts), func(k int) bool { return starts[k] > m.Start }) - 1
		if i < 0 {
			continue
		}
		s, en := m.Start-starts[i], m.End-starts[i]
		if en > len(folded[i].Text) {
			continue
		}
		os, oe := folded[i].Span(s, en)
		if os < 0 {
			continue
		}
		m.Start, m.End, m.Text = os, oe, texts[i][os:oe]
		out[i] = append(out[i], m)
	}
	return out, nil
}

// runeOffsets maps each code-point index of s (plus the end) to its byte
// offset.
func runeOffsets(s string) []int {
	out := make([]int, 0, utf8.RuneCountInString(s)+1)
	for i := range s {
		out = append(out, i)
	}
	return append(out, len(s))
}

func allowedEntityList(allowed map[Entity]bool) []string {
	if len(allowed) == 0 {
		return nil
	}
	out := make([]string, 0, len(allowed))
	for e := range allowed {
		out = append(out, string(e))
	}
	return out
}
