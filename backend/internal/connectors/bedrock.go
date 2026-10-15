package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/errclass"
	"github.com/mydisha/keirouter/backend/internal/transform"
)

// Bedrock drives AWS Bedrock Runtime. Chat goes through the Converse API,
// which gives every hosted model family (Claude, Nova, Llama, Mistral,
// DeepSeek, gpt-oss, ...) one request shape. Model ids prefixed "invoke/"
// use InvokeModel with the native Anthropic Messages body instead.
// Requests are authenticated with a Bedrock API key (bearer) or SigV4.
type Bedrock struct {
	id          string
	defaultBase string
	codec       transform.AnthropicCodec
}

// NewBedrock builds a Bedrock connector. defaultBaseURL may carry the
// {aws_region} placeholder.
func NewBedrock(id, defaultBaseURL string) *Bedrock {
	return &Bedrock{id: id, defaultBase: defaultBaseURL}
}

func (c *Bedrock) ID() string            { return c.id }
func (c *Bedrock) Dialect() core.Dialect { return core.DialectBedrock }

const (
	// bedrockService is the SigV4 signing name of both the runtime and the
	// control-plane endpoints.
	bedrockService = "bedrock"
	// bedrockInvokePrefix routes a model through InvokeModel.
	bedrockInvokePrefix = "invoke/"
	// bedrockEventStream is the binary streaming content type.
	bedrockEventStream = "application/vnd.amazon.eventstream"
	// bedrockDefaultRuntime is used when no spec base URL was supplied.
	bedrockDefaultRuntime = "https://bedrock-runtime.{aws_region}.amazonaws.com"

	bedrockFieldGuardrailID      = "bedrock_guardrail_id"
	bedrockFieldGuardrailVersion = "bedrock_guardrail_version"
)

func init() {
	registerDialect(core.DialectBedrock, func(id, base string) core.Connector { return NewBedrock(id, base) })
}

// validAWSRegion keeps the region a plain identifier so it cannot reshape the
// endpoint host it is substituted into.
func validAWSRegion(r string) bool {
	if r == "" || len(r) > 32 {
		return false
	}
	for i := 0; i < len(r); i++ {
		ch := r[i]
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
			return false
		}
	}
	return true
}

func (c *Bedrock) region(creds core.Credentials) (string, error) {
	r := awsRegion(creds)
	if !validAWSRegion(r) {
		return "", &core.ProviderError{Kind: core.ErrBadRequest, Scope: core.FailureScopeAccount, Provider: c.id,
			Message: "invalid AWS region " + strconv.Quote(r)}
	}
	return r, nil
}

// runtimeBase is the Bedrock Runtime endpoint: the account override (VPC
// endpoint, gateway) or the spec URL with the region filled in.
func (c *Bedrock) runtimeBase(creds core.Credentials) (string, error) {
	region, err := c.region(creds)
	if err != nil {
		return "", err
	}
	u := c.defaultBase
	if creds.BaseURL != "" {
		u = creds.BaseURL
	}
	if u == "" {
		u = bedrockDefaultRuntime
	}
	u = strings.ReplaceAll(u, "{"+awsFieldRegion+"}", region)
	u = resolveURLPlaceholders(u, creds.Extra)
	return strings.TrimRight(u, "/"), nil
}

// controlBase is the Bedrock control-plane endpoint (model and profile
// listings). A runtime VPC endpoint does not serve it, so only a non-AWS
// override (a gateway in front of both planes) replaces the public host.
func (c *Bedrock) controlBase(creds core.Credentials) (string, error) {
	region, err := c.region(creds)
	if err != nil {
		return "", err
	}
	if creds.BaseURL != "" {
		u := resolveURLPlaceholders(strings.ReplaceAll(creds.BaseURL, "{"+awsFieldRegion+"}", region), creds.Extra)
		if parsed, perr := url.Parse(u); perr == nil && !strings.HasSuffix(parsed.Hostname(), ".amazonaws.com") {
			return strings.TrimRight(u, "/"), nil
		}
	}
	return "https://bedrock." + region + ".amazonaws.com", nil
}

// modelURL builds a runtime URL for one model action. The id is escaped as a
// single path segment: ids carry ':' and inference-profile ARNs carry '/'.
func (c *Bedrock) modelURL(creds core.Credentials, modelID, action string) (string, error) {
	base, err := c.runtimeBase(creds)
	if err != nil {
		return "", err
	}
	return base + "/model/" + awsPathEscape(modelID) + "/" + action, nil
}

// send authenticates and issues one request. Error statuses are drained,
// classified and closed here; on success the caller owns resp.Body.
func (c *Bedrock) send(ctx context.Context, creds core.Credentials, method, endpoint string, body []byte, accept, model string) (*http.Response, error) {
	headers := map[string]string{"Accept": accept}
	if body != nil {
		headers["Content-Type"] = "application/json"
	}
	signed, err := awsAuthHeaders(ctx, c.id, bedrockService, method, endpoint, body, headers, creds)
	if err != nil {
		return nil, err
	}
	build := func() (*http.Request, error) {
		var r io.Reader
		if body != nil {
			r = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, r)
		if err != nil {
			return nil, err
		}
		for k, v := range signed {
			req.Header.Set(k, v)
		}
		return req, nil
	}
	resp, err := sendRequest(ctx, c.id, model, build)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, bedrockHTTPError(c.id, model, resp, readErrorBody(resp))
	}
	return resp, nil
}

// call performs a buffered JSON request and returns the body and headers.
func (c *Bedrock) call(ctx context.Context, creds core.Credentials, method, endpoint string, body []byte, model string) ([]byte, http.Header, error) {
	resp, err := c.send(ctx, creds, method, endpoint, body, "application/json", model)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	b, err := readBodyLimited(c.id, model, resp)
	if err != nil {
		return nil, nil, err
	}
	if perr := checkNonJSONResponse(c.id, model, resp, b); perr != nil {
		return nil, nil, perr
	}
	return b, resp.Header, nil
}

// splitInvokeModel reports whether model asks for InvokeModel and returns the
// upstream model id.
func splitInvokeModel(model string) (string, bool) {
	if rest, ok := strings.CutPrefix(model, bedrockInvokePrefix); ok {
		return rest, true
	}
	return model, false
}

// Chat performs a non-streaming completion.
func (c *Bedrock) Chat(ctx context.Context, req *core.ChatRequest, creds core.Credentials) (*core.ChatResponse, error) {
	if modelID, invoke := splitInvokeModel(req.Model); invoke {
		return c.invokeChat(ctx, req, creds, modelID)
	}
	rendered, err := c.renderConverse(ctx, req, req.Model, creds)
	if err != nil {
		return nil, err
	}
	endpoint, err := c.modelURL(creds, req.Model, "converse")
	if err != nil {
		return nil, err
	}
	body, _, err := c.call(ctx, creds, http.MethodPost, endpoint, rendered.body, req.Model)
	if err != nil {
		return nil, err
	}
	resp, err := parseConverseResponse(body, req.Model, rendered.names)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Scope: core.FailureScopeRequest, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	return resp, nil
}

// Stream performs a streaming completion over the binary event stream.
func (c *Bedrock) Stream(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig) (<-chan core.StreamChunk, error) {
	if modelID, invoke := splitInvokeModel(req.Model); invoke {
		return c.invokeStream(ctx, req, creds, cfg, modelID)
	}
	rendered, err := c.renderConverse(ctx, req, req.Model, creds)
	if err != nil {
		return nil, err
	}
	endpoint, err := c.modelURL(creds, req.Model, "converse-stream")
	if err != nil {
		return nil, err
	}
	resp, err := c.send(ctx, creds, http.MethodPost, endpoint, rendered.body, bedrockEventStream, req.Model)
	if err != nil {
		return nil, err
	}
	out := make(chan core.StreamChunk, 16)
	state := newConverseStreamState(rendered.names)
	go c.pumpEventStream(ctx, req.Model, resp.Body, out, cfg, state)
	return out, nil
}

// bedrockFrameHandler turns decoded event-stream frames into chunks. finish
// is called once at a clean end of stream to flush the terminal chunks or
// report a truncated stream.
type bedrockFrameHandler interface {
	frame(f *eventStreamFrame) ([]core.StreamChunk, error)
	finish() ([]core.StreamChunk, error)
}

// pumpEventStream reads frames until the stream ends, the consumer leaves or
// an error is delivered. It owns body and out.
func (c *Bedrock) pumpEventStream(ctx context.Context, model string, body io.ReadCloser, out chan core.StreamChunk, cfg core.StreamConfig, h bedrockFrameHandler) {
	defer close(out)
	defer body.Close()
	pump := newSSEPump(ctx, c.id, model, out, cfg)
	parser := newEventStreamParser(body)
	for {
		if ctx.Err() != nil {
			return
		}
		frame, err := parser.next()
		if errors.Is(err, errEventStreamEOF) {
			break
		}
		if err != nil {
			if pe := pump.scanError(err); pe != nil {
				pump.emitError(pe)
			}
			return
		}
		if frame == nil {
			continue
		}
		if exc := bedrockFrameException(frame); exc != nil {
			pump.emitError(exc)
			return
		}
		chunks, err := h.frame(frame)
		if err != nil {
			pump.emitError(err)
			return
		}
		if !pump.emitAll(chunks) {
			return
		}
	}
	chunks, err := h.finish()
	if !pump.emitAll(chunks) {
		return
	}
	pump.emitError(err)
}

// Validate confirms the credential with a converse call to a model id that
// cannot exist: the request is authenticated before the id is checked, so a
// validation/not-found answer proves the key or signature was accepted while
// 401/403 rejects it and a transport failure reports an unreachable endpoint.
func (c *Bedrock) Validate(ctx context.Context, creds core.Credentials) error {
	const probeModel = "keirouter.validation-probe-v1:0"
	endpoint, err := c.modelURL(creds, probeModel, "converse")
	if err != nil {
		return err
	}
	body := []byte(`{"messages":[{"role":"user","content":[{"text":"ping"}]}],"inferenceConfig":{"maxTokens":1}}`)
	headers, err := awsAuthHeaders(ctx, c.id, bedrockService, http.MethodPost, endpoint, body,
		map[string]string{"Content-Type": "application/json", "Accept": "application/json"}, creds)
	if err != nil {
		return err
	}
	return validateProbe(ctx, c.id, endpoint, body, headers)
}

// ---- error classification -------------------------------------------------

// bedrockHTTPError classifies an error response, refining the generic status
// mapping with the AWS error type header and Bedrock's message vocabulary.
func bedrockHTTPError(provider, model string, resp *http.Response, body []byte) error {
	pe := core.AsProviderError(httpStatusError(provider, model, resp, body))
	errType := resp.Header.Get("X-Amzn-ErrorType")
	if i := strings.IndexByte(errType, ':'); i >= 0 {
		errType = errType[:i]
	}
	if errType == "" {
		var payload struct {
			Type string `json:"__type"`
		}
		if json.Unmarshal(body, &payload) == nil {
			errType = payload.Type
			if i := strings.LastIndexByte(errType, '#'); i >= 0 {
				errType = errType[i+1:]
			}
		}
	}
	return refineBedrockError(pe, errType, string(body))
}

// bedrockModelUnavailablePhrases mark answers about the model rather than the
// request or the credential: no model access granted, an unknown id, a model
// that needs an inference profile, or a retired version.
var bedrockModelUnavailablePhrases = []string{
	"access to the model", "model identifier is invalid", "on-demand throughput", "end of its life",
}

// refineBedrockError adjusts a classified error using the AWS exception type
// and message. Model-access and invalid-id answers are model-scoped: they
// must not bench a credential that still serves other models.
func refineBedrockError(pe *core.ProviderError, errType, msg string) *core.ProviderError {
	set := func(kind core.ErrorKind, scope core.FailureScope) { pe.Kind, pe.Scope = kind, scope }
	switch strings.ToLower(errType) {
	case "throttlingexception", "servicequotaexceededexception", "toomanyrequestsexception":
		if pe.Kind != core.ErrRateLimit && pe.Kind != core.ErrQuotaExhausted {
			set(core.ErrRateLimit, core.FailureScopeAccount)
		}
	case "modelnotreadyexception", "serviceunavailableexception", "internalserverexception",
		"modelerrorexception", "modelstreamerrorexception":
		set(core.ErrUpstream, core.FailureScopeProvider)
	case "modeltimeoutexception":
		set(core.ErrTimeout, core.FailureScopeProvider)
	case "resourcenotfoundexception":
		set(core.ErrModelUnavailable, core.FailureScopeModel)
	}
	lower := strings.ToLower(msg)
	for _, phrase := range bedrockModelUnavailablePhrases {
		if strings.Contains(lower, phrase) {
			set(core.ErrModelUnavailable, core.FailureScopeModel)
			break
		}
	}
	return pe
}

// bedrockFrameException converts an in-band exception or error frame into a
// ProviderError, or returns nil for an ordinary event.
func bedrockFrameException(f *eventStreamFrame) *core.ProviderError {
	msgType := strings.ToLower(f.headers[":message-type"])
	if msgType != "exception" && msgType != "error" {
		return nil
	}
	errType := f.headers[":exception-type"]
	if errType == "" {
		errType = f.headers[":error-code"]
	}
	var payload struct {
		Message      string `json:"message"`
		MessageUpper string `json:"Message"`
	}
	_ = json.Unmarshal(f.payload, &payload)
	msg := bedrockFirstNonEmpty(payload.Message, payload.MessageUpper, f.headers[":error-message"], strings.TrimSpace(string(f.payload)))
	if msg == "" {
		msg = "bedrock stream error"
	}

	pe := &core.ProviderError{Message: msg}
	if errType != "" {
		pe.Message = errType + ": " + msg
	}
	switch strings.ToLower(errType) {
	case "throttlingexception", "servicequotaexceededexception":
		pe.Kind, pe.Scope, pe.StatusCode = core.ErrRateLimit, core.FailureScopeAccount, http.StatusTooManyRequests
	case "accessdeniedexception", "unrecognizedclientexception", "expiredtokenexception":
		pe.Kind, pe.Scope, pe.StatusCode = core.ErrAuth, core.FailureScopeAccount, http.StatusForbidden
	case "validationexception":
		pe.StatusCode = http.StatusBadRequest
		pe.Kind, pe.Scope = core.ErrBadRequest, core.FailureScopeRequest
		if errclass.LooksLikeContextWindow(msg) {
			pe.Kind, pe.Scope = core.ErrContextWindow, core.FailureScopeModel
		} else if errclass.LooksLikeContentFilter(msg) {
			pe.Kind = core.ErrContentFilter
		}
	case "modeltimeoutexception":
		pe.Kind, pe.Scope, pe.StatusCode = core.ErrTimeout, core.FailureScopeProvider, http.StatusRequestTimeout
	case "serviceunavailableexception", "modelnotreadyexception":
		pe.Kind, pe.Scope, pe.StatusCode = core.ErrUpstream, core.FailureScopeProvider, http.StatusServiceUnavailable
	case "modelstreamerrorexception", "modelerrorexception":
		pe.Kind, pe.Scope, pe.StatusCode = core.ErrUpstream, core.FailureScopeProvider, http.StatusFailedDependency
	default:
		pe.Kind, pe.Scope, pe.StatusCode = core.ErrUpstream, core.FailureScopeProvider, http.StatusInternalServerError
	}
	return refineBedrockError(pe, "", msg)
}

func bedrockFirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
