package connectors

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/errclass"
	"github.com/mydisha/keirouter/backend/internal/transform"
)

// SageMaker drives AWS SageMaker real-time inference endpoints whose
// containers accept the Chat Completions message format (TGI messages API,
// LMI, vLLM, SGLang). The model is the endpoint name; every call is SigV4
// signed for the "sagemaker" service.
//
// Non-streaming calls POST /endpoints/{name}/invocations and get a Chat
// Completions JSON body back. Streaming calls POST
// /endpoints/{name}/invocations-response-stream, which answers with an AWS
// binary eventstream: each PayloadPart event carries a raw slice of the
// container's own SSE output, so the slices are reassembled into lines before
// the "data:" payloads reach the OpenAI codec.
type SageMaker struct {
	id          string
	defaultBase string
	codec       transform.OpenAICodec
}

const (
	sageMakerDefaultBase             = "https://runtime.sagemaker.{aws_region}.amazonaws.com"
	sageMakerFieldInferenceComponent = "sagemaker_inference_component"
	sageMakerInferenceComponentHdr   = "X-Amzn-SageMaker-Inference-Component"
)

// NewSageMaker builds the SageMaker runtime connector.
func NewSageMaker(id, defaultBaseURL string) *SageMaker {
	if defaultBaseURL == "" {
		defaultBaseURL = sageMakerDefaultBase
	}
	return &SageMaker{id: id, defaultBase: defaultBaseURL}
}

func init() {
	registerDialect(core.DialectSageMaker, func(id, base string) core.Connector { return NewSageMaker(id, base) })
}

func (c *SageMaker) ID() string            { return c.id }
func (c *SageMaker) Dialect() core.Dialect { return core.DialectSageMaker }

// baseURL resolves the runtime endpoint for the account's region. A
// per-account base URL (VPC interface endpoint, private gateway) wins.
func (c *SageMaker) baseURL(creds core.Credentials) string {
	region := awsRegion(creds)
	u := c.defaultBase
	if creds.BaseURL != "" {
		u = creds.BaseURL
	} else if strings.HasPrefix(region, "cn-") {
		// China partition endpoints live under amazonaws.com.cn.
		u = strings.Replace(u, ".amazonaws.com", ".amazonaws.com.cn", 1)
	}
	u = strings.ReplaceAll(u, "{aws_region}", region)
	return strings.TrimRight(resolveURLPlaceholders(u, creds.Extra), "/")
}

// invokeURL is the invocation URL for one endpoint.
func (c *SageMaker) invokeURL(creds core.Credentials, endpoint string, stream bool) string {
	op := "invocations"
	if stream {
		op = "invocations-response-stream"
	}
	return joinURL(c.baseURL(creds), "endpoints/"+awsPathEscape(endpoint)+"/"+op)
}

// render produces the Chat Completions body for the container. Streaming
// requests also ask for the trailing usage chunk (vLLM, SGLang and TGI honour
// it; servers that do not know the option ignore it).
func (c *SageMaker) render(req *core.ChatRequest, creds core.Credentials, stream bool) ([]byte, error) {
	r := *req
	r.Stream = stream
	// The URL addresses the endpoint; containers that validate the body's
	// model (vLLM, SGLang) need their served model name instead.
	if name := extraValue(creds, "sagemaker_model_name"); name != "" {
		r.Model = name
	}
	body, err := c.codec.RenderRequestForProvider(&r, c.id)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	if stream {
		body = spliceJSONObjectMember(body, "stream_options", []byte(`{"include_usage":true}`))
	}
	return body, nil
}

// spliceJSONObjectMember appends one member to an encoded JSON object.
func spliceJSONObjectMember(obj []byte, key string, value []byte) []byte {
	obj = bytes.TrimSpace(obj)
	if len(obj) < 2 || obj[len(obj)-1] != '}' {
		return obj
	}
	kb, _ := json.Marshal(key)
	out := make([]byte, 0, len(obj)+len(kb)+len(value)+2)
	out = append(out, obj[:len(obj)-1]...)
	if len(bytes.TrimSpace(obj[1:len(obj)-1])) > 0 {
		out = append(out, ',')
	}
	out = append(out, kb...)
	out = append(out, ':')
	out = append(out, value...)
	return append(out, '}')
}

// send signs and posts one invocation, mapping SageMaker error envelopes to
// ProviderErrors. The caller closes the response body.
func (c *SageMaker) send(ctx context.Context, creds core.Credentials, model, endpoint string, body []byte, extra map[string]string) (*http.Response, error) {
	headers := map[string]string{"Content-Type": "application/json"}
	for k, v := range extra {
		headers[k] = v
	}
	if ic := extraValue(creds, sageMakerFieldInferenceComponent); ic != "" {
		headers[sageMakerInferenceComponentHdr] = ic
	}
	// SageMaker has no API-key mode: always sign with the AWS identity.
	signCreds := creds
	signCreds.APIKey = ""
	signed, err := awsAuthHeaders(ctx, c.id, "sagemaker", http.MethodPost, endpoint, body, headers, signCreds)
	if err != nil {
		return nil, err
	}
	build := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
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
		return nil, c.statusError(model, resp, readErrorBody(resp))
	}
	return resp, nil
}

// sageMakerErrorBody is the JSON error envelope of the runtime API. ModelError
// (HTTP 424) wraps the container's own status and message.
type sageMakerErrorBody struct {
	Type               string `json:"__type"`
	Message            string `json:"message"`
	ErrorCode          string `json:"ErrorCode"`
	OriginalStatusCode int    `json:"OriginalStatusCode"`
	OriginalMessage    string `json:"OriginalMessage"`
}

// statusError classifies a failed invocation. A ModelError is reported with
// the container's original status so a 429 or context-length 400 from the
// model is handled like the same answer from any other provider; AWS
// throttling and signature failures are re-mapped to 429 / 403.
func (c *SageMaker) statusError(model string, resp *http.Response, body []byte) error {
	var env sageMakerErrorBody
	_ = json.Unmarshal(body, &env)
	errType := resp.Header.Get("X-Amzn-ErrorType")
	if errType == "" {
		errType = env.Type
	}
	if i := strings.IndexAny(errType, ":#"); i >= 0 {
		// "ValidationError:http://..." or "com.amazon...#ValidationError".
		if errType[i] == '#' {
			errType = errType[i+1:]
		} else {
			errType = errType[:i]
		}
	}

	status := resp.StatusCode
	msgBody := body
	switch {
	case env.OriginalStatusCode >= 400:
		status = env.OriginalStatusCode
		if env.OriginalMessage != "" {
			msgBody = []byte(env.OriginalMessage)
		}
	case strings.Contains(errType, "Throttling"):
		status = http.StatusTooManyRequests
	case sageMakerAuthErrorType(errType):
		if status != http.StatusUnauthorized {
			status = http.StatusForbidden
		}
	}
	cp := *resp
	cp.StatusCode = status
	err := httpStatusError(c.id, model, &cp, msgBody)

	lower := strings.ToLower(env.Message)
	if status == http.StatusBadRequest && strings.Contains(lower, "not found") &&
		(strings.Contains(lower, "endpoint") || strings.Contains(lower, "inference component")) {
		if pe := core.AsProviderError(err); pe != nil {
			out := *pe
			out.Kind, out.Scope = core.ErrModelUnavailable, core.FailureScopeModel
			return &out
		}
	}
	return err
}

func sageMakerAuthErrorType(t string) bool {
	switch t {
	case "UnrecognizedClientException", "InvalidSignatureException", "AccessDeniedException",
		"ExpiredTokenException", "IncompleteSignature", "MissingAuthenticationToken",
		"InvalidClientTokenId", "SignatureDoesNotMatch":
		return true
	}
	return false
}

// Chat performs a non-streaming invocation.
func (c *SageMaker) Chat(ctx context.Context, req *core.ChatRequest, creds core.Credentials) (*core.ChatResponse, error) {
	if strings.TrimSpace(req.Model) == "" {
		return nil, &core.ProviderError{Kind: core.ErrBadRequest, Provider: c.id, Message: "model must be the SageMaker endpoint name"}
	}
	body, err := c.render(req, creds, false)
	if err != nil {
		return nil, err
	}
	resp, err := c.send(ctx, creds, req.Model, c.invokeURL(creds, req.Model, false), body,
		map[string]string{"Accept": "application/json"})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := readBodyLimited(c.id, req.Model, resp)
	if err != nil {
		return nil, err
	}
	out, err := c.codec.ParseResponse(raw, req.Model)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Scope: core.FailureScopeModel, Provider: c.id, Model: req.Model,
			Message: "endpoint did not return a Chat Completions response (does the container serve the messages API?): " + err.Error(),
			Cause:   err}
	}
	// Containers report their internal model path ("/opt/ml/model", "tgi");
	// the endpoint name is what callers routed to.
	out.Model = req.Model
	return out, nil
}

// Stream performs a streaming invocation over the response-stream API.
func (c *SageMaker) Stream(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig) (<-chan core.StreamChunk, error) {
	if strings.TrimSpace(req.Model) == "" {
		return nil, &core.ProviderError{Kind: core.ErrBadRequest, Provider: c.id, Message: "model must be the SageMaker endpoint name"}
	}
	body, err := c.render(req, creds, true)
	if err != nil {
		return nil, err
	}
	resp, err := c.send(ctx, creds, req.Model, c.invokeURL(creds, req.Model, true), body, nil)
	if err != nil {
		return nil, err
	}
	out := make(chan core.StreamChunk, 16)
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		// A gateway in front of the endpoint already unwrapped the
		// eventstream into plain SSE.
		go c.pumpSSE(ctx, req.Model, resp.Body, out, cfg)
	} else {
		go c.pumpEventStream(ctx, req.Model, resp.Body, out, cfg)
	}
	return out, nil
}

// pumpSSE forwards a plain SSE body.
func (c *SageMaker) pumpSSE(ctx context.Context, model string, body io.ReadCloser, out chan core.StreamChunk, cfg core.StreamConfig) {
	defer close(out)
	defer body.Close()
	p := newSSEPump(ctx, c.id, model, out, cfg)
	gate := &streamFinishGate{}
	p.run(body, sseDataLines, func(payload []byte) ([]core.StreamChunk, error) {
		if isStreamDoneSentinel(payload) {
			return gate.flush(nil), errStopStream
		}
		chunks, err := c.codec.ParseStreamLine(payload, model)
		return gate.hold(chunks), err
	})
	if !p.errored && ctx.Err() == nil {
		p.emitAll(gate.flush(nil))
	}
}

// pumpEventStream decodes the binary eventstream, reassembles the SSE text
// carried by PayloadPart events and forwards canonical chunks.
func (c *SageMaker) pumpEventStream(ctx context.Context, model string, body io.ReadCloser, out chan core.StreamChunk, cfg core.StreamConfig) {
	defer close(out)
	defer body.Close()
	p := newSSEPump(ctx, c.id, model, out, cfg)
	parser := newEventStreamParser(body)
	gate := &streamFinishGate{}
	var pending []byte

	// handle processes one complete line; false ends the stream.
	handle := func(line []byte) bool {
		payload, ok := sageMakerLinePayload(line)
		if !ok {
			if isSSEKeepAliveBytes(line) {
				return p.emit(core.StreamChunk{Type: core.ChunkPing})
			}
			return true
		}
		if isStreamDoneSentinel(payload) {
			p.emitAll(gate.flush(nil))
			return false
		}
		// Codecs may retain slices of their input; give each line its own copy.
		chunks, err := c.codec.ParseStreamLine(append([]byte(nil), payload...), model)
		if err != nil {
			return true // a malformed line is skipped, as on plain SSE streams
		}
		return p.emitAll(gate.hold(chunks))
	}

	for {
		if ctx.Err() != nil {
			return
		}
		frame, err := parser.next()
		if err == errEventStreamEOF {
			break
		}
		if err != nil {
			if pe := p.scanError(err); pe != nil {
				p.emitError(pe)
			}
			return
		}
		if frame == nil {
			continue
		}
		if pe, ok := sageMakerFrameError(frame); ok {
			p.emitError(pe)
			return
		}
		if ev := frame.headers[":event-type"]; ev != "" && ev != "PayloadPart" {
			continue
		}
		pending = append(pending, sageMakerPartBytes(frame)...)
		rest := pending
		for {
			i := bytes.IndexByte(rest, '\n')
			if i < 0 {
				break
			}
			if !handle(rest[:i]) {
				return
			}
			rest = rest[i+1:]
		}
		pending = pending[:copy(pending, rest)]
		if len(pending) > sseMaxLineBytes {
			p.emitError(&core.ProviderError{Kind: core.ErrUpstream, Scope: core.FailureScopeRequest, Provider: c.id, Model: model,
				Message: "upstream stream frame exceeds the maximum supported size"})
			return
		}
	}
	if len(bytes.TrimSpace(pending)) > 0 && !handle(pending) {
		return
	}
	if !p.errored && ctx.Err() == nil {
		p.emitAll(gate.flush(nil))
	}
}

// sageMakerLinePayload extracts the JSON payload of one reassembled line:
// an SSE "data:" line, or a bare JSON line from containers that stream
// JSON Lines instead of SSE.
func sageMakerLinePayload(line []byte) ([]byte, bool) {
	if data, ok := sseDataPayload(line); ok {
		return data, len(data) > 0
	}
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		return trimmed, true
	}
	return nil, false
}

// sageMakerPartBytes returns the bytes of a PayloadPart event. The runtime
// sends them as the raw event payload; a JSON {"Bytes":"<base64>"} wrapper is
// also accepted for gateways that re-encode events.
func sageMakerPartBytes(f *eventStreamFrame) []byte {
	if strings.Contains(f.headers[":content-type"], "json") {
		var wrapped struct {
			Bytes string `json:"Bytes"`
		}
		if json.Unmarshal(f.payload, &wrapped) == nil && wrapped.Bytes != "" {
			if b, err := base64.StdEncoding.DecodeString(wrapped.Bytes); err == nil {
				return b
			}
		}
	}
	return f.payload
}

// sageMakerFrameError converts an exception or error event into a
// ProviderError. ModelStreamError carries an ErrorCode (ModelInvocationTimeExceeded,
// StreamBroken); InternalStreamFailure is a service-side fault.
func sageMakerFrameError(f *eventStreamFrame) (*core.ProviderError, bool) {
	switch strings.ToLower(f.headers[":message-type"]) {
	case "exception":
		exType := f.headers[":exception-type"]
		var p struct {
			Message   string `json:"Message"`
			ErrorCode string `json:"ErrorCode"`
		}
		_ = json.Unmarshal(f.payload, &p)
		msg := p.Message
		if msg == "" && !json.Valid(f.payload) {
			msg = strings.TrimSpace(string(f.payload))
		}
		return sageMakerStreamError(exType, p.ErrorCode, msg), true
	case "error":
		return sageMakerStreamError(f.headers[":error-code"], "", f.headers[":error-message"]), true
	}
	return nil, false
}

func sageMakerStreamError(exType, code, msg string) *core.ProviderError {
	label := exType
	if code != "" {
		label += " (" + code + ")"
	}
	if label == "" {
		label = "stream error"
	}
	full := label
	if msg != "" {
		full += ": " + msg
	}
	switch {
	case code == "ModelInvocationTimeExceeded":
		return &core.ProviderError{Kind: core.ErrTimeout, Scope: core.FailureScopeProvider, Message: full}
	case code == "StreamBroken":
		return &core.ProviderError{Kind: core.ErrUpstream, Scope: core.FailureScopeNetwork, Message: full}
	case strings.Contains(exType, "Throttling") || exType == "ModelNotReadyException":
		pe := errclass.ClassifyErrorDetail("throttling_error", http.StatusTooManyRequests, "", full)
		return pe
	case sageMakerAuthErrorType(exType):
		return &core.ProviderError{Kind: core.ErrAuth, Scope: core.FailureScopeAccount, StatusCode: http.StatusForbidden, Message: full}
	case exType == "InternalStreamFailure" || exType == "InternalFailure" || exType == "ServiceUnavailable":
		return &core.ProviderError{Kind: core.ErrUpstream, Scope: core.FailureScopeProvider, StatusCode: http.StatusInternalServerError, Message: full}
	case exType == "ValidationError":
		return &core.ProviderError{Kind: core.ErrBadRequest, Scope: core.FailureScopeRequest, StatusCode: http.StatusBadRequest, Message: full}
	}
	// A ModelStreamError carries the container's own words: let them decide
	// (context length, rate limit, ...), defaulting to an upstream fault.
	pe := errclass.ClassifyErrorDetail("", 0, "", msg)
	pe.Message = full
	return pe
}

// isStreamDoneSentinel reports the Chat Completions end-of-stream sentinel.
func isStreamDoneSentinel(payload []byte) bool {
	return bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]"))
}

// streamFinishGate holds back ChunkFinish until the upstream stream ends, so the
// usage chunk that Chat Completions servers send after the finish reason is
// still delivered before it and the stream always closes with usage + finish.
type streamFinishGate struct {
	held   bool
	reason core.FinishReason
}

// hold strips finish chunks from a batch, remembering the reason.
func (g *streamFinishGate) hold(chunks []core.StreamChunk) []core.StreamChunk {
	n := 0
	for _, ch := range chunks {
		if ch.Type == core.ChunkFinish {
			g.held, g.reason = true, ch.FinishReason
			continue
		}
		chunks[n] = ch
		n++
	}
	return chunks[:n]
}

// flush appends the held finish (if any) to chunks and clears it.
func (g *streamFinishGate) flush(chunks []core.StreamChunk) []core.StreamChunk {
	if !g.held {
		return chunks
	}
	g.held = false
	return append(chunks, core.StreamChunk{Type: core.ChunkFinish, FinishReason: g.reason})
}

// Validate confirms the AWS identity with STS GetCallerIdentity.
func (c *SageMaker) Validate(ctx context.Context, creds core.Credentials) error {
	if err := awsCallerIdentity(ctx, c.id, creds); err != nil {
		return fmt.Errorf("validation failed for %s: %w", c.id, err)
	}
	return nil
}
