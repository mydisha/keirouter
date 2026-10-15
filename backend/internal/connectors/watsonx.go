package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/errclass"
	"github.com/mydisha/keirouter/backend/internal/transform"
)

// Watsonx drives IBM watsonx.ai's text/chat API. The wire format is Chat
// Completions shaped with a few differences that are applied to the body the
// OpenAI codec renders: the model goes in model_id, the request is scoped to a
// project_id or space_id, the string forms of tool_choice move to
// tool_choice_option, and parameters the service rejects are dropped. Models
// named "deployment/<id>" call a deployed prompt/model endpoint instead.
//
// Authentication is an IBM Cloud IAM API key exchanged for a bearer token,
// a Cloud Pak for Data username + API key exchanged at /icp4d-api, or a
// ready-made bearer token.
type Watsonx struct {
	id          string
	defaultBase string
	codec       transform.OpenAICodec
}

// Credential field keys (see catalog_extended.go).
const (
	watsonxFieldRegion     = "watsonx_region"
	watsonxFieldURL        = "watsonx_url"
	watsonxFieldProjectID  = "watsonx_project_id"
	watsonxFieldSpaceID    = "watsonx_space_id"
	watsonxFieldAuth       = "watsonx_auth"
	watsonxFieldUsername   = "watsonx_username"
	watsonxFieldAPIVersion = "watsonx_api_version"

	watsonxDefaultAPIVersion = "2024-03-13"
	watsonxDeploymentPrefix  = "deployment/"
)

// watsonxIAMTokenURL is IBM Cloud's IAM token endpoint (a var for tests).
var watsonxIAMTokenURL = "https://iam.cloud.ibm.com/identity/token"

// NewWatsonx builds the watsonx.ai connector.
func NewWatsonx(id, defaultBaseURL string) *Watsonx {
	return &Watsonx{id: id, defaultBase: defaultBaseURL}
}

func init() {
	registerDialect(core.DialectWatsonx, func(id, base string) core.Connector { return NewWatsonx(id, base) })
	providerModels["watsonx"] = []ModelSpec{
		m("ibm/granite-4-h-small", "Granite 4.0 H Small"),
		m("ibm/granite-3-3-8b-instruct", "Granite 3.3 8B Instruct"),
		m("ibm/granite-3-2-8b-instruct", "Granite 3.2 8B Instruct"),
		m("ibm/granite-3-8b-instruct", "Granite 3.0 8B Instruct"),
		m("ibm/granite-vision-3-2-2b", "Granite Vision 3.2 2B"),
		m("openai/gpt-oss-120b", "gpt-oss-120b"),
		m("meta-llama/llama-4-maverick-17b-128e-instruct-fp8", "Llama 4 Maverick 17B 128E Instruct"),
		m("meta-llama/llama-3-3-70b-instruct", "Llama 3.3 70B Instruct"),
		m("meta-llama/llama-3-405b-instruct", "Llama 3.1 405B Instruct"),
		m("meta-llama/llama-3-2-90b-vision-instruct", "Llama 3.2 90B Vision Instruct"),
		m("meta-llama/llama-3-2-11b-vision-instruct", "Llama 3.2 11B Vision Instruct"),
		m("mistralai/mistral-medium-2505", "Mistral Medium 3"),
		m("mistralai/mistral-small-3-1-24b-instruct-2503", "Mistral Small 3.1 24B Instruct"),
		m("mistralai/mistral-large", "Mistral Large"),
	}
}

func (c *Watsonx) ID() string            { return c.id }
func (c *Watsonx) Dialect() core.Dialect { return core.DialectWatsonx }

// configError reports an account misconfiguration that no retry can fix.
func (c *Watsonx) configError(msg string) error {
	return &core.ProviderError{Kind: core.ErrAuth, Scope: core.FailureScopeAccount, Provider: c.id,
		StatusCode: http.StatusUnauthorized, Message: msg}
}

// baseURL resolves the service URL: an explicit per-account base URL, the
// custom (Cloud Pak for Data) URL, or the regional IBM Cloud endpoint.
func (c *Watsonx) baseURL(creds core.Credentials) (string, error) {
	if creds.BaseURL != "" {
		return strings.TrimRight(resolveURLPlaceholders(creds.BaseURL, creds.Extra), "/"), nil
	}
	region := strings.ToLower(extraValue(creds, watsonxFieldRegion))
	switch region {
	case "":
		if c.defaultBase != "" {
			return strings.TrimRight(c.defaultBase, "/"), nil
		}
		return "https://us-south.ml.cloud.ibm.com", nil
	case "custom":
		u := extraValue(creds, watsonxFieldURL)
		if u == "" {
			return "", c.configError("watsonx URL is required when the region is set to custom")
		}
		return strings.TrimRight(u, "/"), nil
	case "ap-south-1":
		return "https://ap-south-1.aws.wxai.ibm.com", nil
	}
	for _, r := range region {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return "", c.configError("invalid watsonx region " + fmt.Sprintf("%q", region))
		}
	}
	return "https://" + region + ".ml.cloud.ibm.com", nil
}

func (c *Watsonx) apiVersion(creds core.Credentials) string {
	return extraOr(creds, watsonxFieldAPIVersion, watsonxDefaultAPIVersion)
}

// chatURL returns the chat (or chat_stream) URL for a model and whether the
// model addresses a deployment.
func (c *Watsonx) chatURL(base, model, version string, stream bool) (string, bool) {
	op := "text/chat"
	if stream {
		op = "text/chat_stream"
	}
	q := "?version=" + url.QueryEscape(version)
	if id, ok := strings.CutPrefix(model, watsonxDeploymentPrefix); ok {
		return base + "/ml/v1/deployments/" + url.PathEscape(id) + "/" + op + q, true
	}
	return base + "/ml/v1/" + op + q, false
}

// scope returns the project_id / space_id member that scopes a foundation
// model call. A project wins when both are set.
func (c *Watsonx) scope(creds core.Credentials) (string, string, error) {
	if p := extraValue(creds, watsonxFieldProjectID); p != "" {
		return "project_id", p, nil
	}
	if s := extraValue(creds, watsonxFieldSpaceID); s != "" {
		return "space_id", s, nil
	}
	return "", "", c.configError("watsonx project ID or deployment space ID is required (set one on the account)")
}

func (c *Watsonx) apiKey(creds core.Credentials) string {
	if k := strings.TrimSpace(creds.APIKey); k != "" {
		return k
	}
	return strings.TrimSpace(creds.AccessToken)
}

// token returns the bearer token for the account and, for exchanged tokens,
// the cache key to invalidate when the service rejects it.
func (c *Watsonx) token(ctx context.Context, creds core.Credentials, base string) (string, string, error) {
	key := c.apiKey(creds)
	if key == "" {
		return "", "", c.configError("watsonx API key is required")
	}
	switch strings.ToLower(extraOr(creds, watsonxFieldAuth, "iam")) {
	case "bearer":
		return key, "", nil
	case "cp4d":
		user := extraValue(creds, watsonxFieldUsername)
		if user == "" {
			return "", "", c.configError("Cloud Pak for Data username is required")
		}
		cacheKey := tokenCacheKey("watsonx-cp4d", base, user, key)
		tok, err := sharedTokenCache.get(ctx, cacheKey, func(ctx context.Context) (exchangedToken, error) {
			body, _ := json.Marshal(map[string]string{"username": user, "api_key": key})
			raw, err := doJSON(ctx, c.id, "token", base+"/icp4d-api/v1/authorize", body,
				map[string]string{"Accept": "application/json"})
			if err != nil {
				return exchangedToken{}, asAuthError(c.id, err)
			}
			return parseTokenResponse(c.id, raw)
		})
		return tok, cacheKey, err
	default:
		iamURL := watsonxIAMTokenURL
		cacheKey := tokenCacheKey("watsonx-iam", iamURL, key)
		tok, err := sharedTokenCache.get(ctx, cacheKey, func(ctx context.Context) (exchangedToken, error) {
			return fetchFormToken(ctx, c.id, iamURL, url.Values{
				"grant_type": {"urn:ibm:params:oauth:grant-type:apikey"},
				"apikey":     {key},
			}, nil)
		})
		return tok, cacheKey, err
	}
}

func (c *Watsonx) headers(creds core.Credentials, token string) map[string]string {
	return mergeHeaders(map[string]string{
		"Authorization": bearer(token),
		"Accept":        "application/json",
	}, creds.Headers)
}

// authorized runs call with auth headers. When the service answers 401 to a
// cached exchanged token, the token is dropped and the call retried once with
// a fresh one.
func (c *Watsonx) authorized(ctx context.Context, creds core.Credentials, base string, call func(headers map[string]string) error) error {
	tok, cacheKey, err := c.token(ctx, creds, base)
	if err != nil {
		return err
	}
	err = call(c.headers(creds, tok))
	if err == nil || cacheKey == "" || ctx.Err() != nil || core.AsProviderError(err).StatusCode != http.StatusUnauthorized {
		return err
	}
	sharedTokenCache.invalidate(cacheKey)
	if tok, _, err = c.token(ctx, creds, base); err != nil {
		return err
	}
	return call(c.headers(creds, tok))
}

// watsonxChatParams are the top-level members text/chat accepts; everything
// else the OpenAI codec may render (stream, stream_options,
// parallel_tool_calls, user, logit_bias, vendor reasoning switches, ...) is
// dropped so the service does not reject the request.
var watsonxChatParams = map[string]bool{
	"messages": true, "tools": true, "tool_choice": true, "temperature": true, "top_p": true,
	"max_tokens": true, "stop": true, "seed": true, "n": true, "frequency_penalty": true,
	"presence_penalty": true, "logprobs": true, "top_logprobs": true, "response_format": true,
	"reasoning_effort": true,
}

// render builds the text/chat body for a request.
func (c *Watsonx) render(req *core.ChatRequest, creds core.Credentials, deployment bool) ([]byte, error) {
	r := *req
	r.Stream = false // the endpoint, not a body flag, selects streaming
	raw, err := c.codec.RenderRequestForProvider(&r, c.id)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	var in map[string]json.RawMessage
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	out := make(map[string]json.RawMessage, len(in)+3)
	for k, v := range in {
		if watsonxChatParams[k] {
			out[k] = v
		}
	}

	if msgs, ok := out["messages"]; ok && bytes.Contains(msgs, []byte(`"reasoning_content"`)) {
		out["messages"] = watsonxStripMessageReasoning(msgs)
	}
	if tools, ok := out["tools"]; ok {
		out["tools"] = watsonxSanitizeTools(tools)
	}
	if tc, ok := out["tool_choice"]; ok {
		var s string
		if json.Unmarshal(tc, &s) == nil {
			delete(out, "tool_choice")
			switch s {
			case "auto", "none", "required":
				out["tool_choice_option"] = tc
			}
		}
	}
	if effort := watsonxReasoningEffort(req.Reasoning); effort != "" {
		out["reasoning_effort"], _ = json.Marshal(effort)
	}

	if !deployment {
		// Deployments carry their own model and scope.
		key, val, err := c.scope(creds)
		if err != nil {
			return nil, err
		}
		out["model_id"], _ = json.Marshal(req.Model)
		out[key], _ = json.Marshal(val)
	}
	return json.Marshal(out)
}

// watsonxStripMessageReasoning removes echoed reasoning_content, which the
// service does not accept on input messages.
func watsonxStripMessageReasoning(raw json.RawMessage) json.RawMessage {
	var msgs []map[string]json.RawMessage
	if json.Unmarshal(raw, &msgs) != nil {
		return raw
	}
	for _, m := range msgs {
		delete(m, "reasoning_content")
	}
	b, err := json.Marshal(msgs)
	if err != nil {
		return raw
	}
	return b
}

// watsonxSanitizeTools drops "strict" and "additionalProperties" from tool
// definitions; the service's schema validation rejects them.
func watsonxSanitizeTools(raw json.RawMessage) json.RawMessage {
	var tools []any
	if json.Unmarshal(raw, &tools) != nil {
		return raw
	}
	for _, t := range tools {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		if fn, ok := tm["function"].(map[string]any); ok {
			delete(fn, "strict")
			if params, ok := fn["parameters"]; ok {
				fn["parameters"] = watsonxStripSchemaKeys(params)
			}
		}
	}
	b, err := json.Marshal(tools)
	if err != nil {
		return raw
	}
	return b
}

func watsonxStripSchemaKeys(v any) any {
	switch t := v.(type) {
	case map[string]any:
		delete(t, "additionalProperties")
		delete(t, "strict")
		for k, child := range t {
			t[k] = watsonxStripSchemaKeys(child)
		}
	case []any:
		for i, child := range t {
			t[i] = watsonxStripSchemaKeys(child)
		}
	}
	return v
}

// watsonxReasoningEffort maps the canonical effort to the low/medium/high
// knob reasoning models (gpt-oss) accept. Disabled or unset yields "".
func watsonxReasoningEffort(r *core.ReasoningConfig) string {
	if r == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(r.Effort)) {
	case "minimal", "low":
		return "low"
	case "medium", "", "auto", "adaptive":
		return "medium"
	case "high", "xhigh", "max":
		return "high"
	}
	return ""
}

// watsonxFinish maps watsonx's own stop reasons that the OpenAI vocabulary
// lacks; ok is false for the shared ones.
func watsonxFinish(reason string) (core.FinishReason, bool) {
	switch reason {
	case "max_tokens", "time_limit", "token_limit":
		return core.FinishLength, true
	}
	return "", false
}

// watsonxRawFinish reads choices[0].finish_reason from a response or chunk.
func watsonxRawFinish(body []byte) string {
	var p struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if json.Unmarshal(body, &p) != nil || len(p.Choices) == 0 {
		return ""
	}
	return p.Choices[0].FinishReason
}

// watsonxErrorEnvelope is the service's error body:
// {"errors":[{"code":"...","message":"..."}],"trace":"...","status_code":400}.
type watsonxErrorEnvelope struct {
	Errors []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	StatusCode int `json:"status_code"`
}

// watsonxStreamError classifies an in-band error event, if payload is one.
func watsonxStreamError(payload []byte) (*core.ProviderError, bool) {
	if !bytes.Contains(payload, []byte(`"errors"`)) {
		return nil, false
	}
	var env watsonxErrorEnvelope
	if json.Unmarshal(payload, &env) != nil || len(env.Errors) == 0 {
		return nil, false
	}
	e := env.Errors[0]
	msg := e.Message
	if msg == "" {
		msg = e.Code
	}
	code := env.StatusCode
	if code == 0 && strings.Contains(e.Code, "token_expired") {
		code = http.StatusUnauthorized
	}
	pe := errclass.ClassifyErrorDetail(e.Code, code, "", msg)
	if e.Code != "" && e.Message != "" {
		pe.Message = e.Code + ": " + e.Message
	}
	return pe, true
}

// Chat performs a non-streaming text/chat call.
func (c *Watsonx) Chat(ctx context.Context, req *core.ChatRequest, creds core.Credentials) (*core.ChatResponse, error) {
	base, err := c.baseURL(creds)
	if err != nil {
		return nil, err
	}
	endpoint, deployment := c.chatURL(base, req.Model, c.apiVersion(creds), false)
	body, err := c.render(req, creds, deployment)
	if err != nil {
		return nil, err
	}
	var raw []byte
	err = c.authorized(ctx, creds, base, func(h map[string]string) error {
		var cerr error
		raw, cerr = doJSON(ctx, c.id, req.Model, endpoint, body, h)
		return cerr
	})
	if err != nil {
		return nil, err
	}
	out, err := c.codec.ParseResponse(raw, req.Model)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	if fr, ok := watsonxFinish(watsonxRawFinish(raw)); ok {
		out.FinishReason = fr
	}
	return out, nil
}

// Stream performs a streaming text/chat_stream call (SSE).
func (c *Watsonx) Stream(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig) (<-chan core.StreamChunk, error) {
	base, err := c.baseURL(creds)
	if err != nil {
		return nil, err
	}
	endpoint, deployment := c.chatURL(base, req.Model, c.apiVersion(creds), true)
	body, err := c.render(req, creds, deployment)
	if err != nil {
		return nil, err
	}
	var resp *http.Response
	err = c.authorized(ctx, creds, base, func(h map[string]string) error {
		var serr error
		resp, serr = openStream(ctx, c.id, req.Model, endpoint, body, h)
		return serr
	})
	if err != nil {
		return nil, err
	}
	out := make(chan core.StreamChunk, 16)
	go c.pump(ctx, req.Model, resp.Body, out, cfg)
	return out, nil
}

func (c *Watsonx) pump(ctx context.Context, model string, body io.ReadCloser, out chan core.StreamChunk, cfg core.StreamConfig) {
	defer close(out)
	defer body.Close()
	p := newSSEPump(ctx, c.id, model, out, cfg)
	gate := &streamFinishGate{}
	p.run(body, sseDataLines, func(payload []byte) ([]core.StreamChunk, error) {
		if isStreamDoneSentinel(payload) {
			return gate.flush(nil), errStopStream
		}
		if pe, ok := watsonxStreamError(payload); ok {
			return []core.StreamChunk{{Type: core.ChunkError, Err: pe}}, nil
		}
		chunks, err := c.codec.ParseStreamLine(payload, model)
		if err != nil {
			return nil, err
		}
		for i := range chunks {
			if chunks[i].Type == core.ChunkFinish {
				if fr, ok := watsonxFinish(watsonxRawFinish(payload)); ok {
					chunks[i].FinishReason = fr
				}
			}
		}
		return gate.hold(chunks), nil
	})
	if !p.errored && ctx.Err() == nil {
		p.emitAll(gate.flush(nil))
	}
}

// Validate exchanges the key for a token (proving it), then sends a one-token
// chat to prove the token is accepted for the configured project or space.
func (c *Watsonx) Validate(ctx context.Context, creds core.Credentials) error {
	fail := func(err error) error { return fmt.Errorf("validation failed for %s: %w", c.id, err) }
	base, err := c.baseURL(creds)
	if err != nil {
		return fail(err)
	}
	if _, _, err := c.scope(creds); err != nil {
		return fail(err)
	}
	probeModel := firstCatalogModel(c.id)
	if probeModel == "" {
		probeModel = "ibm/granite-3-3-8b-instruct"
	}
	maxTokens := 1
	req := &core.ChatRequest{
		Model:     probeModel,
		Messages:  []core.Message{{Role: core.RoleUser, Content: []core.ContentPart{{Type: core.PartText, Text: "ping"}}}},
		MaxTokens: &maxTokens,
	}
	endpoint, _ := c.chatURL(base, probeModel, c.apiVersion(creds), false)
	body, err := c.render(req, creds, false)
	if err != nil {
		return fail(err)
	}
	err = c.authorized(ctx, creds, base, func(h map[string]string) error {
		_, cerr := doJSON(ctx, c.id, "validate", endpoint, body, h)
		return cerr
	})
	if err == nil {
		return nil
	}
	if isNonJSONResponseError(err) || validationAuthError(err) || !validationReachedUpstream(err) {
		return fail(err)
	}
	// A 4xx naming the project or space means the scope is wrong; anything
	// else (probe model not offered in this region, ...) still proves the
	// credential and scope were accepted.
	msg := strings.ToLower(core.AsProviderError(err).Message)
	if strings.Contains(msg, "project") || strings.Contains(msg, "space_id") || strings.Contains(msg, "space ") {
		return fail(err)
	}
	return nil
}

// ListModels lists the chat-capable foundation models the service offers.
func (c *Watsonx) ListModels(ctx context.Context, creds core.Credentials) ([]ModelSpec, error) {
	base, err := c.baseURL(creds)
	if err != nil {
		return nil, err
	}
	ctx = egressCtx(ctx, creds)
	endpoint := base + "/ml/v1/foundation_model_specs?version=" + url.QueryEscape(c.apiVersion(creds)) +
		"&filters=function_text_chat&limit=200"
	var raw []byte
	err = c.authorized(ctx, creds, base, func(h map[string]string) error {
		var gerr error
		raw, gerr = doJSONMethod(ctx, http.MethodGet, c.id, "models", endpoint, nil, h)
		return gerr
	})
	if err != nil {
		return nil, err
	}
	return parseWatsonxModelSpecs(raw, time.Now())
}

// parseWatsonxModelSpecs reads a foundation_model_specs page, skipping
// models already withdrawn.
func parseWatsonxModelSpecs(raw []byte, now time.Time) ([]ModelSpec, error) {
	var page struct {
		Resources []struct {
			ModelID   string `json:"model_id"`
			Label     string `json:"label"`
			Lifecycle []struct {
				ID        string `json:"id"`
				StartDate string `json:"start_date"`
			} `json:"lifecycle"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, fmt.Errorf("decode watsonx foundation model specs: %w", err)
	}
	out := make([]ModelSpec, 0, len(page.Resources))
	for _, r := range page.Resources {
		if r.ModelID == "" {
			continue
		}
		withdrawn := false
		for _, l := range r.Lifecycle {
			if l.ID != "withdrawn" {
				continue
			}
			if t, err := time.Parse("2006-01-02", l.StartDate); err != nil || !t.After(now) {
				withdrawn = true
			}
		}
		if withdrawn {
			continue
		}
		name := r.Label
		if name == "" {
			name = r.ModelID
		}
		out = append(out, ModelSpec{ID: r.ModelID, Name: name, Kind: core.ServiceLLM})
	}
	return out, nil
}
