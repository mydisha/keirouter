package errclass

import (
	"bytes"
	"strings"

	json "github.com/mydisha/keirouter/backend/internal/fastjson"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// streamErrorEnvelope matches the error frames providers emit inside an HTTP
// 200 SSE stream:
//
//	OpenAI:    {"error":{"message":"...","type":"...","code":"..."}}
//	Anthropic: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}
//	Gemini:    {"error":{"code":429,"message":"...","status":"RESOURCE_EXHAUSTED"}}
//	Generic:   {"error":"string message"}
type streamErrorEnvelope struct {
	Type  string          `json:"type"`
	Error json.RawMessage `json:"error"`
}

type streamErrorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Code    any    `json:"code"`
	Status  string `json:"status"`
}

// StreamErrorFrame reports whether an SSE data payload is an in-band error
// frame and, if so, returns the classified ProviderError. Provider and Model
// are left for the caller to fill in.
//
// Treating these frames as errors matters: without it a provider that reports
// overload or a rate limit after sending HTTP 200 looks like a clean, empty
// completion. The stream is then recorded as a success, the account is never
// cooled down, and the client receives a well-formed but truncated answer.
func StreamErrorFrame(payload []byte) (*core.ProviderError, bool) {
	trimmed := trimSpaceBytes(payload)
	if len(trimmed) < 8 || trimmed[0] != '{' {
		return nil, false
	}
	// Cheap pre-check so the vast majority of frames skip the decode.
	if !containsErrorKey(trimmed) {
		return nil, false
	}
	var env streamErrorEnvelope
	if json.UnmarshalNoCopy(trimmed, &env) != nil {
		return nil, false
	}
	errRaw := trimSpaceBytes(env.Error)
	if len(errRaw) == 0 || string(errRaw) == "null" {
		return nil, false
	}
	if env.Type != "" && env.Type != "error" {
		// A legitimate event that happens to carry an "error" field (for
		// example Responses API response.failed is handled by its codec).
		return nil, false
	}

	var detail streamErrorDetail
	switch errRaw[0] {
	case '{':
		if json.Unmarshal(errRaw, &detail) != nil {
			return nil, false
		}
	case '"':
		var msg string
		if json.Unmarshal(errRaw, &msg) != nil || msg == "" {
			return nil, false
		}
		detail.Message = msg
	default:
		return nil, false
	}
	if detail.Message == "" && detail.Type == "" {
		return nil, false
	}
	return ClassifyErrorDetail(detail.Type, anyToInt(detail.Code), detail.Status, detail.Message), true
}

// ClassifyErrorDetail maps a provider error type/code/status/message tuple to
// a ProviderError the dispatcher can act on. StatusCode is set when the frame
// carried one so telemetry can distinguish network faults from 5xx.
func ClassifyErrorDetail(errType string, code int, status, message string) *core.ProviderError {
	pe := &core.ProviderError{Message: message}
	if pe.Message == "" {
		pe.Message = errType
	}
	lowerType := strings.ToLower(errType)
	lowerMsg := strings.ToLower(message)

	if code == 0 {
		switch strings.ToUpper(status) {
		case "RESOURCE_EXHAUSTED":
			code = 429
		case "UNAVAILABLE":
			code = 503
		case "DEADLINE_EXCEEDED":
			code = 504
		case "PERMISSION_DENIED", "UNAUTHENTICATED":
			code = 403
		case "NOT_FOUND":
			code = 404
		case "INVALID_ARGUMENT", "FAILED_PRECONDITION":
			code = 400
		case "INTERNAL":
			code = 500
		}
	}
	pe.StatusCode = code

	// Anthropic / OpenAI error type vocabulary first: it is the most precise
	// signal and does not depend on wording.
	switch lowerType {
	case "overloaded_error":
		pe.Kind, pe.Scope = core.ErrUpstream, core.FailureScopeProvider
		if pe.StatusCode == 0 {
			pe.StatusCode = 529
		}
		return pe
	case "rate_limit_error", "rate_limit_exceeded", "throttling_error", "tokens", "requests", "insufficient_quota":
		pe.Kind, pe.Scope = core.ErrRateLimit, core.FailureScopeAccount
		if pe.StatusCode == 0 {
			pe.StatusCode = 429
		}
		return pe
	case "authentication_error", "permission_error", "invalid_api_key":
		pe.Kind, pe.Scope = core.ErrAuth, core.FailureScopeAccount
		return pe
	case "billing_error":
		pe.Kind, pe.Scope = core.ErrQuotaExhausted, core.FailureScopeAccount
		return pe
	case "not_found_error", "model_not_found":
		pe.Kind, pe.Scope = core.ErrModelUnavailable, core.FailureScopeModel
		return pe
	case "api_error", "internal_server_error", "server_error", "service_unavailable_error":
		pe.Kind, pe.Scope = core.ErrUpstream, core.FailureScopeProvider
		if pe.StatusCode == 0 {
			pe.StatusCode = 500
		}
		return pe
	case "timeout_error":
		pe.Kind, pe.Scope = core.ErrTimeout, core.FailureScopeProvider
		return pe
	}

	// Message vocabulary next (LiteLLM's string heuristics).
	switch {
	case LooksLikeContextWindow(lowerMsg):
		pe.Kind, pe.Scope = core.ErrContextWindow, core.FailureScopeModel
		return pe
	case LooksLikeContentFilter(lowerMsg):
		pe.Kind, pe.Scope = core.ErrContentFilter, core.FailureScopeRequest
		return pe
	case strings.Contains(lowerMsg, "rate limit"), strings.Contains(lowerMsg, "rate_limit"),
		strings.Contains(lowerMsg, "tokens per min"), strings.Contains(lowerMsg, "too many requests"),
		strings.Contains(lowerMsg, "resource exhausted"), strings.Contains(lowerMsg, "quota exceeded"):
		pe.Kind, pe.Scope = core.ErrRateLimit, core.FailureScopeAccount
		return pe
	case strings.Contains(lowerMsg, "at capacity"), strings.Contains(lowerMsg, "overloaded"),
		strings.Contains(lowerMsg, "server_is_overloaded"), strings.Contains(lowerMsg, "model_at_capacity"):
		// Capacity, not credentials: rotate attempts without a heavy penalty.
		pe.Kind, pe.Scope = core.ErrUpstream, core.FailureScopeProvider
		return pe
	case strings.Contains(lowerMsg, "model") &&
		(strings.Contains(lowerMsg, "not supported") || strings.Contains(lowerMsg, "not available") ||
			strings.Contains(lowerMsg, "not found") || strings.Contains(lowerMsg, "does not exist")):
		pe.Kind, pe.Scope = core.ErrModelUnavailable, core.FailureScopeModel
		return pe
	}

	// Finally the numeric status.
	switch {
	case code == 429:
		pe.Kind, pe.Scope = core.ErrRateLimit, core.FailureScopeAccount
	case code == 401, code == 403:
		pe.Kind, pe.Scope = core.ErrAuth, core.FailureScopeAccount
	case code == 402:
		pe.Kind, pe.Scope = core.ErrQuotaExhausted, core.FailureScopeAccount
	case code == 404:
		pe.Kind, pe.Scope = core.ErrModelUnavailable, core.FailureScopeModel
	case code == 408, code == 504:
		pe.Kind, pe.Scope = core.ErrTimeout, core.FailureScopeProvider
	case code >= 400 && code < 500:
		pe.Kind, pe.Scope = core.ErrBadRequest, core.FailureScopeRequest
	default:
		pe.Kind, pe.Scope = core.ErrUpstream, core.FailureScopeProvider
	}
	return pe
}

var errorKey = []byte(`"error"`)

// containsErrorKey is the allocation-free pre-check that lets the vast
// majority of stream frames skip the JSON decode.
func containsErrorKey(b []byte) bool {
	return bytes.Contains(b, errorKey)
}

func trimSpaceBytes(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && (b[start] == ' ' || b[start] == '\n' || b[start] == '\r' || b[start] == '\t') {
		start++
	}
	for end > start && (b[end-1] == ' ' || b[end-1] == '\n' || b[end-1] == '\r' || b[end-1] == '\t') {
		end--
	}
	return b[start:end]
}
