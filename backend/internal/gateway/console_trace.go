package gateway

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	json "github.com/mydisha/keirouter/backend/internal/fastjson"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/mydisha/keirouter/backend/internal/consolelog"
	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/dispatch"
	"github.com/mydisha/keirouter/backend/internal/pipeline"
)

// reqTrace writes the console lines for one inbound API request. Every line
// carries the request ID, key, client and model, so the dashboard can group a
// request's lines and filter on them; exactly one line per request is marked
// Final and summarizes the outcome in plain language.
//
// A trace is created by authMiddleware and travels in the request context as
// the pipeline's AttemptObserver, which is how failed fallback attempts land
// on the same request's timeline.
type reqTrace struct {
	buf      *consolelog.Buffer
	start    time.Time
	base     consolelog.Entry
	primary  string // provider/model of the first resolved target
	finished atomic.Bool
}

var _ pipeline.AttemptObserver = (*reqTrace)(nil)

func newReqTrace(buf *consolelog.Buffer, r *http.Request) *reqTrace {
	return &reqTrace{
		buf:   buf,
		start: time.Now(),
		base: consolelog.Entry{
			Kind:     consolelog.KindRequest,
			ReqID:    chimiddleware.GetReqID(r.Context()),
			Endpoint: r.Method + " " + r.URL.Path,
		},
	}
}

// traceOf returns the request's trace. Handlers reached without
// authMiddleware (tests, internal calls) get a fresh, detached trace.
func (s *Server) traceOf(r *http.Request) *reqTrace {
	if tr, ok := pipeline.ObserverFrom(r.Context()).(*reqTrace); ok {
		return tr
	}
	return newReqTrace(s.consoleLog, r)
}

func (t *reqTrace) elapsedMs() int { return int(time.Since(t.start).Milliseconds()) }

func (t *reqTrace) entry(level, msg string) consolelog.Entry {
	e := t.base
	e.Level = level
	e.Message = msg
	return e
}

// setRequest records what the client asked for once the body is parsed.
func (t *reqTrace) setRequest(model string, stream bool) {
	if model != "" {
		t.base.Model = model
	}
	t.base.Stream = stream
}

// step records a DEBUG progress line on the request's timeline.
func (t *reqTrace) step(msg, detail string) {
	e := t.entry("DEBUG", msg)
	e.Detail = detail
	e.LatencyMs = t.elapsedMs()
	t.buf.Add(e)
}

// routed records the resolved target chain.
func (t *reqTrace) routed(targets []dispatch.Target) {
	if len(targets) == 0 {
		return
	}
	t.primary = targets[0].Provider + "/" + targets[0].Model
	var chain strings.Builder
	for i, tg := range targets {
		if i > 0 {
			chain.WriteByte('\n')
		}
		fmt.Fprintf(&chain, "%d. %s/%s", i+1, tg.Provider, tg.Model)
	}
	msg := "Routed to " + t.primary
	if n := len(targets) - 1; n > 0 {
		msg += fmt.Sprintf(" (+%d fallback%s)", n, plural(n))
	}
	t.step(msg, "Target order:\n"+chain.String())
}

// AttemptFailed implements pipeline.AttemptObserver.
func (t *reqTrace) AttemptFailed(provider, model string, pe *core.ProviderError) {
	if pe == nil {
		return
	}
	e := t.entry("WARN", fmt.Sprintf("%s/%s failed · %s · trying next target", provider, model, failureReason(pe, provider)))
	e.Provider = provider
	e.Target = provider + "/" + model
	e.Status = pe.StatusCode
	e.LatencyMs = t.elapsedMs()
	e.Detail = errorDetail(pe, pe)
	e.Hint = failureHint(pe)
	t.buf.Add(e)
}

// RateLimitWait implements pipeline.AttemptObserver.
func (t *reqTrace) RateLimitWait(wait time.Duration, retry, max int) {
	e := t.entry("WARN", fmt.Sprintf("Every eligible account is rate-limited · waiting %s before retry %d/%d",
		humanDuration(int(wait.Milliseconds())), retry, max))
	e.Status = http.StatusTooManyRequests
	e.LatencyMs = t.elapsedMs()
	e.Hint = "All accounts for this model are cooling down after 429s. KeiRouter holds the request and retries instead of failing right away; add accounts or lower traffic if this repeats."
	t.buf.Add(e)
}

// claim marks the trace finished and reports whether the caller is the first
// to do so; only the first terminal line per request is written.
func (t *reqTrace) claim() bool { return t.finished.CompareAndSwap(false, true) }

// reject finalizes a request KeiRouter refused before it reached a provider.
func (t *reqTrace) reject(status int, msg, detail, hint string) {
	if !t.claim() {
		return
	}
	level := "WARN"
	if status >= 500 {
		level = "ERROR"
	}
	e := t.entry(level, msg)
	e.Status = status
	e.Detail = detail
	e.Hint = hint
	e.LatencyMs = t.elapsedMs()
	e.Final = true
	t.buf.Add(e)
}

// outcome is what a handler knows when a dispatched request ends.
type outcome struct {
	provider   string
	model      string
	account    string
	tokens     int
	costMicros int64
	cacheHit   bool
	fallbacks  int
	chunks     int
	bytes      int64
	err        error
}

// complete finalizes a request that was dispatched to the pipeline.
func (t *reqTrace) complete(o outcome) {
	if !t.claim() {
		return
	}
	e := t.base
	e.Final = true
	e.LatencyMs = t.elapsedMs()
	e.Provider = o.provider
	e.Fallbacks = o.fallbacks
	e.Tokens = o.tokens
	e.CostMicros = o.costMicros
	e.CacheHit = o.cacheHit
	if o.provider != "" && o.model != "" {
		e.Target = o.provider + "/" + o.model
	}
	model := e.Model
	if model == "" {
		model = o.model
	}

	if o.err != nil {
		t.fail(&e, model, o)
		t.buf.Add(e)
		return
	}

	e.Level = "INFO"
	e.Status = http.StatusOK
	var msg strings.Builder
	msg.WriteString("Completed · ")
	msg.WriteString(model)
	switch {
	case e.Target != "" && !strings.EqualFold(e.Target, model) && !strings.EqualFold(o.model, model):
		msg.WriteString(" → ")
		msg.WriteString(e.Target)
	case o.provider != "":
		msg.WriteString(" via ")
		msg.WriteString(o.provider)
	}
	if o.tokens > 0 {
		fmt.Fprintf(&msg, " · %s tokens", humanInt(o.tokens))
	}
	if o.costMicros > 0 {
		fmt.Fprintf(&msg, " · %s", humanCost(o.costMicros))
	}
	fmt.Fprintf(&msg, " · %s", humanDuration(e.LatencyMs))
	if o.cacheHit {
		msg.WriteString(" · cache hit")
	}
	if o.fallbacks > 0 {
		fmt.Fprintf(&msg, " · on attempt %d", o.fallbacks+1)
		e.Hint = fmt.Sprintf("The first attempt (%s) failed, so KeiRouter retried on another account or fallback target. The warnings in this request's timeline say why each earlier attempt failed.", orDash(t.primary))
	}
	e.Message = msg.String()

	var d strings.Builder
	writeField(&d, "Requested", model)
	writeField(&d, "Served by", e.Target)
	writeField(&d, "Account", o.account)
	writeField(&d, "Key", e.Key)
	writeField(&d, "Client", e.Client)
	if o.tokens > 0 {
		writeField(&d, "Tokens", humanInt(o.tokens))
	}
	if o.costMicros > 0 {
		writeField(&d, "Cost", humanCost(o.costMicros))
	}
	if o.chunks > 0 {
		writeField(&d, "Chunks", humanInt(o.chunks))
	}
	if o.bytes > 0 {
		writeField(&d, "Streamed", humanBytes(int(o.bytes)))
	}
	writeField(&d, "Duration", fmt.Sprintf("%dms", e.LatencyMs))
	e.Detail = d.String()
	t.buf.Add(e)
}

func (t *reqTrace) fail(e *consolelog.Entry, model string, o outcome) {
	pe := core.AsProviderError(o.err)
	if e.Provider == "" {
		e.Provider = pe.Provider
	}
	if isClientDisconnect(o.err) || pe.Kind == core.ErrClientCanceled {
		e.Level = "WARN"
		e.Status = 499
		msg := fmt.Sprintf("Client disconnected · %s · after %s", model, humanDuration(e.LatencyMs))
		if o.chunks > 0 {
			msg += fmt.Sprintf(" · %s chunks sent", humanInt(o.chunks))
		}
		e.Message = msg
		e.Hint = "The client closed the connection before the response finished (user cancelled, client timeout, or network drop). Not a provider fault; nothing was cooled down."
		e.Detail = errorDetail(pe, o.err)
		return
	}
	e.Status = providerErrorStatus(pe)
	e.Level = "ERROR"
	if clientSideFailure(pe.Kind) {
		e.Level = "WARN"
	}
	e.Message = fmt.Sprintf("Failed · %s · %s", model, failureReason(pe, e.Provider))
	if o.fallbacks > 0 {
		e.Message += fmt.Sprintf(" · %d attempts", o.fallbacks+1)
	}
	e.Hint = failureHint(pe)
	e.Detail = errorDetail(pe, o.err)
}

// clientSideFailure reports whether an error kind is caused by the request or
// a deliberate policy rather than a provider or KeiRouter fault.
func clientSideFailure(kind core.ErrorKind) bool {
	switch kind {
	case core.ErrBadRequest, core.ErrContextWindow, core.ErrContentFilter,
		core.ErrCapability, core.ErrBudgetBlocked, core.ErrPolicyBlocked:
		return true
	}
	return false
}

// failureReason is the short, inline cause of a provider error.
func failureReason(pe *core.ProviderError, provider string) string {
	if provider == "" {
		provider = pe.Provider
	}
	if provider == "" {
		provider = "provider"
	}
	status := ""
	if pe.StatusCode > 0 {
		status = fmt.Sprintf(" (HTTP %d)", pe.StatusCode)
	}
	switch pe.Kind {
	case core.ErrAuth:
		return provider + " rejected the credentials" + status
	case core.ErrRateLimit:
		return "rate-limited by " + provider + status
	case core.ErrQuotaExhausted:
		if pe.CreditsExhausted {
			return provider + " credits used up" + status
		}
		return provider + " quota exhausted" + status
	case core.ErrUpstream:
		if pe.StatusCode == 0 {
			return provider + " unreachable (no HTTP response)"
		}
		return provider + " server error" + status
	case core.ErrTimeout:
		return provider + " timed out"
	case core.ErrBadRequest:
		return provider + " rejected the request" + status
	case core.ErrContextWindow:
		return "prompt exceeds the context window"
	case core.ErrContentFilter:
		return "blocked by " + provider + " content filter"
	case core.ErrModelUnavailable:
		return "model unavailable" + status
	case core.ErrModelOutOfStock:
		return "model out of stock (no seller right now)" + status
	case core.ErrCapability:
		return "no target supports this request"
	case core.ErrBudgetBlocked:
		return "blocked by a budget limit"
	case core.ErrPolicyBlocked:
		return "blocked by a guardrail policy"
	case core.ErrClientCanceled:
		return "client disconnected"
	case core.ErrInternal:
		return "internal error"
	}
	return string(pe.Kind) + status
}

// failureHint explains a provider error in operator terms: what it means and
// where to look next.
func failureHint(pe *core.ProviderError) string {
	var h string
	switch pe.Kind {
	case core.ErrAuth:
		h = "The provider refused the account's credentials (expired token, revoked key, or wrong region). Reconnect or update the account on the Providers page."
	case core.ErrRateLimit:
		h = "The provider is throttling this account. KeiRouter cooled it down and moves to other accounts or fallbacks; add accounts or a fallback chain if this keeps happening."
	case core.ErrQuotaExhausted:
		if pe.CreditsExhausted {
			h = "The account's paid balance is empty and will not recover by itself. Top up the provider account or disable it."
		} else {
			h = "The account's quota window is used up. It recovers when the window resets; add accounts or fallbacks to cover the gap."
		}
	case core.ErrUpstream:
		if pe.StatusCode == 0 {
			h = "KeiRouter got no HTTP response from the provider (connection refused, DNS, TLS or proxy failure). Check that the provider is running and its base URL and proxy settings are right."
		} else {
			h = "The provider returned a server-side error. Usually transient; if it persists, check the provider's status page or the Health page."
		}
	case core.ErrTimeout:
		h = "The provider stopped sending data or took too long. Check provider health, or raise the stall/request timeout for slow reasoning models."
	case core.ErrBadRequest:
		h = "The provider rejected the request payload itself (unsupported parameter, malformed tool schema, ...). Usually a client or model-compatibility issue, not routing."
	case core.ErrContextWindow:
		h = "The prompt is larger than every target model's context window. The client should compact the conversation, or route to a model with a larger window."
	case core.ErrContentFilter:
		h = "The provider's safety system rejected the prompt or the output."
	case core.ErrModelUnavailable:
		h = "The model is not served by any active account. Check the model name, the account's model list, or whether the provider retired it."
	case core.ErrModelOutOfStock:
		h = "The marketplace has no healthy seller for this model right now. Nothing was cooled down: the chain moved on, and the client was told to retry in 30 seconds. Add a fallback model to the chain to cover these gaps."
	case core.ErrCapability:
		h = "The request needs a feature (tools, vision, audio, ...) that none of the target models supports."
	case core.ErrBudgetBlocked:
		h = "A budget on this key, project or tenant is used up. Raise it on the Budgets page or wait for the period to reset."
	case core.ErrPolicyBlocked:
		h = "A guardrail policy blocked the request. The Guardrails page shows which rule matched."
	case core.ErrInternal:
		h = "KeiRouter failed internally. The server log has the full error."
	default:
		h = "See the detail below for the raw error."
	}
	if pe.RetryAfter > 0 {
		h += fmt.Sprintf(" Retry-After: %s.", humanDuration(int(pe.RetryAfter.Milliseconds())))
	}
	return h
}

// errorDetail renders the technical context of a failure as "Field: value"
// lines followed by the raw error text.
func errorDetail(pe *core.ProviderError, err error) string {
	var d strings.Builder
	if pe != nil {
		if pe.Provider != "" {
			writeField(&d, "Provider", pe.Provider+"/"+pe.Model)
		}
		writeField(&d, "Error kind", string(pe.Kind))
		if pe.StatusCode > 0 {
			writeField(&d, "Upstream status", fmt.Sprintf("%d", pe.StatusCode))
		}
		if pe.RetryAfter > 0 {
			writeField(&d, "Retry after", pe.RetryAfter.Round(time.Millisecond).String())
		}
	}
	raw := ""
	switch {
	case pe != nil && pe.Message != "":
		raw = pe.Message
		if pe.Cause != nil && !strings.Contains(raw, pe.Cause.Error()) {
			raw += "\n" + pe.Cause.Error()
		}
	case err != nil:
		raw = err.Error()
	}
	if raw != "" {
		if d.Len() > 0 {
			d.WriteString("\n\n")
		}
		d.WriteString(raw)
	}
	return d.String()
}

func writeField(b *strings.Builder, name, value string) {
	if value == "" {
		return
	}
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	b.WriteString(name)
	b.WriteString(": ")
	b.WriteString(value)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func humanCost(micros int64) string {
	usd := float64(micros) / 1_000_000
	if usd < 0.01 {
		return fmt.Sprintf("$%.4f", usd)
	}
	return fmt.Sprintf("$%.2f", usd)
}

func maskToken(tok string) string {
	if len(tok) <= 10 {
		return strings.Repeat("•", len(tok))
	}
	return tok[:6] + "…" + tok[len(tok)-4:]
}

// ---- safety net ---------------------------------------------------------------

// settle writes the terminal line for a request whose handler returned without
// calling complete or reject (early validation errors, media endpoints, model
// listings), using the HTTP status and the error body the client received.
func (t *reqTrace) settle(status int, body []byte) {
	if t.finished.Load() {
		return
	}
	if status == 0 {
		status = http.StatusOK
	}
	if status < 400 {
		if !t.claim() {
			return
		}
		e := t.entry("DEBUG", fmt.Sprintf("%d · %s · %s", status, t.base.Endpoint, humanDuration(t.elapsedMs())))
		e.Status = status
		e.LatencyMs = t.elapsedMs()
		e.Final = true
		t.buf.Add(e)
		return
	}
	reason := errorBodyMessage(body)
	if reason == "" {
		reason = strings.ToLower(http.StatusText(status))
	}
	path := t.base.Endpoint
	if _, p, ok := strings.Cut(path, " "); ok {
		path = p
	}
	msg := fmt.Sprintf("Rejected · %s · %s", path, reason)
	if t.base.Model != "" {
		msg = fmt.Sprintf("Rejected · %s · %s", t.base.Model, reason)
	}
	t.reject(status, msg, "", statusHint(status))
}

// statusHint explains a gateway-side HTTP error status.
func statusHint(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "The client sent a request KeiRouter could not accept (missing field, bad JSON, unknown model). Fix the client request."
	case http.StatusUnauthorized:
		return "The API key is missing, unknown, revoked or expired."
	case http.StatusForbidden:
		return "The key is valid but not allowed to do this (model restrictions or policy)."
	case http.StatusNotFound:
		return "The requested model or resource does not exist on this gateway."
	case http.StatusRequestEntityTooLarge:
		return "The body is larger than server.max_request_body_bytes."
	case http.StatusTooManyRequests:
		return "A KeiRouter rate limit on this key (RPM/TPM/concurrency) was hit."
	case http.StatusServiceUnavailable:
		return "KeiRouter is overloaded or a dependency is unavailable; retry shortly."
	}
	if status >= 500 {
		return "KeiRouter failed while handling the request. The server log has the full error."
	}
	return ""
}

// errorBodyMessage extracts the message from an OpenAI, Anthropic, Gemini or
// plain {"error": "..."} error envelope.
func errorBodyMessage(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' {
		return strings.TrimSpace(string(body))
	}
	var env struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(body, &env) != nil {
		return ""
	}
	if len(env.Error) > 0 {
		var s string
		if json.Unmarshal(env.Error, &s) == nil {
			return s
		}
		var obj struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(env.Error, &obj) == nil && obj.Message != "" {
			return obj.Message
		}
	}
	return env.Message
}

// errorTap keeps the start of an error response body so the safety net can
// quote it. It ignores successful responses, so streaming bodies cost one
// status check per write.
type errorTap struct {
	ww  chimiddleware.WrapResponseWriter
	buf []byte
}

const errorTapMax = 1024

func (t *errorTap) Write(p []byte) (int, error) {
	if t.ww.Status() >= 400 && len(t.buf) < errorTapMax {
		n := min(len(p), errorTapMax-len(t.buf))
		t.buf = append(t.buf, p[:n]...)
	}
	return len(p), nil
}
