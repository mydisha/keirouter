package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/errclass"
	"github.com/mydisha/keirouter/backend/internal/transform"
)

// SAP drives SAP Generative AI Hub through the AI Core orchestration service.
// Every request goes to a running orchestration deployment
// ({deploymentUrl}/v2/completion) whose body wraps the chat in a templating
// module config. Authentication is an OAuth client-credentials token from the
// subaccount's XSUAA tenant; every call carries the AI-Resource-Group header.
//
// The orchestration result is a Chat Completions object (final_result), so
// response and stream parsing reuse the OpenAI codec.
type SAP struct {
	id          string
	defaultBase string
	codec       transform.OpenAICodec
}

// NewSAP builds a SAP AI Core orchestration connector.
func NewSAP(id, defaultBaseURL string) *SAP {
	return &SAP{id: id, defaultBase: defaultBaseURL}
}

func init() {
	registerDialect(core.DialectSAP, func(id, base string) core.Connector { return NewSAP(id, base) })
	providerModels["sap"] = []ModelSpec{
		m("gpt-4o", "GPT-4o"),
		m("gpt-4o-mini", "GPT-4o mini"),
		m("gpt-4.1", "GPT-4.1"),
		m("gpt-4.1-mini", "GPT-4.1 mini"),
		m("gpt-5", "GPT-5"),
		m("gpt-5-mini", "GPT-5 mini"),
		m("o3", "o3"),
		m("o4-mini", "o4-mini"),
		m("anthropic--claude-4.5-sonnet", "Claude Sonnet 4.5"),
		m("anthropic--claude-4-sonnet", "Claude Sonnet 4"),
		m("anthropic--claude-4-opus", "Claude Opus 4"),
		m("anthropic--claude-3.7-sonnet", "Claude 3.7 Sonnet"),
		m("anthropic--claude-3.5-sonnet", "Claude 3.5 Sonnet"),
		m("anthropic--claude-3-haiku", "Claude 3 Haiku"),
		m("gemini-2.5-pro", "Gemini 2.5 Pro"),
		m("gemini-2.5-flash", "Gemini 2.5 Flash"),
		m("mistralai--mistral-large-instruct", "Mistral Large Instruct"),
		m("mistralai--mistral-small-instruct", "Mistral Small Instruct"),
		m("amazon--nova-pro", "Amazon Nova Pro"),
		m("amazon--nova-lite", "Amazon Nova Lite"),
		m("meta--llama3.1-70b-instruct", "Llama 3.1 70B Instruct"),
	}
}

func (c *SAP) ID() string            { return c.id }
func (c *SAP) Dialect() core.Dialect { return core.DialectSAP }

const (
	sapTokenPath          = "/oauth/token"
	sapDefaultGroup       = "default"
	sapDeploymentTTL      = 10 * time.Minute
	sapDeploymentCacheMax = 1024
	// sapClientType identifies the calling client to AI Core telemetry.
	sapClientType = "KeiRouter"
)

// sapConfig is the resolved connection material for one account.
type sapConfig struct {
	clientID      string
	clientSecret  string
	tokenURL      string // {auth_url}/oauth/token
	apiURL        string // AI API root, without the /v2 suffix
	resourceGroup string
	deploymentURL string // explicit orchestration deployment, optional
}

// sapServiceKey is the JSON service key AI Core issues. Some exports wrap the
// fields in a "credentials" object (VCAP binding shape).
type sapServiceKey struct {
	ClientID     string `json:"clientid"`
	ClientSecret string `json:"clientsecret"`
	URL          string `json:"url"`
	CertURL      string `json:"certurl"`
	Certificate  string `json:"certificate"`
	ServiceURLs  struct {
		AIAPIURL string `json:"AI_API_URL"`
	} `json:"serviceurls"`
	Credentials *sapServiceKey `json:"credentials"`
}

// resolveConfig merges the service key with the explicit form fields
// (explicit fields win) and checks that a client-credentials flow is possible.
func (c *SAP) resolveConfig(creds core.Credentials) (sapConfig, error) {
	var key sapServiceKey
	if raw := extraValue(creds, "sap_service_key"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &key); err != nil {
			return sapConfig{}, c.authErr("the SAP AI Core service key is not valid JSON")
		}
		if key.Credentials != nil && key.ClientID == "" {
			key = *key.Credentials
		}
	}
	cfg := sapConfig{
		clientID:      sapFirstNonBlank(extraValue(creds, "sap_client_id"), key.ClientID),
		clientSecret:  sapFirstNonBlank(extraValue(creds, "sap_client_secret"), key.ClientSecret),
		resourceGroup: extraOr(creds, "sap_resource_group", sapDefaultGroup),
		deploymentURL: strings.TrimRight(extraValue(creds, "sap_deployment_url"), "/"),
	}
	authURL := sapFirstNonBlank(extraValue(creds, "sap_auth_url"), key.URL)
	apiURL := sapFirstNonBlank(extraValue(creds, "sap_api_url"), strings.TrimSpace(creds.BaseURL), key.ServiceURLs.AIAPIURL, c.defaultBase)

	if cfg.clientSecret == "" && key.Certificate != "" {
		return sapConfig{}, c.authErr("certificate-based (x509) SAP AI Core service keys are not supported; create a service key that uses a client secret")
	}
	var missing []string
	if cfg.clientID == "" {
		missing = append(missing, "client id")
	}
	if cfg.clientSecret == "" {
		missing = append(missing, "client secret")
	}
	if authURL == "" {
		missing = append(missing, "auth URL")
	}
	if apiURL == "" {
		missing = append(missing, "AI API URL")
	}
	if len(missing) > 0 {
		return sapConfig{}, c.authErr("SAP AI Core credentials are incomplete: provide a service key or the " +
			strings.Join(missing, ", "))
	}
	cfg.tokenURL = sapTokenURL(authURL)
	cfg.apiURL = sapAPIRoot(apiURL)
	return cfg, nil
}

func (c *SAP) authErr(msg string) *core.ProviderError {
	return &core.ProviderError{Kind: core.ErrAuth, Scope: core.FailureScopeAccount, Provider: c.id, Message: msg}
}

// sapTokenURL appends the token path to an XSUAA tenant URL unless present.
func sapTokenURL(authURL string) string {
	authURL = strings.TrimRight(strings.TrimSpace(authURL), "/")
	if strings.HasSuffix(authURL, sapTokenPath) {
		return authURL
	}
	return authURL + sapTokenPath
}

// sapAPIRoot strips a trailing /v2 so paths can be joined uniformly.
func sapAPIRoot(apiURL string) string {
	apiURL = strings.TrimRight(strings.TrimSpace(apiURL), "/")
	return strings.TrimSuffix(apiURL, "/v2")
}

func sapFirstNonBlank(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// ---- auth and deployment discovery ------------------------------------------

func (c *SAP) tokenKey(cfg sapConfig) string {
	return tokenCacheKey("sap", cfg.tokenURL, cfg.clientID, cfg.clientSecret)
}

// token returns a bearer for cfg; minted reports whether this call performed
// the exchange (a rejected fresh token is not worth a second exchange).
func (c *SAP) token(ctx context.Context, cfg sapConfig) (tok string, minted bool, err error) {
	tok, err = sharedTokenCache.get(ctx, c.tokenKey(cfg), func(ctx context.Context) (exchangedToken, error) {
		minted = true
		form := url.Values{
			"grant_type":    {"client_credentials"},
			"client_id":     {cfg.clientID},
			"client_secret": {cfg.clientSecret},
		}
		return fetchFormToken(ctx, c.id, cfg.tokenURL, form, nil)
	})
	return tok, minted, err
}

func (c *SAP) headers(cfg sapConfig, creds core.Credentials, tok string) map[string]string {
	return mergeHeaders(map[string]string{
		"Authorization":     bearer(tok),
		"AI-Resource-Group": cfg.resourceGroup,
		"AI-Client-Type":    sapClientType,
	}, creds.Headers)
}

// sapDeploymentCache remembers the discovered orchestration deployment per
// account so discovery costs one listing call per TTL, not one per request.
type sapDeploymentCache struct {
	mu      sync.Mutex
	entries map[string]sapDeploymentEntry
	group   singleflight.Group
}

type sapDeploymentEntry struct {
	url     string
	expires time.Time
}

var sapDeployments = &sapDeploymentCache{entries: map[string]sapDeploymentEntry{}}

func (d *sapDeploymentCache) lookup(key string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.entries[key]
	if !ok || !time.Now().Before(e.expires) {
		return "", false
	}
	return e.url, true
}

func (d *sapDeploymentCache) store(key, u string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.entries) >= sapDeploymentCacheMax {
		now := time.Now()
		for k, e := range d.entries {
			if !now.Before(e.expires) {
				delete(d.entries, k)
			}
		}
		for k := range d.entries {
			if len(d.entries) < sapDeploymentCacheMax/2 {
				break
			}
			delete(d.entries, k)
		}
	}
	d.entries[key] = sapDeploymentEntry{url: u, expires: time.Now().Add(sapDeploymentTTL)}
}

func (d *sapDeploymentCache) invalidate(key string) {
	d.mu.Lock()
	delete(d.entries, key)
	d.mu.Unlock()
}

func (c *SAP) deploymentKey(cfg sapConfig) string {
	return tokenCacheKey("sap-deployment", cfg.apiURL, cfg.resourceGroup, cfg.clientID)
}

// sapDeployment is one entry of the AI Core deployments listing.
type sapDeployment struct {
	ID            string `json:"id"`
	DeploymentURL string `json:"deploymentUrl"`
	ScenarioID    string `json:"scenarioId"`
	Status        string `json:"status"`
	CreatedAt     string `json:"createdAt"`
}

// listOrchestrationDeployments returns the running orchestration deployments
// in the account's resource group, newest first.
func (c *SAP) listOrchestrationDeployments(ctx context.Context, cfg sapConfig, creds core.Credentials, tok string) ([]sapDeployment, error) {
	u := cfg.apiURL + "/v2/lm/deployments?scenarioId=orchestration&status=RUNNING"
	body, err := doJSONMethod(ctx, http.MethodGet, c.id, "deployments", u, nil, c.headers(cfg, creds, tok))
	if err != nil {
		return nil, err
	}
	var list struct {
		Resources []sapDeployment `json:"resources"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Message: "unreadable deployments listing: " + err.Error(), Cause: err}
	}
	out := list.Resources[:0]
	for _, d := range list.Resources {
		if d.DeploymentURL == "" {
			continue
		}
		if d.ScenarioID != "" && d.ScenarioID != "orchestration" {
			continue
		}
		if d.Status != "" && !strings.EqualFold(d.Status, "RUNNING") {
			continue
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool { return sapCreatedAt(out[i]).After(sapCreatedAt(out[j])) })
	return out, nil
}

func sapCreatedAt(d sapDeployment) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, d.CreatedAt)
	return t
}

// errNoDeployment reports a resource group without a running orchestration
// deployment: the credential works but there is nothing to route to.
func (c *SAP) errNoDeployment(cfg sapConfig, model string) *core.ProviderError {
	return &core.ProviderError{Kind: core.ErrModelUnavailable, Scope: core.FailureScopeAccount, Provider: c.id, Model: model,
		Message: fmt.Sprintf("no running orchestration deployment in resource group %q; deploy the orchestration scenario in SAP AI Core or set the deployment URL", cfg.resourceGroup)}
}

// deployment returns the orchestration deployment URL, discovering and
// caching it when the account does not pin one.
func (c *SAP) deployment(ctx context.Context, cfg sapConfig, creds core.Credentials, tok, model string) (string, error) {
	if cfg.deploymentURL != "" {
		return cfg.deploymentURL, nil
	}
	key := c.deploymentKey(cfg)
	if u, ok := sapDeployments.lookup(key); ok {
		return u, nil
	}
	v, err, _ := sapDeployments.group.Do(key, func() (any, error) {
		deps, err := c.listOrchestrationDeployments(ctx, cfg, creds, tok)
		if err != nil {
			return "", err
		}
		if len(deps) == 0 {
			return "", c.errNoDeployment(cfg, model)
		}
		u := strings.TrimRight(deps[0].DeploymentURL, "/")
		sapDeployments.store(key, u)
		return u, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

// call runs fn with a bearer and the orchestration deployment URL. A 401
// answered to a cached token drops it and retries once with a fresh one; a
// 404 from a discovered deployment forgets it so the next call rediscovers.
func (c *SAP) call(ctx context.Context, cfg sapConfig, creds core.Credentials, model string, fn func(tok, deploymentURL string) error) error {
	for attempt := 0; ; attempt++ {
		tok, minted, err := c.token(ctx, cfg)
		if err != nil {
			return err
		}
		dep, err := c.deployment(ctx, cfg, creds, tok, model)
		if err == nil {
			err = fn(tok, dep)
		}
		if err == nil {
			return nil
		}
		pe := core.AsProviderError(err)
		if pe.StatusCode == http.StatusNotFound && cfg.deploymentURL == "" {
			sapDeployments.invalidate(c.deploymentKey(cfg))
		}
		if pe.StatusCode == http.StatusUnauthorized && !minted && attempt == 0 {
			sharedTokenCache.invalidate(c.tokenKey(cfg))
			continue
		}
		return err
	}
}

// ---- request rendering ------------------------------------------------------

type sapRequest struct {
	Config sapOrchestrationConfig `json:"config"`
}

type sapOrchestrationConfig struct {
	Modules sapModules     `json:"modules"`
	Stream  *sapStreamOpts `json:"stream,omitempty"`
}

type sapStreamOpts struct {
	Enabled bool `json:"enabled"`
}

type sapModules struct {
	PromptTemplating sapPromptTemplating `json:"prompt_templating"`
}

type sapPromptTemplating struct {
	Prompt sapPrompt   `json:"prompt"`
	Model  sapModelRef `json:"model"`
}

type sapPrompt struct {
	Template       []sapMessage    `json:"template"`
	Tools          []sapTool       `json:"tools,omitempty"`
	ResponseFormat json.RawMessage `json:"response_format,omitempty"`
}

type sapModelRef struct {
	Name    string         `json:"name"`
	Params  map[string]any `json:"params"`
	Version string         `json:"version"`
}

type sapMessage struct {
	Role       string        `json:"role"`
	Content    any           `json:"content"`
	ToolCalls  []sapToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type sapContentPart struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	ImageURL     *sapImageURL    `json:"image_url,omitempty"`
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
}

type sapImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail"`
}

type sapToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type sapTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
		Strict      bool            `json:"strict"`
	} `json:"function"`
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
}

// sapPassthroughParams are client sampling knobs the orchestration service
// forwards to the model unchanged.
var sapPassthroughParams = []string{
	"frequency_penalty", "presence_penalty", "seed", "n", "logit_bias", "logprobs", "top_logprobs",
}

// renderRequest builds the orchestration body. The orchestration service has
// no tool_choice knob in its templating module, so a forced or disabled tool
// choice is not forwarded.
func (c *SAP) renderRequest(req *core.ChatRequest, stream bool) ([]byte, error) {
	params := map[string]any{}
	if req.MaxTokens != nil {
		params["max_tokens"] = *req.MaxTokens
	}
	if req.Temperature != nil {
		params["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		params["top_p"] = *req.TopP
	}
	if len(req.Stop) > 0 {
		params["stop"] = req.Stop
	}
	if req.ParallelToolCalls != nil && len(req.Tools) > 0 {
		params["parallel_tool_calls"] = *req.ParallelToolCalls
	}
	for _, k := range sapPassthroughParams {
		if raw, ok := req.Extra[k]; ok && len(raw) > 0 {
			params[k] = raw
		}
	}
	version := "latest"
	if raw, ok := req.Extra["model_version"]; ok {
		var v string
		if json.Unmarshal(raw, &v) == nil && v != "" {
			version = v
		}
	}

	prompt := sapPrompt{Template: sapRenderMessages(req), ResponseFormat: sapResponseFormat(req.ResponseFormat)}
	for _, t := range req.Tools {
		if t.Name == "" {
			continue
		}
		var tool sapTool
		tool.Type = "function"
		tool.Function.Name = t.Name
		tool.Function.Description = t.Description
		tool.Function.Parameters = sapToolParameters(t.Parameters)
		tool.CacheControl = t.CacheControl
		prompt.Tools = append(prompt.Tools, tool)
	}

	body := sapRequest{Config: sapOrchestrationConfig{Modules: sapModules{PromptTemplating: sapPromptTemplating{
		Prompt: prompt,
		Model:  sapModelRef{Name: req.Model, Params: params, Version: version},
	}}}}
	if stream {
		body.Config.Stream = &sapStreamOpts{Enabled: true}
	}
	return json.Marshal(body)
}

// sapToolParameters guarantees the object schema the orchestration service
// requires ("type":"object" with a properties map).
func sapToolParameters(raw json.RawMessage) json.RawMessage {
	var schema map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &schema) != nil || schema == nil {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	changed := false
	if _, ok := schema["type"]; !ok {
		schema["type"] = "object"
		changed = true
	}
	if _, ok := schema["properties"]; !ok {
		schema["properties"] = map[string]any{}
		changed = true
	}
	if !changed {
		return raw
	}
	out, _ := json.Marshal(schema)
	return out
}

// sapResponseFormat keeps the response formats the templating module accepts.
func sapResponseFormat(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var rf struct {
		Type       string          `json:"type"`
		JSONSchema json.RawMessage `json:"json_schema"`
	}
	if json.Unmarshal(raw, &rf) != nil {
		return nil
	}
	switch rf.Type {
	case "text", "json_object":
		out, _ := json.Marshal(map[string]string{"type": rf.Type})
		return out
	case "json_schema":
		if len(rf.JSONSchema) == 0 {
			return nil
		}
		return raw
	}
	return nil
}

// sapRenderMessages converts the canonical conversation to the template
// messages: system/developer text, user text and images, assistant text plus
// tool calls, and one tool message per tool result.
func sapRenderMessages(req *core.ChatRequest) []sapMessage {
	var out []sapMessage
	if req.System != "" {
		if len(req.SystemCacheControl) > 0 {
			out = append(out, sapMessage{Role: "system", Content: []sapContentPart{
				{Type: "text", Text: req.System, CacheControl: req.SystemCacheControl}}})
		} else {
			out = append(out, sapMessage{Role: "system", Content: req.System})
		}
	}
	for _, msg := range req.Messages {
		// Tool results become their own tool messages, ahead of any other
		// content that rode along in the same canonical message.
		var rest []core.ContentPart
		for _, p := range msg.Content {
			if p.Type == core.PartToolResult && p.ToolResult != nil {
				out = append(out, sapMessage{Role: "tool", ToolCallID: p.ToolResult.CallID, Content: p.ToolResult.Content})
				continue
			}
			rest = append(rest, p)
		}
		switch msg.Role {
		case core.RoleSystem, core.RoleDeveloper:
			if content, ok := sapTextContent(rest, false); ok {
				out = append(out, sapMessage{Role: string(msg.Role), Content: content})
			}
		case core.RoleAssistant:
			am := sapMessage{Role: "assistant", Content: ""}
			if content, ok := sapTextContent(rest, false); ok {
				am.Content = content
			}
			for _, p := range rest {
				if p.Type != core.PartToolCall || p.ToolCall == nil {
					continue
				}
				var tc sapToolCall
				tc.ID = p.ToolCall.ID
				tc.Type = "function"
				tc.Function.Name = p.ToolCall.Name
				tc.Function.Arguments = sapArgumentsString(p.ToolCall.Arguments)
				am.ToolCalls = append(am.ToolCalls, tc)
			}
			if am.Content == "" && len(am.ToolCalls) == 0 {
				continue
			}
			out = append(out, am)
		case core.RoleTool:
			// Tool results were emitted above; stray text has no home.
		default:
			if content, ok := sapTextContent(rest, true); ok {
				out = append(out, sapMessage{Role: "user", Content: content})
			}
		}
	}
	return out
}

// sapTextContent renders text (and, for user turns, image) parts. A single
// uncached text part stays a plain string; anything richer becomes a list.
func sapTextContent(parts []core.ContentPart, images bool) (any, bool) {
	var list []sapContentPart
	rich := false
	for _, p := range parts {
		switch p.Type {
		case core.PartText:
			list = append(list, sapContentPart{Type: "text", Text: p.Text, CacheControl: p.CacheControl})
			if len(p.CacheControl) > 0 {
				rich = true
			}
		case core.PartImage:
			if !images || p.Media == nil {
				continue
			}
			u := sapMediaURL(p.Media)
			if u == "" {
				continue
			}
			list = append(list, sapContentPart{Type: "image_url", ImageURL: &sapImageURL{URL: u, Detail: "auto"}, CacheControl: p.CacheControl})
			rich = true
		}
	}
	switch {
	case len(list) == 0:
		return nil, false
	case len(list) == 1 && !rich:
		return list[0].Text, true
	}
	return list, true
}

// sapMediaURL returns a remote URL as-is or inline data as a data: URL.
func sapMediaURL(m *core.MediaPayload) string {
	if m.URL != "" {
		return m.URL
	}
	if m.Data == "" {
		return ""
	}
	mime := m.MIMEType
	if mime == "" {
		mime = "image/png"
	}
	return "data:" + mime + ";base64," + m.Data
}

// sapArgumentsString renders canonical tool arguments as the JSON string the
// Chat Completions shape carries.
func sapArgumentsString(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return "{}"
	}
	if s[0] == '"' {
		var inner string
		if json.Unmarshal(raw, &inner) == nil {
			return inner
		}
	}
	return s
}

// ---- responses and errors ---------------------------------------------------

// sapEnvelope is an orchestration response or stream event. final_result is
// the Chat Completions object (or chunk); orchestration_result is the older
// name for the same thing. Errors come as {"error":{...}} or, in older
// deployments, as a top-level {code, message, location}.
type sapEnvelope struct {
	RequestID           string          `json:"request_id"`
	FinalResult         json.RawMessage `json:"final_result"`
	OrchestrationResult json.RawMessage `json:"orchestration_result"`
	Error               json.RawMessage `json:"error"`
	Code                json.RawMessage `json:"code"`
	Message             string          `json:"message"`
	Location            string          `json:"location"`
	Object              string          `json:"object"`
	Choices             json.RawMessage `json:"choices"`
}

func (e *sapEnvelope) result() json.RawMessage {
	if len(e.FinalResult) > 0 && string(e.FinalResult) != "null" {
		return e.FinalResult
	}
	if len(e.OrchestrationResult) > 0 && string(e.OrchestrationResult) != "null" {
		return e.OrchestrationResult
	}
	return nil
}

// providerError extracts an in-band error, or nil.
func (e *sapEnvelope) providerError(provider, model string) *core.ProviderError {
	code, msg, loc := 0, e.Message, e.Location
	if len(e.Code) > 0 {
		code = sapCode(e.Code)
	}
	raw := strings.TrimSpace(string(e.Error))
	switch {
	case raw == "" || raw == "null":
		if code == 0 && msg == "" {
			return nil
		}
	case raw[0] == '"':
		_ = json.Unmarshal(e.Error, &msg)
	case raw[0] == '{':
		var detail struct {
			Code     json.RawMessage `json:"code"`
			Message  string          `json:"message"`
			Location string          `json:"location"`
		}
		_ = json.Unmarshal(e.Error, &detail)
		code, msg, loc = sapCode(detail.Code), detail.Message, detail.Location
	}
	if msg == "" {
		msg = "orchestration request failed"
	}
	if loc != "" {
		msg = loc + ": " + msg
	}
	pe := errclass.ClassifyErrorDetail("", code, "", msg)
	pe.Provider, pe.Model = provider, model
	return pe
}

func sapCode(raw json.RawMessage) int {
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		fmt.Sscanf(s, "%d", &n)
	}
	return n
}

func (c *SAP) parseResponse(body []byte, model string) (*core.ChatResponse, error) {
	var env sapEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: model, Message: "unreadable orchestration response: " + err.Error(), Cause: err}
	}
	result := env.result()
	if result == nil {
		if pe := env.providerError(c.id, model); pe != nil {
			return nil, pe
		}
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: model, Message: "orchestration response has no final_result"}
	}
	resp, err := c.codec.ParseResponse(result, model)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: model, Message: err.Error(), Cause: err}
	}
	if resp.ID == "" {
		resp.ID = env.RequestID
	}
	return resp, nil
}

// parseStreamEvent turns one orchestration stream event into canonical chunks.
func (c *SAP) parseStreamEvent(payload []byte, model string) ([]core.StreamChunk, error) {
	if string(payload) == "[DONE]" {
		return nil, errStopStream
	}
	var env sapEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, err
	}
	if result := env.result(); result != nil {
		return c.codec.ParseStreamLine(result, model)
	}
	if pe := env.providerError(c.id, model); pe != nil {
		return []core.StreamChunk{{Type: core.ChunkError, Err: pe}}, nil
	}
	if len(env.Choices) > 0 && env.Object != "" {
		return c.codec.ParseStreamLine(payload, model)
	}
	return nil, nil
}

// ---- Connector ----------------------------------------------------------------

func sapCompletionURL(deploymentURL string) string {
	return strings.TrimRight(deploymentURL, "/") + "/v2/completion"
}

// Chat performs a non-streaming orchestration completion.
func (c *SAP) Chat(ctx context.Context, req *core.ChatRequest, creds core.Credentials) (*core.ChatResponse, error) {
	cfg, err := c.resolveConfig(creds)
	if err != nil {
		return nil, err
	}
	req.Stream = false
	body, err := c.renderRequest(req, false)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	var respBody []byte
	err = c.call(ctx, cfg, creds, req.Model, func(tok, dep string) error {
		var cerr error
		respBody, cerr = doJSON(ctx, c.id, req.Model, sapCompletionURL(dep), body, c.headers(cfg, creds, tok))
		return cerr
	})
	if err != nil {
		return nil, err
	}
	return c.parseResponse(respBody, req.Model)
}

// Stream performs a streaming orchestration completion. Usage and the finish
// reason arrive on the last result chunk; they are held back and emitted, in
// that order, once the upstream stream ends.
func (c *SAP) Stream(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig) (<-chan core.StreamChunk, error) {
	scfg, err := c.resolveConfig(creds)
	if err != nil {
		return nil, err
	}
	req.Stream = true
	body, err := c.renderRequest(req, true)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	var resp *http.Response
	err = c.call(ctx, scfg, creds, req.Model, func(tok, dep string) error {
		var serr error
		resp, serr = openStream(ctx, c.id, req.Model, sapCompletionURL(dep), body, c.headers(scfg, creds, tok))
		return serr
	})
	if err != nil {
		return nil, err
	}
	out := make(chan core.StreamChunk, 16)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		pump := newSSEPump(ctx, c.id, req.Model, out, cfg)
		var tail sapHeldTail
		pump.run(resp.Body, sseDataLines, func(payload []byte) ([]core.StreamChunk, error) {
			chunks, perr := c.parseStreamEvent(payload, req.Model)
			return tail.hold(chunks), perr
		})
		if !pump.errored && ctx.Err() == nil {
			pump.emitAll(tail.flush())
		}
	}()
	return out, nil
}

// sapHeldTail holds usage and finish chunks back until the stream ends so the
// consumer always sees content, then usage, then the finish.
type sapHeldTail struct {
	usage  *core.Usage
	finish core.FinishReason
	seen   bool
}

func (t *sapHeldTail) hold(chunks []core.StreamChunk) []core.StreamChunk {
	out := chunks[:0]
	for _, ch := range chunks {
		switch ch.Type {
		case core.ChunkUsage:
			if ch.Usage != nil {
				u := *ch.Usage
				t.usage = &u
			}
		case core.ChunkFinish:
			t.seen = true
			if ch.FinishReason != "" {
				t.finish = ch.FinishReason
			}
		default:
			out = append(out, ch)
		}
	}
	return out
}

func (t *sapHeldTail) flush() []core.StreamChunk {
	var out []core.StreamChunk
	if t.usage != nil {
		out = append(out, core.StreamChunk{Type: core.ChunkUsage, Usage: t.usage})
	}
	finish := t.finish
	if finish == "" {
		finish = core.FinishStop
	}
	return append(out, core.StreamChunk{Type: core.ChunkFinish, FinishReason: finish})
}

// Validate exchanges the client credentials for a token and lists the
// orchestration deployments, so a rejected secret, a resource group the key
// cannot read, and a group without a running orchestration deployment each
// produce their own message.
func (c *SAP) Validate(ctx context.Context, creds core.Credentials) error {
	cfg, err := c.resolveConfig(creds)
	if err != nil {
		return fmt.Errorf("validation failed for %s: %w", c.id, err)
	}
	tok, _, err := c.token(ctx, cfg)
	if err != nil {
		if validationAuthError(err) {
			return fmt.Errorf("validation failed for %s: the OAuth client credentials were rejected by the auth URL: %w", c.id, err)
		}
		return fmt.Errorf("validation failed for %s: could not reach the SAP auth URL: %w", c.id, err)
	}
	deps, err := c.listOrchestrationDeployments(ctx, cfg, creds, tok)
	if err != nil {
		if validationAuthError(err) {
			return fmt.Errorf("validation failed for %s: the AI API rejected the token for resource group %q: %w", c.id, cfg.resourceGroup, err)
		}
		return fmt.Errorf("validation failed for %s: could not list deployments on the AI API: %w", c.id, err)
	}
	if cfg.deploymentURL == "" && len(deps) == 0 {
		return fmt.Errorf("validation failed for %s: %w", c.id, c.errNoDeployment(cfg, ""))
	}
	if cfg.deploymentURL == "" {
		sapDeployments.store(c.deploymentKey(cfg), strings.TrimRight(deps[0].DeploymentURL, "/"))
	}
	return nil
}
