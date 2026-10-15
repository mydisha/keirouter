package connectors

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/errclass"
)

// GigaChat drives Sber's GigaChat API. Its chat endpoint looks like Chat
// Completions but predates tools: functions are declared with the legacy
// "functions" list, the model answers with a single function_call whose
// arguments are a JSON object, results go back as role "function" messages,
// and nothing carries a tool-call id. The connector synthesizes stable ids on
// the way out and resolves them back to function names on the way in.
//
// Authentication exchanges the authorization key (base64 client_id:secret)
// for a 30-minute access token scoped to the account's API tier.
type GigaChat struct {
	id          string
	defaultBase string
}

// NewGigaChat builds a GigaChat connector.
func NewGigaChat(id, defaultBaseURL string) *GigaChat {
	base := defaultBaseURL
	if base == "" {
		base = gigaDefaultBase
	}
	return &GigaChat{id: id, defaultBase: base}
}

func init() {
	registerDialect(core.DialectGigaChat, func(id, base string) core.Connector { return NewGigaChat(id, base) })
	providerModels["gigachat"] = []ModelSpec{
		m("GigaChat-2", "GigaChat 2 Lite"),
		m("GigaChat-2-Pro", "GigaChat 2 Pro"),
		m("GigaChat-2-Max", "GigaChat 2 Max"),
		emb("Embeddings", "GigaChat Embeddings", 1024),
		emb("Embeddings-2", "GigaChat Embeddings 2", 1024),
		emb("EmbeddingsGigaR", "GigaChat Embeddings GigaR", 2560),
		emb("GigaEmbeddings-3B-2025-09", "GigaEmbeddings 3B", 2048),
	}
}

func (c *GigaChat) ID() string            { return c.id }
func (c *GigaChat) Dialect() core.Dialect { return core.DialectGigaChat }

const (
	gigaDefaultBase    = "https://gigachat.devices.sberbank.ru/api/v1"
	gigaDefaultAuthURL = "https://ngw.devices.sberbank.ru:9443/api/v2/oauth"
	gigaDefaultScope   = "GIGACHAT_API_PERS"
	gigaFileCacheMax   = 1024
	gigaFileCacheTTL   = 24 * time.Hour
)

func (c *GigaChat) baseURL(creds core.Credentials) string {
	if b := strings.TrimSpace(creds.BaseURL); b != "" {
		return strings.TrimRight(b, "/")
	}
	return strings.TrimRight(c.defaultBase, "/")
}

// ---- auth ---------------------------------------------------------------------

// authKey normalizes the authorization key: a "Basic " prefix is dropped and
// a raw client_id:client_secret pair is encoded.
func gigaAuthKey(creds core.Credentials) string {
	key := strings.TrimSpace(creds.APIKey)
	if len(key) > 6 && strings.EqualFold(key[:6], "basic ") {
		key = strings.TrimSpace(key[6:])
	}
	if strings.Contains(key, ":") {
		key = base64.StdEncoding.EncodeToString([]byte(key))
	}
	return key
}

func (c *GigaChat) tokenKey(creds core.Credentials) string {
	return tokenCacheKey("gigachat", extraOr(creds, "gigachat_auth_url", gigaDefaultAuthURL),
		extraOr(creds, "gigachat_scope", gigaDefaultScope), gigaAuthKey(creds))
}

// token returns an access token; minted reports whether this call ran the
// exchange. A ready access token on the credentials is used as-is.
func (c *GigaChat) token(ctx context.Context, creds core.Credentials) (tok string, minted bool, err error) {
	if creds.AccessToken != "" {
		return creds.AccessToken, true, nil
	}
	key := gigaAuthKey(creds)
	if key == "" {
		return "", false, &core.ProviderError{Kind: core.ErrAuth, Scope: core.FailureScopeAccount, Provider: c.id,
			Message: "GigaChat authorization key is missing"}
	}
	authURL := extraOr(creds, "gigachat_auth_url", gigaDefaultAuthURL)
	scope := extraOr(creds, "gigachat_scope", gigaDefaultScope)
	tok, err = sharedTokenCache.get(ctx, c.tokenKey(creds), func(ctx context.Context) (exchangedToken, error) {
		minted = true
		headers := map[string]string{
			"Authorization": "Basic " + key,
			"RqUID":         uuid.NewString(),
		}
		body, err := doFormPOST(ctx, c.id, "token", authURL, url.Values{"scope": {scope}}, headers)
		if err != nil {
			return exchangedToken{}, gigaTLSHint(asAuthError(c.id, err))
		}
		return gigaParseToken(c.id, body)
	})
	return tok, minted, err
}

// gigaParseToken reads the token response. The OAuth endpoint answers
// {access_token, expires_at(ms)}; some gateways use the short {tok, exp}.
func gigaParseToken(provider string, body []byte) (exchangedToken, error) {
	var short struct {
		Tok string          `json:"tok"`
		Exp json.RawMessage `json:"exp"`
	}
	if json.Unmarshal(body, &short) == nil && short.Tok != "" {
		t := exchangedToken{Value: short.Tok}
		if n, err := strconv.ParseInt(strings.Trim(string(short.Exp), `"`), 10, 64); err == nil && n > 0 {
			if n > 1e12 {
				t.ExpiresAt = time.UnixMilli(n)
			} else {
				t.ExpiresAt = time.Unix(n, 0)
			}
		}
		return t, nil
	}
	return parseTokenResponse(provider, body)
}

// gigaTLSHint explains the most common GigaChat transport failure: the
// endpoints use a certificate chained to the Russian Trusted Root CA, which
// stock trust stores do not include. Verification stays on.
func gigaTLSHint(err error) error {
	pe := core.AsProviderError(err)
	if pe == nil || pe.Cause == nil {
		return err
	}
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	if !errors.As(pe.Cause, &unknown) && !errors.As(pe.Cause, &invalid) && !errors.As(pe.Cause, &hostname) &&
		!strings.Contains(pe.Cause.Error(), "x509:") {
		return err
	}
	cp := *pe
	cp.Message = pe.Message + " (TLS verification failed: add the Russian Trusted Root CA to the server's trust store)"
	return &cp
}

// call runs fn with a bearer, retrying once with a fresh token when the API
// rejects a cached one with 401.
func (c *GigaChat) call(ctx context.Context, creds core.Credentials, fn func(tok string) error) error {
	for attempt := 0; ; attempt++ {
		tok, minted, err := c.token(ctx, creds)
		if err != nil {
			return err
		}
		err = fn(tok)
		if err == nil {
			return nil
		}
		if core.AsProviderError(err).StatusCode == http.StatusUnauthorized && !minted && attempt == 0 {
			sharedTokenCache.invalidate(c.tokenKey(creds))
			continue
		}
		return gigaTLSHint(err)
	}
}

func (c *GigaChat) headers(creds core.Credentials, tok string, req *core.ChatRequest) map[string]string {
	h := map[string]string{
		"Authorization": bearer(tok),
		"Accept":        "application/json",
	}
	// The session id lets GigaChat reuse the cached prompt prefix across turns
	// (reported as precached_prompt_tokens).
	if req != nil && req.Metadata.ContextAffinityKey != "" {
		h["X-Session-ID"] = req.Metadata.ContextAffinityKey
	}
	return mergeHeaders(h, creds.Headers)
}

// ---- request rendering --------------------------------------------------------

type gigaRequest struct {
	Model             string          `json:"model"`
	Messages          []gigaMessage   `json:"messages"`
	Temperature       *float64        `json:"temperature,omitempty"`
	TopP              *float64        `json:"top_p,omitempty"`
	MaxTokens         *int            `json:"max_tokens,omitempty"`
	Stream            bool            `json:"stream,omitempty"`
	RepetitionPenalty json.RawMessage `json:"repetition_penalty,omitempty"`
	ProfanityCheck    json.RawMessage `json:"profanity_check,omitempty"`
	Functions         []gigaFunction  `json:"functions,omitempty"`
	FunctionCall      any             `json:"function_call,omitempty"`
}

type gigaMessage struct {
	Role         string            `json:"role"`
	Content      string            `json:"content"`
	FunctionCall *gigaFunctionCall `json:"function_call,omitempty"`
	Name         string            `json:"name,omitempty"`
	Attachments  []string          `json:"attachments,omitempty"`
}

type gigaFunctionCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type gigaFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// gigaRendered is a rendered request plus whether a json_schema response
// format was emulated with a forced function (the answer is then unwrapped
// back to text).
type gigaRendered struct {
	body       []byte
	structured string // forced function name for structured output
}

// render builds the GigaChat body. upload attaches an inline image and
// returns its file id; it may be nil (images are then described in text).
func (c *GigaChat) render(ctx context.Context, req *core.ChatRequest, stream bool, upload func(ctx context.Context, m *core.MediaPayload) (string, error)) (gigaRendered, error) {
	out := gigaRequest{
		Model:     strings.TrimPrefix(req.Model, "gigachat/"),
		MaxTokens: req.MaxTokens,
		TopP:      req.TopP,
		Stream:    stream,
	}
	// GigaChat rejects temperature 0; greedy decoding is top_p 0.
	if req.Temperature != nil {
		if *req.Temperature == 0 {
			zero := 0.0
			out.TopP = &zero
		} else {
			out.Temperature = req.Temperature
		}
	}
	out.RepetitionPenalty = req.Extra["repetition_penalty"]
	out.ProfanityCheck = req.Extra["profanity_check"]

	for _, t := range req.Tools {
		if t.Name == "" || len(t.Raw) > 0 && len(t.Parameters) == 0 {
			continue
		}
		params := t.Parameters
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out.Functions = append(out.Functions, gigaFunction{Name: t.Name, Description: t.Description, Parameters: params})
	}
	if len(out.Functions) > 0 {
		out.FunctionCall = gigaFunctionChoice(req.ToolChoice)
	}

	var rendered gigaRendered
	if name, fn, ok := gigaStructuredOutput(req.ResponseFormat); ok {
		out.Functions = append(out.Functions, fn)
		out.FunctionCall = map[string]string{"name": name}
		rendered.structured = name
	}

	msgs, err := gigaRenderMessages(ctx, req, upload)
	if err != nil {
		return gigaRendered{}, err
	}
	out.Messages = msgs
	rendered.body, err = json.Marshal(out)
	return rendered, err
}

// gigaFunctionChoice maps the canonical tool_choice onto function_call:
// "none"/"auto" pass through, "required" degrades to "auto" (GigaChat has no
// equivalent) and a forced tool becomes {"name": ...}.
func gigaFunctionChoice(choice any) any {
	if choice == nil {
		return nil
	}
	var v any
	if raw, err := json.Marshal(choice); err != nil || json.Unmarshal(raw, &v) != nil {
		return nil
	}
	switch t := v.(type) {
	case string:
		switch t {
		case "none", "auto":
			return t
		case "required", "any":
			return "auto"
		}
	case map[string]any:
		if fn, ok := t["function"].(map[string]any); ok {
			if name, _ := fn["name"].(string); name != "" {
				return map[string]string{"name": name}
			}
		}
		if name, _ := t["name"].(string); name != "" {
			return map[string]string{"name": name}
		}
	}
	return nil
}

// gigaStructuredOutput emulates a json_schema response format with a forced
// function whose parameters are the schema.
func gigaStructuredOutput(raw json.RawMessage) (string, gigaFunction, bool) {
	if len(raw) == 0 {
		return "", gigaFunction{}, false
	}
	var rf struct {
		Type       string `json:"type"`
		JSONSchema struct {
			Name   string          `json:"name"`
			Schema json.RawMessage `json:"schema"`
		} `json:"json_schema"`
	}
	if json.Unmarshal(raw, &rf) != nil || rf.Type != "json_schema" {
		return "", gigaFunction{}, false
	}
	name := rf.JSONSchema.Name
	if name == "" {
		name = "structured_output"
	}
	schema := rf.JSONSchema.Schema
	if len(schema) == 0 {
		schema = json.RawMessage(`{}`)
	}
	return name, gigaFunction{Name: name, Description: "Output structured response: " + name, Parameters: schema}, true
}

// gigaRenderMessages converts the conversation. GigaChat accepts one system
// message, first; later system turns become user turns. An assistant turn
// with several tool calls is split into one function_call turn per call, each
// followed by its result, because GigaChat calls one function at a time.
func gigaRenderMessages(ctx context.Context, req *core.ChatRequest, upload func(ctx context.Context, m *core.MediaPayload) (string, error)) ([]gigaMessage, error) {
	// Results by call id and call names by id, for pairing.
	results := map[string]*core.ToolResult{}
	names := map[string]string{}
	for _, msg := range req.Messages {
		for _, p := range msg.Content {
			switch {
			case p.Type == core.PartToolResult && p.ToolResult != nil:
				results[p.ToolResult.CallID] = p.ToolResult
			case p.Type == core.PartToolCall && p.ToolCall != nil:
				names[p.ToolCall.ID] = p.ToolCall.Name
			}
		}
	}
	consumed := map[string]bool{}

	var out []gigaMessage
	var system []string
	if req.System != "" {
		system = append(system, req.System)
	}
	leading := true
	for _, msg := range req.Messages {
		role := msg.Role
		if role == core.RoleDeveloper {
			role = core.RoleSystem
		}
		if role == core.RoleSystem && leading {
			if t := gigaText(msg.Content); t != "" {
				system = append(system, t)
			}
			continue
		}
		if leading {
			leading = false
			if len(system) > 0 {
				out = append(out, gigaMessage{Role: "system", Content: strings.Join(system, "\n\n")})
			}
		}

		switch role {
		case core.RoleAssistant:
			text := gigaText(msg.Content)
			var calls []*core.ToolCall
			for _, p := range msg.Content {
				if p.Type == core.PartToolCall && p.ToolCall != nil {
					calls = append(calls, p.ToolCall)
				}
			}
			if len(calls) == 0 {
				if text != "" {
					out = append(out, gigaMessage{Role: "assistant", Content: text})
				}
				continue
			}
			for i, tc := range calls {
				am := gigaMessage{Role: "assistant", FunctionCall: &gigaFunctionCall{Name: tc.Name, Arguments: gigaArgumentsObject(tc.Arguments)}}
				if i == 0 {
					am.Content = text
				}
				out = append(out, am)
				// Every call but the last is answered inline so the next call
				// follows a result; the last result keeps its own position.
				if i < len(calls)-1 {
					if r, ok := results[tc.ID]; ok && !consumed[tc.ID] {
						consumed[tc.ID] = true
						out = append(out, gigaFunctionResult(r, tc.Name))
					}
				}
			}
		default:
			// User (and late system or tool) turns: results first, then text.
			for _, p := range msg.Content {
				if p.Type != core.PartToolResult || p.ToolResult == nil || consumed[p.ToolResult.CallID] {
					continue
				}
				consumed[p.ToolResult.CallID] = true
				out = append(out, gigaFunctionResult(p.ToolResult, names[p.ToolResult.CallID]))
			}
			if role == core.RoleTool {
				continue
			}
			um, ok, err := gigaUserMessage(ctx, msg.Content, upload)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, um)
			}
		}
	}
	if leading && len(system) > 0 {
		out = append(out, gigaMessage{Role: "system", Content: strings.Join(system, "\n\n")})
	}
	return out, nil
}

// gigaUserMessage renders a user turn: text joined by newlines, inline images
// uploaded and attached by file id.
func gigaUserMessage(ctx context.Context, parts []core.ContentPart, upload func(ctx context.Context, m *core.MediaPayload) (string, error)) (gigaMessage, bool, error) {
	var texts, attachments []string
	for _, p := range parts {
		switch p.Type {
		case core.PartText:
			texts = append(texts, p.Text)
		case core.PartImage:
			if p.Media == nil {
				continue
			}
			if p.Media.Data == "" || upload == nil {
				texts = append(texts, "[image omitted: GigaChat only accepts images sent inline]")
				continue
			}
			id, err := upload(ctx, p.Media)
			if err != nil {
				if ctx.Err() != nil {
					return gigaMessage{}, false, err
				}
				slog.Warn("gigachat image upload failed; sending the turn without it", "error", err)
				texts = append(texts, "[image omitted: upload failed]")
				continue
			}
			attachments = append(attachments, id)
		}
	}
	if len(texts) == 0 && len(attachments) == 0 {
		return gigaMessage{}, false, nil
	}
	return gigaMessage{Role: "user", Content: strings.Join(texts, "\n"), Attachments: attachments}, true, nil
}

// gigaFunctionResult renders a tool result. The content must be JSON; plain
// text is sent as a JSON string.
func gigaFunctionResult(r *core.ToolResult, name string) gigaMessage {
	content := r.Content
	if !json.Valid([]byte(content)) {
		b, _ := json.Marshal(content)
		content = string(b)
	}
	return gigaMessage{Role: "function", Name: name, Content: content}
}

func gigaText(parts []core.ContentPart) string {
	var texts []string
	for _, p := range parts {
		if p.Type == core.PartText && p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// gigaArgumentsObject returns tool arguments as the JSON object GigaChat
// expects; string-encoded or malformed arguments are decoded or replaced
// with an empty object.
func gigaArgumentsObject(raw json.RawMessage) json.RawMessage {
	s := bytes.TrimSpace(raw)
	if len(s) > 0 && s[0] == '"' {
		var inner string
		if json.Unmarshal(s, &inner) == nil {
			s = bytes.TrimSpace([]byte(inner))
		}
	}
	if len(s) == 0 || s[0] != '{' || !json.Valid(s) {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(s)
}

// ---- image upload ---------------------------------------------------------------

// gigaFileCache remembers uploaded image ids per account so a conversation
// does not re-upload the same image on every turn.
type gigaFileCache struct {
	mu      sync.Mutex
	entries map[string]gigaFileEntry
}

type gigaFileEntry struct {
	id      string
	expires time.Time
}

var gigaFiles = &gigaFileCache{entries: map[string]gigaFileEntry{}}

func (f *gigaFileCache) get(key string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.entries[key]
	if !ok || !time.Now().Before(e.expires) {
		return "", false
	}
	return e.id, true
}

func (f *gigaFileCache) put(key, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.entries) >= gigaFileCacheMax {
		for k := range f.entries {
			delete(f.entries, k)
			if len(f.entries) < gigaFileCacheMax/2 {
				break
			}
		}
	}
	f.entries[key] = gigaFileEntry{id: id, expires: time.Now().Add(gigaFileCacheTTL)}
}

// uploader returns the image upload function bound to one account and token.
func (c *GigaChat) uploader(creds core.Credentials, tok, model string) func(ctx context.Context, m *core.MediaPayload) (string, error) {
	return func(ctx context.Context, m *core.MediaPayload) (string, error) {
		data, err := base64.StdEncoding.DecodeString(m.Data)
		if err != nil {
			return "", fmt.Errorf("decode inline image: %w", err)
		}
		sum := sha256.Sum256(data)
		key := c.tokenKey(creds) + ":" + hex.EncodeToString(sum[:16])
		if id, ok := gigaFiles.get(key); ok {
			return id, nil
		}
		mime := m.MIMEType
		if mime == "" {
			mime = http.DetectContentType(data)
		}
		ext := "jpg"
		if i := strings.IndexByte(mime, '/'); i >= 0 && i < len(mime)-1 {
			ext = strings.SplitN(mime[i+1:], ";", 2)[0]
		}
		id, err := c.uploadFile(ctx, creds, tok, model, uuid.NewString()+"."+ext, mime, data)
		if err != nil {
			return "", err
		}
		gigaFiles.put(key, id)
		return id, nil
	}
}

// uploadFile posts one file to /files (purpose "general") and returns its id.
func (c *GigaChat) uploadFile(ctx context.Context, creds core.Credentials, tok, model, filename, mime string, data []byte) (string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	h.Set("Content-Type", mime)
	part, err := mw.CreatePart(h)
	if err == nil {
		_, err = part.Write(data)
	}
	if err == nil {
		err = mw.WriteField("purpose", "general")
	}
	if err == nil {
		err = mw.Close()
	}
	if err != nil {
		return "", err
	}
	payload, contentType := buf.Bytes(), mw.FormDataContentType()
	headers := c.headers(creds, tok, nil)
	build := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL(creds)+"/files", bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", contentType)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return req, nil
	}
	resp, err := sendRequest(ctx, c.id, model, build)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := readBodyLimited(c.id, model, resp)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", httpStatusError(c.id, model, resp, body)
	}
	var file struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body, &file) != nil || file.ID == "" {
		return "", fmt.Errorf("file upload returned no id")
	}
	return file.ID, nil
}

// ---- responses ------------------------------------------------------------------

type gigaUsage struct {
	PromptTokens          int `json:"prompt_tokens"`
	CompletionTokens      int `json:"completion_tokens"`
	TotalTokens           int `json:"total_tokens"`
	PrecachedPromptTokens int `json:"precached_prompt_tokens"`
}

// canonical folds precached tokens into the prompt count (GigaChat reports
// them separately) and surfaces them as cache reads.
func (u *gigaUsage) canonical() *core.Usage {
	if u == nil {
		return nil
	}
	return &core.Usage{
		PromptTokens:     u.PromptTokens + u.PrecachedPromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens + u.PrecachedPromptTokens,
		CachedTokens:     u.PrecachedPromptTokens,
		Source:           core.UsageSourceProvider,
	}
}

type gigaRespFunctionCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type gigaResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Created int64  `json:"created"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role             string                `json:"role"`
			Content          string                `json:"content"`
			FunctionCall     *gigaRespFunctionCall `json:"function_call"`
			FunctionsStateID string                `json:"functions_state_id"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *gigaUsage `json:"usage"`
}

func gigaFinish(r string) core.FinishReason {
	switch r {
	case "length":
		return core.FinishLength
	case "function_call":
		return core.FinishToolCalls
	case "blacklist":
		return core.FinishFilter
	default:
		return core.FinishStop
	}
}

// gigaArgsJSON returns function_call arguments as a JSON object text: an
// object is kept, a string holding JSON is decoded.
func gigaArgsJSON(raw json.RawMessage) json.RawMessage {
	s := bytes.TrimSpace(raw)
	if len(s) == 0 || bytes.Equal(s, []byte("null")) {
		return json.RawMessage(`{}`)
	}
	if s[0] == '"' {
		var inner string
		if json.Unmarshal(s, &inner) == nil {
			if t := bytes.TrimSpace([]byte(inner)); json.Valid(t) {
				return json.RawMessage(t)
			}
			b, _ := json.Marshal(map[string]string{"value": inner})
			return b
		}
	}
	return json.RawMessage(s)
}

// gigaCallID synthesizes a deterministic tool-call id. GigaChat has none, but
// the canonical pairing of calls and results needs one.
func gigaCallID(seed string, index int, name string, args []byte) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%d\x00%s\x00", seed, index, name)
	h.Write(args)
	return "call_" + hex.EncodeToString(h.Sum(nil)[:12])
}

func (c *GigaChat) parseResponse(body []byte, model, structured string) (*core.ChatResponse, error) {
	var raw gigaResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: model, Message: "unreadable GigaChat response: " + err.Error(), Cause: err}
	}
	if len(raw.Choices) == 0 {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: model, Message: "GigaChat response has no choices"}
	}
	choice := raw.Choices[0]
	// functions_state_id is unique per function call; without it the id is
	// derived from the response itself.
	seed := sapFirstNonBlank(choice.Message.FunctionsStateID, raw.ID+strconv.FormatInt(raw.Created, 10))
	resp := &core.ChatResponse{
		ID:           raw.ID,
		Model:        sapFirstNonBlank(raw.Model, model),
		Message:      core.Message{Role: core.RoleAssistant},
		FinishReason: gigaFinish(choice.FinishReason),
	}
	if resp.ID == "" {
		resp.ID = "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	}
	text := choice.Message.Content
	if fc := choice.Message.FunctionCall; fc != nil && fc.Name != "" {
		args := gigaArgsJSON(fc.Arguments)
		if structured != "" && fc.Name == structured {
			text = string(args)
			resp.FinishReason = core.FinishStop
		} else {
			if text != "" {
				resp.Message.Content = append(resp.Message.Content, core.ContentPart{Type: core.PartText, Text: text})
				text = ""
			}
			resp.Message.Content = append(resp.Message.Content, core.ContentPart{Type: core.PartToolCall, ToolCall: &core.ToolCall{
				ID: gigaCallID(seed, 0, fc.Name, args), Name: fc.Name, Arguments: args,
			}})
			resp.FinishReason = core.FinishToolCalls
		}
	}
	if text != "" {
		resp.Message.Content = append(resp.Message.Content, core.ContentPart{Type: core.PartText, Text: text})
	}
	if u := raw.Usage.canonical(); u != nil {
		resp.Usage = *u
	}
	return resp, nil
}

// gigaStreamState accumulates one stream: the function call (delivered whole
// with the function_call finish) and the held usage/finish tail.
type gigaStreamState struct {
	model      string
	structured string
	seed       string
	call       *gigaRespFunctionCall
	// structuredDone marks an emulated structured-output answer already
	// emitted as text, so its function_call finish reads as a stop.
	structuredDone bool
	tail           sapHeldTail
}

type gigaStreamChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Created int64  `json:"created"`
	Choices []struct {
		Delta struct {
			Content          string                `json:"content"`
			FunctionCall     *gigaRespFunctionCall `json:"function_call"`
			FunctionsStateID string                `json:"functions_state_id"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *gigaUsage `json:"usage"`
	// Error frames: {"status": 4xx, "message": "..."}.
	Status  int    `json:"status"`
	Message string `json:"message"`
}

func (s *gigaStreamState) parse(payload []byte, provider string) ([]core.StreamChunk, error) {
	if string(payload) == "[DONE]" {
		return nil, errStopStream
	}
	if pe, ok := errclass.StreamErrorFrame(payload); ok {
		pe.Provider, pe.Model = provider, s.model
		return []core.StreamChunk{{Type: core.ChunkError, Err: pe}}, nil
	}
	var raw gigaStreamChunk
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, err
	}
	if len(raw.Choices) == 0 && raw.Usage == nil && raw.Status >= 400 {
		pe := errclass.ClassifyErrorDetail("", raw.Status, "", sapFirstNonBlank(raw.Message, "GigaChat stream error"))
		pe.Provider, pe.Model = provider, s.model
		return []core.StreamChunk{{Type: core.ChunkError, Err: pe}}, nil
	}
	if s.seed == "" {
		s.seed = raw.ID + strconv.FormatInt(raw.Created, 10)
	}
	var out []core.StreamChunk
	if len(raw.Choices) > 0 {
		ch := raw.Choices[0]
		if ch.Delta.Content != "" {
			out = append(out, core.StreamChunk{Type: core.ChunkText, Delta: ch.Delta.Content})
		}
		if fc := ch.Delta.FunctionCall; fc != nil {
			if ch.Delta.FunctionsStateID != "" {
				s.seed = ch.Delta.FunctionsStateID
			}
			if s.call == nil {
				s.call = &gigaRespFunctionCall{}
			}
			if fc.Name != "" {
				s.call.Name = fc.Name
			}
			if len(bytes.TrimSpace(fc.Arguments)) > 0 {
				s.call.Arguments = fc.Arguments
			}
		}
		if ch.FinishReason != nil {
			out = append(out, s.flushCall()...)
			reason := gigaFinish(*ch.FinishReason)
			if reason == core.FinishToolCalls && s.structuredDone {
				reason = core.FinishStop
			}
			out = append(out, core.StreamChunk{Type: core.ChunkFinish, FinishReason: reason})
		}
	}
	if u := raw.Usage.canonical(); u != nil {
		out = append(out, core.StreamChunk{Type: core.ChunkUsage, Usage: u})
	}
	return s.tail.hold(out), nil
}

// flushCall emits the pending function call: as a tool call, or as text when
// it carries an emulated structured-output answer.
func (s *gigaStreamState) flushCall() []core.StreamChunk {
	fc := s.call
	if fc == nil || fc.Name == "" {
		s.call = nil
		return nil
	}
	s.call = nil
	args := gigaArgsJSON(fc.Arguments)
	if s.structured != "" && fc.Name == s.structured {
		s.structuredDone = true
		return []core.StreamChunk{{Type: core.ChunkText, Delta: string(args)}}
	}
	return []core.StreamChunk{{Type: core.ChunkToolCall, Index: 0, ToolCall: &core.ToolCall{
		ID: gigaCallID(s.seed, 0, fc.Name, args), Name: fc.Name, Arguments: args,
	}}}
}

// ---- Connector ------------------------------------------------------------------

// Chat performs a non-streaming GigaChat completion.
func (c *GigaChat) Chat(ctx context.Context, req *core.ChatRequest, creds core.Credentials) (*core.ChatResponse, error) {
	req.Stream = false
	var respBody []byte
	var structured string
	err := c.call(ctx, creds, func(tok string) error {
		r, err := c.render(ctx, req, false, c.uploader(creds, tok, req.Model))
		if err != nil {
			return &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
		}
		structured = r.structured
		respBody, err = doJSON(ctx, c.id, req.Model, c.baseURL(creds)+"/chat/completions", r.body, c.headers(creds, tok, req))
		return err
	})
	if err != nil {
		return nil, err
	}
	return c.parseResponse(respBody, req.Model, structured)
}

// Stream performs a streaming GigaChat completion (SSE).
func (c *GigaChat) Stream(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig) (<-chan core.StreamChunk, error) {
	req.Stream = true
	var resp *http.Response
	var structured string
	err := c.call(ctx, creds, func(tok string) error {
		r, err := c.render(ctx, req, true, c.uploader(creds, tok, req.Model))
		if err != nil {
			return &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
		}
		structured = r.structured
		resp, err = openStream(ctx, c.id, req.Model, c.baseURL(creds)+"/chat/completions", r.body, c.headers(creds, tok, req))
		return err
	})
	if err != nil {
		return nil, err
	}
	out := make(chan core.StreamChunk, 16)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		pump := newSSEPump(ctx, c.id, req.Model, out, cfg)
		state := &gigaStreamState{model: req.Model, structured: structured}
		pump.run(resp.Body, sseDataLines, func(payload []byte) ([]core.StreamChunk, error) {
			return state.parse(payload, c.id)
		})
		if !pump.errored && ctx.Err() == nil {
			pump.emitAll(state.end())
		}
	}()
	return out, nil
}

// end emits whatever the stream left pending: a function call that arrived
// without a finish frame, then usage and the finish reason.
func (s *gigaStreamState) end() []core.StreamChunk {
	out := s.flushCall()
	if len(out) > 0 && out[0].Type == core.ChunkToolCall && s.tail.finish == "" {
		s.tail.finish = core.FinishToolCalls
	}
	return append(out, s.tail.flush()...)
}

// Validate exchanges the authorization key for a token, then lists models.
// A rejected key, an unreachable (or untrusted) OAuth endpoint and a failing
// API each produce their own message.
func (c *GigaChat) Validate(ctx context.Context, creds core.Credentials) error {
	tok, _, err := c.token(ctx, creds)
	if err != nil {
		if validationAuthError(err) {
			return fmt.Errorf("validation failed for %s: the authorization key or scope was rejected: %w", c.id, err)
		}
		return fmt.Errorf("validation failed for %s: could not reach the GigaChat OAuth endpoint: %w", c.id, err)
	}
	if _, err := c.listModels(ctx, creds, tok); err != nil {
		return fmt.Errorf("validation failed for %s: %w", c.id, gigaTLSHint(err))
	}
	return nil
}

func (c *GigaChat) listModels(ctx context.Context, creds core.Credentials, tok string) ([]ModelSpec, error) {
	body, err := doJSONMethod(ctx, http.MethodGet, c.id, "models", c.baseURL(creds)+"/models", nil, c.headers(creds, tok, nil))
	if err != nil {
		return nil, err
	}
	var list struct {
		Data []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("decode GigaChat models: %w", err)
	}
	out := make([]ModelSpec, 0, len(list.Data))
	for _, d := range list.Data {
		if d.ID == "" {
			continue
		}
		if d.Type == "embedder" || strings.Contains(strings.ToLower(d.ID), "embedding") {
			out = append(out, ModelSpec{ID: d.ID, Name: d.ID, Kind: core.ServiceEmbedding})
			continue
		}
		out = append(out, ModelSpec{ID: d.ID, Name: d.ID, Kind: core.ServiceLLM})
	}
	return out, nil
}

// ListModels implements LiveModelSource via GET /models.
func (c *GigaChat) ListModels(ctx context.Context, creds core.Credentials) ([]ModelSpec, error) {
	ctx = egressCtx(ctx, creds)
	var models []ModelSpec
	err := c.call(ctx, creds, func(tok string) error {
		var lerr error
		models, lerr = c.listModels(ctx, creds, tok)
		return lerr
	})
	return models, err
}

// Embeddings implements core.MediaConnector via POST /embeddings.
func (c *GigaChat) Embeddings(ctx context.Context, req *core.EmbeddingRequest, creds core.Credentials) (*core.EmbeddingResponse, error) {
	body, err := json.Marshal(map[string]any{"model": strings.TrimPrefix(req.Model, "gigachat/"), "input": req.Input})
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	var respBody []byte
	err = c.call(ctx, creds, func(tok string) error {
		var eerr error
		respBody, eerr = doJSON(ctx, c.id, req.Model, c.baseURL(creds)+"/embeddings", body, c.headers(creds, tok, nil))
		return eerr
	})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Model string `json:"model"`
		Data  []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
			Usage     struct {
				PromptTokens int `json:"prompt_tokens"`
			} `json:"usage"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: req.Model, Message: "unreadable embeddings response: " + err.Error(), Cause: err}
	}
	out := &core.EmbeddingResponse{Model: sapFirstNonBlank(raw.Model, req.Model), Vectors: make([][]float32, len(raw.Data))}
	for i, d := range raw.Data {
		idx := d.Index
		if idx < 0 || idx >= len(out.Vectors) {
			idx = i
		}
		out.Vectors[idx] = d.Embedding
		out.Usage.PromptTokens += d.Usage.PromptTokens
	}
	out.Usage.TotalTokens = out.Usage.PromptTokens
	out.Usage.Source = core.UsageSourceProvider
	return out, nil
}
