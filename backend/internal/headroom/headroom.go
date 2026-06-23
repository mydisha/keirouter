// Package headroom integrates KeiRouter with the Headroom compression proxy.
// It calls Headroom's compression-only HTTP API and applies the returned
// OpenAI-format messages back to KeiRouter's canonical request model.
package headroom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// Config controls Headroom compression for one request.
type Config struct {
	Enabled      bool
	BaseURL      string
	APIKey       string
	Timeout      time.Duration
	TokenBudget  int
	OutputShaper bool
	Fallback     bool
	IncludeTools bool
}

// Stats captures exact savings returned by Headroom.
type Stats struct {
	TokensBefore     int
	TokensAfter      int
	TokensSaved      int
	CompressionRatio float64
	Transforms       []string
	CCRHashes        []string
}

// Client calls the Headroom proxy.
type Client struct {
	http *http.Client
}

// New builds a Headroom client.
func New() *Client { return &Client{http: http.DefaultClient} }

// Compress sends req to Headroom, mutating req in place on success. It returns
// nil stats when disabled, when Headroom reports no compression, or when the
// request contains content we intentionally do not translate.
func (c *Client) Compress(ctx context.Context, req *core.ChatRequest, cfg Config) (*Stats, error) {
	if req == nil || !cfg.Enabled {
		return nil, nil
	}
	if containsUnsupportedParts(req) {
		return nil, nil
	}
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8787"
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	msgs := toOpenAIMessages(req)
	if len(msgs) == 0 {
		return nil, nil
	}
	body := map[string]any{
		"messages": msgs,
		"model":    req.Model,
		"fallback": true,
	}
	if cfg.TokenBudget > 0 {
		body["tokenBudget"] = cfg.TokenBudget
		body["token_budget"] = cfg.TokenBudget
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	hreq, err := http.NewRequestWithContext(callCtx, http.MethodPost, baseURL+"/v1/compress", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		hreq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	hc := c.http
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("headroom: status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var out compressResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Messages) == 0 {
		return nil, errors.New("headroom: empty messages response")
	}
	applyOpenAIMessages(req, out.Messages)

	stats := &Stats{
		TokensBefore:     out.TokensBefore,
		TokensAfter:      out.TokensAfter,
		TokensSaved:      out.TokensSaved,
		CompressionRatio: out.CompressionRatio,
		Transforms:       out.TransformsApplied,
		CCRHashes:        out.CCRHashes,
	}
	if stats.TokensSaved <= 0 && stats.TokensBefore > 0 && stats.TokensAfter > 0 && stats.TokensBefore > stats.TokensAfter {
		stats.TokensSaved = stats.TokensBefore - stats.TokensAfter
	}
	if stats.TokensSaved <= 0 && len(stats.Transforms) == 0 {
		return nil, nil
	}
	return stats, nil
}

type compressResponse struct {
	Messages          []openAIMessage `json:"messages"`
	TokensBefore      int             `json:"tokens_before"`
	TokensAfter       int             `json:"tokens_after"`
	TokensSaved       int             `json:"tokens_saved"`
	CompressionRatio  float64         `json:"compression_ratio"`
	TransformsApplied []string        `json:"transforms_applied"`
	CCRHashes         []string        `json:"ccr_hashes"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    any              `json:"content,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

func containsUnsupportedParts(req *core.ChatRequest) bool {
	for _, m := range req.Messages {
		for _, p := range m.Content {
			if p.Type == core.PartImage || p.Type == core.PartAudio || p.Type == core.PartThinking {
				return true
			}
		}
	}
	return false
}

func toOpenAIMessages(req *core.ChatRequest) []openAIMessage {
	out := make([]openAIMessage, 0, len(req.Messages)+1)
	if strings.TrimSpace(req.System) != "" {
		out = append(out, openAIMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		om := openAIMessage{Role: string(m.Role), Name: m.Name}
		var texts []string
		for _, p := range m.Content {
			switch p.Type {
			case core.PartText:
				if p.Text != "" {
					texts = append(texts, p.Text)
				}
			case core.PartToolResult:
				if p.ToolResult != nil {
					om.Role = "tool"
					om.ToolCallID = p.ToolResult.CallID
					om.Content = p.ToolResult.Content
				}
			case core.PartToolCall:
				if p.ToolCall != nil {
					var tc openAIToolCall
					tc.ID = p.ToolCall.ID
					tc.Type = "function"
					tc.Function.Name = p.ToolCall.Name
					tc.Function.Arguments = p.ToolCall.Arguments
					om.ToolCalls = append(om.ToolCalls, tc)
				}
			}
		}
		if om.Content == nil && len(texts) > 0 {
			om.Content = strings.Join(texts, "\n")
		}
		out = append(out, om)
	}
	return out
}

func applyOpenAIMessages(req *core.ChatRequest, msgs []openAIMessage) {
	req.System = ""
	req.Messages = nil
	for _, om := range msgs {
		if om.Role == "system" || om.Role == "developer" {
			if req.System != "" {
				req.System += "\n\n"
			}
			req.System += contentText(om.Content)
			continue
		}
		m := core.Message{Role: role(om.Role), Name: om.Name}
		if om.Role == "tool" {
			m.Content = append(m.Content, core.ContentPart{Type: core.PartToolResult, ToolResult: &core.ToolResult{CallID: om.ToolCallID, Content: contentText(om.Content)}})
			req.Messages = append(req.Messages, m)
			continue
		}
		if txt := contentText(om.Content); txt != "" {
			m.Content = append(m.Content, core.ContentPart{Type: core.PartText, Text: txt})
		}
		for _, tc := range om.ToolCalls {
			m.Content = append(m.Content, core.ContentPart{Type: core.PartToolCall, ToolCall: &core.ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments}})
		}
		req.Messages = append(req.Messages, m)
	}
}

func role(r string) core.Role {
	switch r {
	case "assistant":
		return core.RoleAssistant
	case "tool":
		return core.RoleTool
	default:
		return core.RoleUser
	}
}

func contentText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []any:
		var b strings.Builder
		for _, item := range x {
			if m, ok := item.(map[string]any); ok {
				if m["type"] == "text" {
					if s, ok := m["text"].(string); ok {
						b.WriteString(s)
					}
				}
			}
		}
		return b.String()
	default:
		b, _ := json.Marshal(x)
		var s string
		if json.Unmarshal(b, &s) == nil {
			return s
		}
		return string(b)
	}
}
