package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/errclass"
)

// Replicate drives Replicate's predictions API. A chat turn becomes one
// prediction: the conversation is flattened into the model's "prompt" (plus
// "system_prompt"), the prediction is created, and the output is collected
// either by holding the create call open (Prefer: wait) and polling until it
// settles, or by reading the prediction's server-sent event stream.
//
// Replicate language models have no native tool calling, so tools are not
// advertised; earlier tool calls and results are rendered into the prompt as
// text so the conversation keeps its context.
type Replicate struct {
	id          string
	defaultBase string

	// Polling cadence for predictions that are still running after the
	// create call returns: starts at pollMin, doubles up to pollMax, and
	// gives up after maxWait (cold boots of large models take minutes).
	pollMin time.Duration
	pollMax time.Duration
	maxWait time.Duration
	// usageTimeout bounds the follow-up fetch that reads token metrics after
	// a stream completes.
	usageTimeout time.Duration
}

const replicateDefaultBase = "https://api.replicate.com/v1"

func init() {
	registerDialect(core.DialectReplicate, func(id, base string) core.Connector { return NewReplicate(id, base) })
	providerModels["replicate"] = []ModelSpec{
		m("meta/meta-llama-3-70b-instruct", "Llama 3 70B Instruct"),
		m("meta/meta-llama-3-8b-instruct", "Llama 3 8B Instruct"),
		m("meta/meta-llama-3.1-405b-instruct", "Llama 3.1 405B Instruct"),
		m("deepseek-ai/deepseek-r1", "DeepSeek R1"),
		m("deepseek-ai/deepseek-v3", "DeepSeek V3"),
		m("deepseek-ai/deepseek-v3.1", "DeepSeek V3.1"),
		m("ibm-granite/granite-3.3-8b-instruct", "Granite 3.3 8B Instruct"),
		m("anthropic/claude-4-sonnet", "Claude Sonnet 4"),
		m("anthropic/claude-4.5-sonnet", "Claude Sonnet 4.5"),
		m("anthropic/claude-4.5-haiku", "Claude Haiku 4.5"),
		m("openai/gpt-5", "GPT-5"),
		m("openai/gpt-oss-120b", "gpt-oss-120b"),
		m("google/gemini-2.5-flash", "Gemini 2.5 Flash"),
		m("xai/grok-4", "Grok 4"),
		m("qwen/qwen3-235b-a22b-instruct-2507", "Qwen3 235B A22B Instruct"),
		m("mistralai/mixtral-8x7b-instruct-v0.1", "Mixtral 8x7B Instruct"),
	}
}

// NewReplicate builds a Replicate connector.
func NewReplicate(id, defaultBaseURL string) *Replicate {
	if defaultBaseURL == "" {
		defaultBaseURL = replicateDefaultBase
	}
	return &Replicate{
		id: id, defaultBase: defaultBaseURL,
		pollMin: 500 * time.Millisecond, pollMax: 2 * time.Second, maxWait: 10 * time.Minute,
		usageTimeout: 5 * time.Second,
	}
}

func (c *Replicate) ID() string            { return c.id }
func (c *Replicate) Dialect() core.Dialect { return core.DialectReplicate }

func (c *Replicate) base(creds core.Credentials) string {
	if creds.BaseURL != "" {
		return strings.TrimRight(resolveURLPlaceholders(creds.BaseURL, creds.Extra), "/")
	}
	return strings.TrimRight(c.defaultBase, "/")
}

func (c *Replicate) headers(creds core.Credentials) map[string]string {
	tok := strings.TrimSpace(creds.APIKey)
	if tok == "" {
		tok = strings.TrimSpace(creds.AccessToken)
	}
	return mergeHeaders(creds.Headers, map[string]string{"Authorization": bearer(tok)})
}

// ---- model addressing -------------------------------------------------------

var replicateVersionRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// replicateTarget is where a prediction is created: a model's or
// deployment's own predictions endpoint, or the generic one with a version.
type replicateTarget struct {
	path    string
	version string
}

// replicateTargetFor resolves a model id: "owner/name" (latest version of an
// official or community model), "owner/name:<version>" or a bare 64-hex
// version id (pinned version), or "deployments/owner/name".
func replicateTargetFor(model string) (replicateTarget, error) {
	m := strings.TrimSpace(model)
	if replicateVersionRE.MatchString(m) {
		return replicateTarget{path: "predictions", version: m}, nil
	}
	if name, ver, ok := strings.Cut(m, ":"); ok {
		if ver == "" || !replicateOwnerName(name) {
			return replicateTarget{}, fmt.Errorf("invalid Replicate model %q (expected owner/name:version)", model)
		}
		return replicateTarget{path: "predictions", version: ver}, nil
	}
	if rest, ok := strings.CutPrefix(m, "deployments/"); ok && replicateOwnerName(rest) {
		owner, name, _ := strings.Cut(rest, "/")
		return replicateTarget{path: "deployments/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/predictions"}, nil
	}
	if !replicateOwnerName(m) {
		return replicateTarget{}, fmt.Errorf("invalid Replicate model %q (expected owner/name)", model)
	}
	owner, name, _ := strings.Cut(m, "/")
	return replicateTarget{path: "models/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/predictions"}, nil
}

func replicateOwnerName(s string) bool {
	owner, name, ok := strings.Cut(s, "/")
	return ok && owner != "" && name != "" && !strings.Contains(name, "/")
}

// ---- input mapping ----------------------------------------------------------

type replicateCreate struct {
	Version string         `json:"version,omitempty"`
	Input   map[string]any `json:"input"`
	Stream  bool           `json:"stream"`
}

// replicatePassthrough lists sampling inputs forwarded verbatim from the
// client's extra parameters when present. Replicate ignores inputs a model
// does not declare, so forwarding is safe across model families.
var replicatePassthrough = []string{"top_k", "seed", "presence_penalty", "frequency_penalty", "min_tokens", "repetition_penalty"}

// replicateInput maps a canonical request onto the conventional input names
// of Replicate language models. max_tokens and max_new_tokens are both set:
// newer models read the former, older Llama-2-era models the latter.
func replicateInput(req *core.ChatRequest) map[string]any {
	prompt, system := replicatePrompt(req)
	in := map[string]any{"prompt": prompt}
	if system != "" {
		in["system_prompt"] = system
	}
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		in["max_tokens"] = *req.MaxTokens
		in["max_new_tokens"] = *req.MaxTokens
	}
	if req.Temperature != nil {
		in["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		in["top_p"] = *req.TopP
	}
	if len(req.Stop) > 0 {
		in["stop_sequences"] = strings.Join(req.Stop, ",")
	}
	for _, k := range replicatePassthrough {
		if raw, ok := req.Extra[k]; ok && len(bytes.TrimSpace(raw)) > 0 && string(raw) != "null" {
			in[k] = raw
		}
	}
	return in
}

// replicatePrompt flattens the conversation. System text goes to
// system_prompt. A single user turn is sent as-is (the model applies its own
// chat template around it); a multi-turn conversation becomes a labelled
// transcript that ends with an open "Assistant:" turn.
func replicatePrompt(req *core.ChatRequest) (prompt, system string) {
	var sys []string
	if s := strings.TrimSpace(req.System); s != "" {
		sys = append(sys, s)
	}
	type turn struct {
		label string
		text  string
	}
	var turns []turn
	for _, msg := range req.Messages {
		switch msg.Role {
		case core.RoleSystem, core.RoleDeveloper:
			if t := strings.TrimSpace(msg.TextContent()); t != "" {
				sys = append(sys, t)
			}
			continue
		}
		var parts []string
		for _, p := range msg.Content {
			switch p.Type {
			case core.PartText:
				if p.Text != "" {
					parts = append(parts, p.Text)
				}
			case core.PartToolCall:
				if p.ToolCall != nil {
					parts = append(parts, fmt.Sprintf("[called tool %s with %s]", p.ToolCall.Name, ociArgs(p.ToolCall.Arguments)))
				}
			case core.PartToolResult:
				if p.ToolResult != nil {
					turns = append(turns, turn{label: "Tool result", text: p.ToolResult.Content})
				}
			}
		}
		if len(parts) == 0 {
			continue
		}
		label := "User"
		switch msg.Role {
		case core.RoleAssistant:
			label = "Assistant"
		case core.RoleTool:
			label = "Tool result"
		}
		turns = append(turns, turn{label: label, text: strings.Join(parts, "\n")})
	}
	system = strings.Join(sys, "\n\n")
	if len(turns) == 1 && turns[0].label == "User" {
		return turns[0].text, system
	}
	var b strings.Builder
	for i, t := range turns {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(t.label)
		b.WriteString(": ")
		b.WriteString(t.text)
	}
	if len(turns) > 0 && turns[len(turns)-1].label != "Assistant" {
		b.WriteString("\n\nAssistant:")
	}
	return b.String(), system
}

// ---- predictions ------------------------------------------------------------

type replicatePrediction struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Output json.RawMessage `json:"output"`
	Error  json.RawMessage `json:"error"`
	URLs   struct {
		Get    string `json:"get"`
		Stream string `json:"stream"`
		Cancel string `json:"cancel"`
	} `json:"urls"`
	Metrics struct {
		InputTokenCount  *int `json:"input_token_count"`
		OutputTokenCount *int `json:"output_token_count"`
	} `json:"metrics"`
}

func replicateTerminal(status string) bool {
	switch status {
	case "succeeded", "failed", "canceled", "aborted":
		return true
	}
	return false
}

// outputText joins a prediction's output, which language models report as
// an array of token strings (or occasionally a single string).
func (p *replicatePrediction) outputText() string {
	raw := bytes.TrimSpace(p.Output)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) == nil {
		var b strings.Builder
		for _, it := range items {
			if json.Unmarshal(it, &s) == nil {
				b.WriteString(s)
			}
		}
		return b.String()
	}
	return string(raw)
}

func (p *replicatePrediction) usage() *core.Usage {
	if p.Metrics.InputTokenCount == nil && p.Metrics.OutputTokenCount == nil {
		return nil
	}
	u := &core.Usage{Source: core.UsageSourceProvider}
	if p.Metrics.InputTokenCount != nil {
		u.PromptTokens = *p.Metrics.InputTokenCount
	}
	if p.Metrics.OutputTokenCount != nil {
		u.CompletionTokens = *p.Metrics.OutputTokenCount
	}
	u.TotalTokens = u.PromptTokens + u.CompletionTokens
	return u
}

// failure maps a failed or canceled prediction to a ProviderError.
func (c *Replicate) failure(p *replicatePrediction, model string) *core.ProviderError {
	if p.Status == "canceled" || p.Status == "aborted" {
		return &core.ProviderError{Kind: core.ErrUpstream, Scope: core.FailureScopeRequest, Provider: c.id, Model: model,
			Message: "prediction was " + p.Status}
	}
	return c.classify(replicateErrorText(p.Error), model)
}

func (c *Replicate) classify(msg, model string) *core.ProviderError {
	if msg == "" {
		msg = "prediction failed"
	}
	pe := errclass.ClassifyErrorDetail("", 0, "", msg)
	pe.Provider, pe.Model = c.id, model
	return pe
}

// replicateErrorText extracts a message from an error value that may be a
// JSON string, an object with detail/message, or plain text.
func replicateErrorText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Detail  string `json:"detail"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		for _, v := range []string{obj.Detail, obj.Message, obj.Error} {
			if v != "" {
				return v
			}
		}
	}
	return string(raw)
}

// trustedURL reports whether a URL handed back by the API may receive the
// account's token: the configured API host, or an https Replicate host.
func (c *Replicate) trustedURL(creds core.Credentials, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return false
	}
	if b, err := url.Parse(c.base(creds)); err == nil && strings.EqualFold(u.Host, b.Host) && u.Scheme == b.Scheme {
		return true
	}
	host := strings.ToLower(u.Hostname())
	return u.Scheme == "https" && (host == "replicate.com" || strings.HasSuffix(host, ".replicate.com") ||
		strings.HasSuffix(host, ".replicate.net"))
}

// getURL returns the polling URL for a prediction.
func (c *Replicate) getURL(creds core.Credentials, p *replicatePrediction) string {
	if p.URLs.Get != "" && c.trustedURL(creds, p.URLs.Get) {
		return p.URLs.Get
	}
	if p.ID == "" {
		return ""
	}
	return c.base(creds) + "/predictions/" + url.PathEscape(p.ID)
}

func (c *Replicate) decode(body []byte, model string) (*replicatePrediction, error) {
	var p replicatePrediction
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: model, Message: "parse prediction: " + err.Error(), Cause: err}
	}
	return &p, nil
}

// create starts a prediction. wait asks the API to hold the call open until
// the prediction settles (up to the API's own limit).
func (c *Replicate) create(ctx context.Context, req *core.ChatRequest, creds core.Credentials, stream, wait bool) (*replicatePrediction, error) {
	target, err := replicateTargetFor(req.Model)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrBadRequest, Scope: core.FailureScopeRequest, Provider: c.id, Model: req.Model, Message: err.Error()}
	}
	body, err := json.Marshal(replicateCreate{Version: target.version, Input: replicateInput(req), Stream: stream})
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	headers := c.headers(creds)
	if wait {
		if secs := replicateWaitSeconds(ctx); secs > 0 {
			headers["Prefer"] = fmt.Sprintf("wait=%d", secs)
		}
	}
	respBody, err := doJSON(ctx, c.id, req.Model, c.base(creds)+"/"+target.path, body, headers)
	if err != nil {
		return nil, err
	}
	return c.decode(respBody, req.Model)
}

// replicateWaitSeconds sizes the Prefer: wait hold (the API allows 1-60s) so
// the create call answers before the request's header budget or deadline
// runs out; a prediction still running afterwards is polled. Zero disables
// the hold.
func replicateWaitSeconds(ctx context.Context) int {
	limit := 60 * time.Second
	if d := core.ResponseHeaderTimeoutFromContext(ctx); d > 0 && d < limit {
		limit = d
	}
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) < limit {
		limit = time.Until(dl)
	}
	secs := int((limit - 2*time.Second) / time.Second)
	if secs > 60 {
		secs = 60
	}
	if secs < 1 {
		return 0
	}
	return secs
}

// fetch reads the current state of a prediction.
func (c *Replicate) fetch(ctx context.Context, creds core.Credentials, model string, p *replicatePrediction) (*replicatePrediction, error) {
	u := c.getURL(creds, p)
	if u == "" {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: model, Message: "prediction has no id to poll"}
	}
	body, err := doJSONMethod(ctx, http.MethodGet, c.id, model, u, nil, c.headers(creds))
	if err != nil {
		return nil, err
	}
	return c.decode(body, model)
}

// await polls a prediction until it settles, calling onUpdate after every
// poll. Transient poll failures are tolerated a few times in a row; auth and
// request errors end the wait immediately. On cancellation or timeout the
// prediction is canceled upstream so it stops billing.
func (c *Replicate) await(ctx context.Context, creds core.Credentials, model string, p *replicatePrediction, onUpdate func(*replicatePrediction) bool) (*replicatePrediction, error) {
	deadline := time.Now().Add(c.maxWait)
	delay := c.pollMin
	transient := 0
	for !replicateTerminal(p.Status) {
		if time.Now().After(deadline) {
			c.cancel(ctx, creds, model, p)
			return nil, &core.ProviderError{Kind: core.ErrTimeout, Scope: core.FailureScopeRequest, Provider: c.id, Model: model,
				Message: fmt.Sprintf("prediction did not finish within %s", c.maxWait)}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			c.cancel(ctx, creds, model, p)
			return nil, transportError(ctx, c.id, model, ctx.Err())
		case <-timer.C:
		}
		if delay *= 2; delay > c.pollMax {
			delay = c.pollMax
		}
		next, err := c.fetch(ctx, creds, model, p)
		if err != nil {
			if ctx.Err() != nil {
				c.cancel(ctx, creds, model, p)
				return nil, err
			}
			pe := core.AsProviderError(err)
			if (pe.Kind == core.ErrUpstream || pe.Kind == core.ErrTimeout) && transient < 3 {
				transient++
				continue
			}
			return nil, err
		}
		transient = 0
		p = next
		if onUpdate != nil && !onUpdate(p) {
			return p, nil
		}
	}
	return p, nil
}

// cancel best-effort cancels a running prediction after the caller gave up.
func (c *Replicate) cancel(ctx context.Context, creds core.Credentials, model string, p *replicatePrediction) {
	u := p.URLs.Cancel
	if u == "" || !c.trustedURL(creds, u) {
		if p.ID == "" {
			return
		}
		u = c.base(creds) + "/predictions/" + url.PathEscape(p.ID) + "/cancel"
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, _ = doJSON(cctx, c.id, model, u, []byte("{}"), c.headers(creds))
}

// Chat creates a prediction, waits for it to settle and returns its output.
func (c *Replicate) Chat(ctx context.Context, req *core.ChatRequest, creds core.Credentials) (*core.ChatResponse, error) {
	p, err := c.create(ctx, req, creds, false, true)
	if err != nil {
		return nil, err
	}
	if p, err = c.await(ctx, creds, req.Model, p, nil); err != nil {
		return nil, err
	}
	if p.Status != "succeeded" {
		return nil, c.failure(p, req.Model)
	}
	resp := &core.ChatResponse{ID: p.ID, Model: req.Model, FinishReason: core.FinishStop,
		Message: core.Message{Role: core.RoleAssistant}}
	if text := p.outputText(); text != "" {
		resp.Message.Content = []core.ContentPart{{Type: core.PartText, Text: text}}
	}
	if u := p.usage(); u != nil {
		resp.Usage = *u
	}
	return resp, nil
}

// Stream creates a streaming prediction and relays its event stream. When
// the API offers no stream URL the prediction is polled instead and output
// deltas are emitted as they appear.
func (c *Replicate) Stream(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig) (<-chan core.StreamChunk, error) {
	p, err := c.create(ctx, req, creds, true, false)
	if err != nil {
		return nil, err
	}
	if p.Status == "failed" || p.Status == "canceled" || p.Status == "aborted" {
		return nil, c.failure(p, req.Model)
	}
	out := make(chan core.StreamChunk, 16)
	if p.URLs.Stream == "" || !c.trustedURL(creds, p.URLs.Stream) {
		go c.pollStream(ctx, creds, req.Model, p, out, cfg)
		return out, nil
	}
	resp, err := c.openEvents(ctx, creds, req.Model, p.URLs.Stream)
	if err != nil {
		c.cancel(ctx, creds, req.Model, p)
		return nil, err
	}
	go c.relayEvents(ctx, creds, req.Model, p, resp, out, cfg)
	return out, nil
}

func (c *Replicate) openEvents(ctx context.Context, creds core.Credentials, model, streamURL string) (*http.Response, error) {
	headers := c.headers(creds)
	build := func() (*http.Request, error) {
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
		if err != nil {
			return nil, err
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		r.Header.Set("Accept", "text/event-stream")
		r.Header.Set("Cache-Control", "no-store")
		return r, nil
	}
	resp, err := sendRequest(ctx, c.id, model, build)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, httpStatusError(c.id, model, resp, readErrorBody(resp))
	}
	return resp, nil
}

// replicateEvent is one dispatched server-sent event.
type replicateEvent struct {
	name string
	data string
}

// relayEvents reads the prediction's SSE stream. Unlike the JSON streams of
// other providers, "output" events carry raw text: a leading space is
// significant and multi-line text arrives as several data lines, so the
// events are assembled per the SSE rules rather than with the shared
// data-line parser.
func (c *Replicate) relayEvents(ctx context.Context, creds core.Credentials, model string, p *replicatePrediction, resp *http.Response, out chan core.StreamChunk, cfg core.StreamConfig) {
	defer close(out)
	defer resp.Body.Close()
	pump := newSSEPump(ctx, c.id, model, out, cfg)

	var ev replicateEvent
	var data []string
	hasData := false
	finished := false
	defer func() {
		// The client left mid-generation: stop the prediction so it does
		// not keep running (and billing) upstream.
		if !finished && ctx.Err() != nil {
			c.cancel(ctx, creds, model, p)
		}
	}()
	// dispatch handles a complete event; it returns false to stop reading.
	dispatch := func() bool {
		ev.data = strings.Join(data, "\n")
		defer func() { ev, data, hasData = replicateEvent{}, nil, false }()
		switch ev.name {
		case "", "output":
			if !hasData || ev.data == "" {
				return true
			}
			return pump.emit(core.StreamChunk{Type: core.ChunkText, Delta: ev.data})
		case "error":
			finished = true
			pump.emitError(c.classify(replicateErrorText(json.RawMessage(ev.data)), model))
			return false
		case "done":
			finished = true
			var done struct {
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal([]byte(ev.data), &done)
			final := c.finalState(ctx, creds, model, p)
			switch done.Reason {
			case "error", "failed":
				if final != nil && final.Status == "failed" {
					pump.emitError(c.failure(final, model))
				} else {
					pump.emitError(c.classify("", model))
				}
				return false
			case "canceled", "aborted":
				pump.emitError(c.failure(&replicatePrediction{Status: "canceled"}, model))
				return false
			}
			var tail []core.StreamChunk
			if final != nil {
				if u := final.usage(); u != nil {
					tail = append(tail, core.StreamChunk{Type: core.ChunkUsage, Usage: u})
				}
			}
			pump.emitAll(append(tail, core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishStop}))
			return false
		}
		return true
	}

	sc := sseScanner(resp.Body)
	for sc.Scan() {
		if ctx.Err() != nil {
			break
		}
		line := strings.TrimSuffix(sc.Text(), "\r")
		if line == "" {
			if (hasData || ev.name != "") && !dispatch() {
				return
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			if !pump.emit(core.StreamChunk{Type: core.ChunkPing}) {
				return
			}
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			ev.name = value
		case "data":
			data = append(data, value)
			hasData = true
		}
	}
	if ctx.Err() != nil {
		return
	}
	if err := sc.Err(); err != nil {
		if pe := pump.scanError(err); pe != nil {
			pump.emitError(pe)
		}
		return
	}
	if (hasData || ev.name != "") && !dispatch() {
		return
	}
	if !finished && !pump.errored {
		pump.emitError(&core.ProviderError{Kind: core.ErrUpstream, Scope: core.FailureScopeRequest, Provider: c.id, Model: model,
			Message: "prediction stream ended before the prediction finished"})
	}
}

// finalState fetches the settled prediction (for token metrics or the
// failure message), bounded so a slow API cannot hold the stream open.
func (c *Replicate) finalState(ctx context.Context, creds core.Credentials, model string, p *replicatePrediction) *replicatePrediction {
	fctx, cancel := context.WithTimeout(ctx, c.usageTimeout)
	defer cancel()
	final, err := c.fetch(fctx, creds, model, p)
	if err != nil {
		return nil
	}
	return final
}

// pollStream emulates streaming by polling the prediction and emitting the
// growth of its output between polls.
func (c *Replicate) pollStream(ctx context.Context, creds core.Credentials, model string, p *replicatePrediction, out chan core.StreamChunk, cfg core.StreamConfig) {
	defer close(out)
	pump := newSSEPump(ctx, c.id, model, out, cfg)
	sent := 0
	emitGrowth := func(cur *replicatePrediction) bool {
		text := cur.outputText()
		if len(text) <= sent {
			return true
		}
		delta := text[sent:]
		sent = len(text)
		return pump.emit(core.StreamChunk{Type: core.ChunkText, Delta: delta})
	}
	if !emitGrowth(p) {
		return
	}
	final, err := c.await(ctx, creds, model, p, emitGrowth)
	if err != nil {
		if ctx.Err() == nil {
			pump.emitError(err)
		}
		return
	}
	if ctx.Err() != nil {
		return
	}
	if final.Status != "succeeded" {
		pump.emitError(c.failure(final, model))
		return
	}
	var tail []core.StreamChunk
	if u := final.usage(); u != nil {
		tail = append(tail, core.StreamChunk{Type: core.ChunkUsage, Usage: u})
	}
	pump.emitAll(append(tail, core.StreamChunk{Type: core.ChunkFinish, FinishReason: core.FinishStop}))
}

// Validate reads the account behind the token: 401 means the token is
// rejected, a transport error that the endpoint is unreachable.
func (c *Replicate) Validate(ctx context.Context, creds core.Credentials) error {
	if strings.TrimSpace(creds.APIKey) == "" && strings.TrimSpace(creds.AccessToken) == "" {
		return fmt.Errorf("validation failed for %s: no API token", c.id)
	}
	if _, err := doJSONMethod(egressCtx(ctx, creds), http.MethodGet, c.id, "validate", c.base(creds)+"/account", nil, c.headers(creds)); err != nil {
		return fmt.Errorf("validation failed for %s: %w", c.id, err)
	}
	return nil
}
