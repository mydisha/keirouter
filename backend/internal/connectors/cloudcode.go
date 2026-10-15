package connectors

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/transform"
)

// CloudCode drives Google's internal CloudCode Assist endpoints used by the
// Gemini CLI and Antigravity. These speak the Gemini generateContent format but
// wrap it: the request body is {project, model, request: <gemini>} and each
// streamed response chunk is {response: <gemini-chunk>}. This connector reuses
// the Gemini codec for the inner format and adds the CloudCode wrap/unwrap, plus
// the per-provider quirks (gemini-cli vs antigravity).
type CloudCode struct {
	id          string
	defaultBase string
	variant     cloudCodeVariant
	codec       transform.GeminiCodec
}

type cloudCodeVariant int

const (
	variantGeminiCLI cloudCodeVariant = iota
	variantAntigravity
)

// Gemini CLI client fingerprint.
const (
	geminiCLIVersion              = "0.34.0"
	geminiCLIAPIClient            = "google-genai-sdk/1.41.0 gl-node/v22.19.0"
	antigravityUA                 = "antigravity/1.104.0"
	antigravityClaudeContinuation = "Continue."
	antigravityCompetitivePrompt  = "You are a Claude agent, built on Anthropic's Claude Agent SDK."
	// CloudCode occasionally resets an HTTP/2 request before it returns SSE
	// headers. Retrying once is safe: no response bytes have reached the client.
	cloudCodeStreamOpenAttempts = 2
	cloudCodeStreamRetryDelay   = 250 * time.Millisecond
)

// NewGeminiCLI builds a CloudCode connector for the Gemini CLI provider.
func NewGeminiCLI(id, defaultBaseURL string) *CloudCode {
	return &CloudCode{id: id, defaultBase: defaultBaseURL, variant: variantGeminiCLI}
}

// NewAntigravity builds a CloudCode connector for the Antigravity provider.
func NewAntigravity(id, defaultBaseURL string) *CloudCode {
	c := &CloudCode{id: id, defaultBase: defaultBaseURL, variant: variantAntigravity}
	RegisterQuotaSource(id, c)
	return c
}

func (c *CloudCode) ID() string { return c.id }
func (c *CloudCode) Dialect() core.Dialect {
	if c.variant == variantAntigravity {
		return core.DialectAntigravity
	}
	return core.DialectGeminiCLI
}

func (c *CloudCode) baseURL(creds core.Credentials) string {
	if creds.BaseURL != "" {
		return creds.BaseURL
	}
	return c.defaultBase
}

// url builds the CloudCode endpoint. Gemini CLI appends the action directly to
// the base (which already ends in /v1internal); Antigravity appends
// "/v1internal:<action>" to a bare host base.
func (c *CloudCode) url(creds core.Credentials, stream bool) string {
	base := strings.TrimRight(c.baseURL(creds), "/")
	action := "generateContent"
	if stream {
		action = "streamGenerateContent?alt=sse"
	}
	if c.variant == variantAntigravity {
		return base + "/v1internal:" + action
	}
	return base + ":" + action
}

func (c *CloudCode) headers(creds core.Credentials, stream bool) map[string]string {
	accept := "application/json"
	if stream {
		accept = "text/event-stream"
	}
	h := map[string]string{
		"Authorization": bearer(creds.AccessToken),
		"Accept":        accept,
	}
	if c.variant == variantAntigravity {
		h["User-Agent"] = antigravityUA
		h["x-request-source"] = "local" // INTERNAL_REQUEST_HEADER (anti-loop)
		if sid := creds.Extra["session_id"]; sid != "" {
			h["X-Machine-Session-Id"] = sid
		}
	} else {
		h["User-Agent"] = "GeminiCLI/" + geminiCLIVersion
		h["X-Goog-Api-Client"] = geminiCLIAPIClient
	}
	return mergeHeaders(h, creds.Headers)
}

// Validate confirms an OAuth access token is present. CloudCode endpoints only
// expose a generate path (no cheap models/userinfo probe) and a live probe
// would consume quota, so this is a presence check.
func (c *CloudCode) Validate(ctx context.Context, creds core.Credentials) error {
	if creds.AccessToken == "" {
		return fmt.Errorf("validation failed for %s: no access token", c.id)
	}
	return nil
}

// FetchQuota fetches the upstream model quotas for the Antigravity provider.
// It bypasses the local language_server proxy and hits the API directly.
func (c *CloudCode) FetchQuota(ctx context.Context, creds core.Credentials) (*QuotaResult, error) {
	if c.variant != variantAntigravity {
		return nil, nil // Not supported for Gemini CLI
	}

	base := strings.TrimRight(c.baseURL(creds), "/")
	quotaURL := base + "/v1internal:fetchAvailableModels"

	bodyMap := make(map[string]any)
	if proj := creds.Extra["project_id"]; proj != "" {
		bodyMap["project"] = proj
	}
	body, _ := json.Marshal(bodyMap)

	respBody, err := doJSON(ctx, c.id, "quota", quotaURL, body, c.headers(creds, false))
	if err != nil {
		return nil, err
	}

	var result struct {
		Models map[string]struct {
			QuotaInfo struct {
				RemainingFraction *float64 `json:"remainingFraction"`
				ResetTime         string   `json:"resetTime"`
			} `json:"quotaInfo"`
		} `json:"models"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}

	var quotas []QuotaEntry
	for model, info := range result.Models {
		if info.QuotaInfo.ResetTime == "" || info.QuotaInfo.RemainingFraction == nil {
			continue // Skip models without quota tracking or inaccessible ones
		}

		frac := *info.QuotaInfo.RemainingFraction

		quotas = append(quotas, QuotaEntry{
			ResourceType: model,
			Limit:        100,
			Remaining:    int(frac * 100),
			Used:         100 - int(frac*100),
			ResetAt:      info.QuotaInfo.ResetTime,
		})
	}

	sort.Slice(quotas, func(i, j int) bool { return quotas[i].ResourceType < quotas[j].ResourceType })

	return &QuotaResult{Quotas: quotas}, nil
}

var antigravityModelAlias = map[string]string{
	"gemini-3.7-flash-low":    "gemini-3.7-flash-tiered",
	"gemini-3.7-flash-medium": "gemini-3.7-flash-tiered",
	"gemini-3.7-flash-high":   "gemini-3.7-flash-tiered",
	"gemini-3.5-flash-low":    "gemini-3.5-flash-extra-low",
	"gemini-3.5-flash-medium": "gemini-3.5-flash-low",
	"gemini-3.5-flash-high":   "gemini-3-flash-agent",
	"gemini-3.1-pro-low":      "gemini-pro-agent",
	"gemini-3.1-pro-high":     "gemini-pro-agent",
}

var antigravityModelFallbacks = map[string][]string{
	"gemini-3.1-pro-high": {"gemini-3.1-pro-high", "gemini-pro-agent", "gemini-3-pro-high"},
	"gemini-3.1-pro-low":  {"gemini-3.1-pro-low", "gemini-3-pro-low"},
}

func antigravityFallbackChain(model string) []string {
	if chain, ok := antigravityModelFallbacks[model]; ok {
		return chain
	}
	return []string{model}
}

func resolveAntigravityModel(model string) string {
	if alias, ok := antigravityModelAlias[model]; ok {
		return alias
	}
	return model
}

// wrapRequest renders the canonical request to the inner Gemini body, then wraps
// it in the CloudCode envelope expected by the provider.
func (c *CloudCode) wrapRequest(req *core.ChatRequest, creds core.Credentials, overrideModel string) ([]byte, error) {
	body, _, err := c.wrapRequestWithConversationRepair(req, creds, overrideModel, false)
	return body, err
}

// wrapRequestWithConversationRepair reports whether it had to repair the final
// conversation turn. forceRepair is reserved for a single retry after the
// upstream explicitly rejects the request as an assistant prefill.
func (c *CloudCode) wrapRequestWithConversationRepair(req *core.ChatRequest, creds core.Credentials, overrideModel string, forceRepair bool) ([]byte, bool, error) {
	effectiveModel := req.Model
	if overrideModel != "" {
		effectiveModel = overrideModel
	}

	renderReq := req
	innerObj, err := c.renderCloudCodeInnerRequest(renderReq)
	if err != nil {
		return nil, false, err
	}

	repaired := false
	needsProactiveRepair := isAntigravityClaudeModel(effectiveModel) &&
		antigravityClaudeRequestNeedsContinuation(innerObj)
	if c.variant == variantAntigravity && (forceRepair || needsProactiveRepair) {
		renderReq = appendAntigravityClaudeContinuation(req)
		innerObj, err = c.renderCloudCodeInnerRequest(renderReq)
		if err != nil {
			return nil, false, err
		}
		repaired = renderReq != req
	}

	projectID := creds.Extra["project_id"]
	if projectID == "" {
		projectID = generateCloudCodeProjectID()
	}

	if c.variant == variantAntigravity {
		stripAntigravityCompetitivePrompt(innerObj)
		// Antigravity adds session id + agent metadata to the inner request.
		sessionID := req.Metadata.ContextAffinityKey
		if sessionID != "" {
			sessionID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(sessionID)).String()
		}
		if sessionID == "" {
			sessionID = creds.Extra["session_id"]
		}
		if sessionID == "" {
			sessionID = deriveCloudCodeSession(creds)
		}
		innerObj["sessionId"] = sessionID
		if tools, ok := innerObj["tools"].([]any); ok && len(tools) > 0 {
			innerObj["toolConfig"] = map[string]any{
				"functionCallingConfig": map[string]any{"mode": "VALIDATED"},
			}
		}

		// Inject thinkingConfig for Reasoning models
		isHigh := strings.HasSuffix(req.Model, "-high") || (req.Reasoning != nil && req.Reasoning.Effort == "high")
		isLow := strings.HasSuffix(req.Model, "-low") || (req.Reasoning != nil && req.Reasoning.Effort == "low")
		if isHigh || isLow {
			budget := 1024
			if isHigh {
				budget = 8192
			}
			if req.Reasoning != nil && req.Reasoning.MaxTokens > 0 {
				budget = req.Reasoning.MaxTokens
			}

			genConf, ok := innerObj["generationConfig"].(map[string]any)
			if !ok || genConf == nil {
				genConf = make(map[string]any)
				innerObj["generationConfig"] = genConf
			}
			genConf["thinkingConfig"] = map[string]any{
				"thinkingBudget":  budget,
				"includeThoughts": true,
			}
		}

		envelope := map[string]any{
			"project":     projectID,
			"model":       resolveAntigravityModel(effectiveModel),
			"userAgent":   "antigravity",
			"requestType": "agent",
			"requestId":   "agent-" + uuid.NewString(),
			"request":     innerObj,
		}
		body, err := json.Marshal(envelope)
		return body, repaired, err
	}

	envelope := map[string]any{
		"project": projectID,
		"model":   req.Model,
		"request": innerObj,
	}
	body, err := json.Marshal(envelope)
	return body, repaired, err
}

func stripAntigravityCompetitivePrompt(inner map[string]any) bool {
	system, ok := inner["systemInstruction"].(map[string]any)
	if !ok {
		return false
	}
	parts, ok := system["parts"].([]any)
	if !ok {
		return false
	}
	changed := false
	cleaned := make([]any, 0, len(parts))
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok {
			cleaned = append(cleaned, raw)
			continue
		}
		text, ok := part["text"].(string)
		if !ok || !strings.Contains(text, antigravityCompetitivePrompt) {
			cleaned = append(cleaned, raw)
			continue
		}
		changed = true
		part["text"] = strings.TrimSpace(strings.ReplaceAll(text, antigravityCompetitivePrompt, ""))
		if part["text"] != "" {
			cleaned = append(cleaned, part)
		}
	}
	if !changed {
		return false
	}
	if len(cleaned) == 0 {
		delete(inner, "systemInstruction")
	} else {
		system["parts"] = cleaned
	}
	return true
}

func (c *CloudCode) renderCloudCodeInnerRequest(req *core.ChatRequest) (map[string]any, error) {
	inner, err := c.codec.RenderRequest(req)
	if err != nil {
		return nil, err
	}
	var innerObj map[string]any
	if err := json.Unmarshal(inner, &innerObj); err != nil {
		return nil, err
	}
	return innerObj, nil
}

func isAntigravityClaudeModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	model = strings.TrimPrefix(model, "antigravity/")
	model = strings.TrimPrefix(model, "models/")
	return strings.HasPrefix(model, "claude-")
}

// antigravityClaudeRequestNeedsContinuation examines the exact Gemini payload
// that will be sent upstream. This keeps the check aligned with the renderer as
// support for canonical content types evolves.
func antigravityClaudeRequestNeedsContinuation(inner map[string]any) bool {
	contents, ok := inner["contents"].([]any)
	if !ok || len(contents) == 0 {
		return false
	}
	last, ok := contents[len(contents)-1].(map[string]any)
	if !ok {
		return true
	}
	if role, _ := last["role"].(string); role == "model" {
		return true
	}
	parts, ok := last["parts"].([]any)
	if !ok || len(parts) == 0 {
		return true
	}
	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := part["text"].(string); ok && strings.TrimSpace(text) != "" {
			return false
		}
		if call, ok := part["functionCall"].(map[string]any); ok {
			if name, _ := call["name"].(string); strings.TrimSpace(name) != "" {
				return false
			}
		}
		if _, ok := part["functionResponse"].(map[string]any); ok {
			return false
		}
		if data, ok := part["inlineData"].(map[string]any); ok {
			if encoded, _ := data["data"].(string); strings.TrimSpace(encoded) != "" {
				return false
			}
		}
	}
	return true
}

// appendAntigravityClaudeContinuation clones the request before modifying the
// conversation. Accounting, fallback, and callers continue to see the original.
func appendAntigravityClaudeContinuation(req *core.ChatRequest) *core.ChatRequest {
	if req == nil || len(req.Messages) == 0 {
		return req
	}
	lastIndex := len(req.Messages) - 1
	last := req.Messages[lastIndex]
	clone := *req
	clone.Messages = append([]core.Message(nil), req.Messages...)
	continuation := core.ContentPart{
		Type: core.PartText,
		Text: antigravityClaudeContinuation,
	}
	if last.Role == core.RoleAssistant {
		clone.Messages = append(clone.Messages, core.Message{
			Role:    core.RoleUser,
			Content: []core.ContentPart{continuation},
		})
	} else {
		last.Content = append([]core.ContentPart(nil), last.Content...)
		last.Content = append(last.Content, continuation)
		clone.Messages[lastIndex] = last
	}
	return &clone
}

func isAntigravityClaudePrefillError(err error) bool {
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.StatusCode != http.StatusBadRequest {
		return false
	}
	message := strings.ToLower(pe.Message)
	return strings.Contains(message, "does not support assistant message prefill") ||
		strings.Contains(message, "conversation must end with a user message")
}

// unwrapResponse extracts the inner Gemini body from a CloudCode unary response
// ({response: <gemini>}), falling back to the body itself when not wrapped.
func unwrapCloudCodeResponse(body []byte) []byte {
	var wrapper struct {
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(body, &wrapper); err == nil && len(wrapper.Response) > 0 {
		return wrapper.Response
	}
	return body
}

// Chat performs a non-streaming CloudCode call.
func (c *CloudCode) Chat(ctx context.Context, req *core.ChatRequest, creds core.Credentials) (*core.ChatResponse, error) {
	request := *req
	request.Stream = false
	chain := []string{""}
	if c.variant == variantAntigravity {
		chain = antigravityFallbackChain(request.Model)
	}

	var lastErr error
	for _, override := range chain {
		body, _, err := c.wrapRequestWithConversationRepair(&request, creds, override, false)
		if err != nil {
			return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: request.Model, Message: err.Error(), Cause: err}
		}

		respBody, err := doJSON(ctx, c.id, request.Model, c.url(creds, false), body, c.headers(creds, false))
		if c.variant == variantAntigravity && isAntigravityClaudePrefillError(err) {
			retryBody, repaired, renderErr := c.wrapRequestWithConversationRepair(&request, creds, override, true)
			if renderErr != nil {
				return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: request.Model, Message: renderErr.Error(), Cause: renderErr}
			}
			if repaired {
				respBody, err = doJSON(ctx, c.id, request.Model, c.url(creds, false), retryBody, c.headers(creds, false))
			}
		}
		if err != nil {
			var pe *core.ProviderError
			if len(chain) > 1 && errors.As(err, &pe) && pe.Kind == core.ErrUpstream {
				lastErr = err
				continue
			}
			return nil, err
		}

		inner := unwrapCloudCodeResponse(respBody)
		resp, err := c.codec.ParseResponse(inner, request.Model)
		if err != nil {
			return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: request.Model, Message: err.Error(), Cause: err}
		}
		return resp, nil
	}
	return nil, lastErr
}

// Stream performs a streaming CloudCode call, unwrapping {response: ...} from
// each SSE chunk before handing the inner Gemini chunk to the codec.
func (c *CloudCode) Stream(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig) (<-chan core.StreamChunk, error) {
	request := *req
	request.Stream = true
	chain := []string{""}
	if c.variant == variantAntigravity {
		chain = antigravityFallbackChain(request.Model)
	}

	var lastErr error
	var resp *http.Response
	for _, override := range chain {
		body, _, err := c.wrapRequestWithConversationRepair(&request, creds, override, false)
		if err != nil {
			return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: request.Model, Message: err.Error(), Cause: err}
		}

		resp, err = c.openStreamWithTransientRetry(ctx, request.Model, creds, body)
		if c.variant == variantAntigravity && isAntigravityClaudePrefillError(err) {
			retryBody, repaired, renderErr := c.wrapRequestWithConversationRepair(&request, creds, override, true)
			if renderErr != nil {
				return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: request.Model, Message: renderErr.Error(), Cause: renderErr}
			}
			if repaired {
				resp, err = c.openStreamWithTransientRetry(ctx, request.Model, creds, retryBody)
			}
		}
		if err == nil {
			break
		}
		var pe *core.ProviderError
		if len(chain) > 1 && errors.As(err, &pe) && pe.Kind == core.ErrUpstream {
			lastErr = err
			continue
		}
		return nil, err
	}
	if resp == nil {
		return nil, lastErr
	}

	out := make(chan core.StreamChunk, 16)
	// SSE comment heartbeats are turned into ChunkPing by the pump so the
	// stall detector sees the connection is alive.
	go streamSSE(ctx, c.id, request.Model, resp.Body, out, cfg, sseDataLines, func(payload []byte) ([]core.StreamChunk, error) {
		return c.codec.ParseStreamLine(unwrapCloudCodeResponse(payload), request.Model)
	})
	return out, nil
}

func (c *CloudCode) openStreamWithTransientRetry(ctx context.Context, model string, creds core.Credentials, body []byte) (*http.Response, error) {
	var resp *http.Response
	var err error
	for openAttempt := 0; openAttempt < cloudCodeStreamOpenAttempts; openAttempt++ {
		resp, err = openStream(ctx, c.id, model, c.url(creds, true), body, c.headers(creds, true))
		if err == nil || !shouldRetryCloudCodeStreamOpen(ctx, err) || openAttempt+1 == cloudCodeStreamOpenAttempts {
			break
		}
		if err := waitForCloudCodeStreamRetry(ctx, cloudCodeStreamRetryDelay); err != nil {
			return nil, err
		}
	}
	return resp, err
}

// shouldRetryCloudCodeStreamOpen detects a connection reset reported by Go as
// context.Canceled even though our request context remains live. A cancelled
// client request must never be retried: the caller has already gone away.
func shouldRetryCloudCodeStreamOpen(ctx context.Context, err error) bool {
	return ctx.Err() == nil && errors.Is(err, context.Canceled)
}

func waitForCloudCodeStreamRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// generateCloudCodeProjectID builds a project id of the form
// "<adj>-<noun>-<5 hex>".
func generateCloudCodeProjectID() string {
	adjs := []string{"useful", "bright", "swift", "calm", "bold"}
	nouns := []string{"fuze", "wave", "spark", "flow", "core"}
	return adjs[randIndex(len(adjs))] + "-" + nouns[randIndex(len(nouns))] + "-" + randomHex(3)[:5]
}

// deriveCloudCodeSession derives a stable session id from the account identity
// (email/connection).
func deriveCloudCodeSession(creds core.Credentials) string {
	seed := creds.Extra["email"]
	if seed == "" {
		seed = creds.AccountID
	}
	if seed == "" {
		return uuid.NewString()
	}
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(seed)).String()
}

func randIndex(n int) int {
	if n <= 0 {
		return 0
	}
	b := make([]byte, 1)
	_, _ = rand.Read(b)
	return int(b[0]) % n
}
