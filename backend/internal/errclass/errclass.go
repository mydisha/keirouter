// Package errclass holds provider-agnostic error-text classifiers shared by
// the HTTP status mapper (connectors) and the stream codecs (transform). The
// phrase lists mirror LiteLLM's exception_mapping_utils so a provider's
// wording is recognised the same way on both gateways.
package errclass

import (
	"strings"
	"time"

	json "github.com/mydisha/keirouter/backend/internal/fastjson"
)

// contextWindowPhrases are lowercase substrings that reliably indicate the
// prompt exceeds the model's context window (OpenAI, Anthropic, Gemini,
// Bedrock, llama.cpp, Cerebras, vLLM, HuggingFace wording).
var contextWindowPhrases = []string{
	"context_length_exceeded",
	"context length exceeded",
	"exceed context limit",
	"exceeds the context window",
	"exceeds context window",
	"this model's maximum context length is",
	"model's maximum context limit",
	"maximum context length",
	"is longer than the model's context length",
	"input tokens exceed the configured limit",
	"`inputs` tokens + `max_new_tokens` must be",
	"exceeds the available context size",
	"exceeds the maximum number of tokens allowed",
	"prompt is too long",
	"prompt: length",
	"input is too long",
	"too many input tokens",
	"too many tokens",
	"request payload size exceeds",
	"string too long. expected a string with maximum length",
	"input length and `max_tokens` exceed context limit",
}

// contextWindowExclusions prevent unrelated "too long" validation errors (for
// example an oversized `user` field) from being treated as context overflow.
var contextWindowExclusions = []string{
	"string_above_max_length",
}

// LooksLikeContextWindow reports whether an error body describes a prompt that
// does not fit the model's context window.
func LooksLikeContextWindow(body string) bool {
	if body == "" {
		return false
	}
	s := strings.ToLower(body)
	for _, ex := range contextWindowExclusions {
		if strings.Contains(s, ex) {
			return false
		}
	}
	if strings.Contains(s, "invalid 'user'") && strings.Contains(s, "string too long") {
		return false
	}
	// Throttling messages mention token counts too ("Rate limit reached ...
	// too many tokens per min"); a rate limit is never a context overflow.
	if strings.Contains(s, "rate limit") || strings.Contains(s, "rate_limit") ||
		strings.Contains(s, "per min") || strings.Contains(s, "per minute") ||
		strings.Contains(s, "per second") || strings.Contains(s, "per hour") {
		return false
	}
	for _, p := range contextWindowPhrases {
		if strings.Contains(s, p) {
			return true
		}
	}
	// Cerebras: "current length is N while limit is M"; HF: "maximum input length is N tokens".
	if strings.Contains(s, "current length is") && strings.Contains(s, "while limit is") {
		return true
	}
	if strings.Contains(s, "maximum input length is") && strings.Contains(s, "tokens") {
		return true
	}
	return false
}

// authPhrases are lowercase substrings providers use for credential
// failures reported under a status other than 401/403. Cloudflare answers an
// invalid token with HTTP 400 {"errors":[{"code":9106,"message":
// "Authentication failed"}]} or code 10000 "Authentication error".
var authPhrases = []string{
	"authentication failed",
	"authentication error",
	"invalid api key",
	"invalid api token",
	"invalid token",
	"incorrect api key",
	"unauthorized",
	"invalid x-api-key",
	"api key not valid",
	"invalid authentication",
}

// LooksLikeAuthError reports whether an error body describes rejected
// credentials regardless of the HTTP status it came with.
func LooksLikeAuthError(body string) bool {
	if body == "" {
		return false
	}
	s := strings.ToLower(body)
	for _, p := range authPhrases {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// contentFilterPhrases are lowercase substrings used by OpenAI, Azure,
// Anthropic, Gemini and Bedrock when a safety system rejects a request.
var contentFilterPhrases = []string{
	"content_policy_violation",
	"content_filter_policy",
	"responsibleaipolicyviolation",
	"content management policy",
	"your task failed as a result of our safety system",
	"your request was rejected as a result of our safety system",
	"request was rejected as a result of the safety system",
	"violating our usage policy",
	"content filtering policy",
	"the response was blocked",
	"output blocked by content filtering policy",
	"blocked by content filter",
	"prohibited_content",
}

// LooksLikeContentFilter reports whether an error body describes a safety /
// content-policy rejection.
func LooksLikeContentFilter(body string) bool {
	if body == "" {
		return false
	}
	s := strings.ToLower(body)
	for _, p := range contentFilterPhrases {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// BodyErrorCode extracts a numeric status from common JSON error envelopes:
// {"error":{"code":429}}, {"error":{"code":"429"}}, {"code":429},
// {"error":{"status":"RESOURCE_EXHAUSTED"}} (Gemini). Returns 0 when absent.
// Gateways and reverse proxies frequently wrap a provider 429 in a 5xx; the
// embedded code is the more truthful signal.
func BodyErrorCode(body []byte) int {
	if len(body) == 0 || body[0] != '{' {
		return 0
	}
	var env struct {
		Code  any `json:"code"`
		Error *struct {
			Code   any    `json:"code"`
			Status string `json:"status"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &env) != nil {
		return 0
	}
	if env.Error != nil {
		if c := anyToInt(env.Error.Code); c > 0 {
			return c
		}
		switch strings.ToUpper(env.Error.Status) {
		case "RESOURCE_EXHAUSTED":
			return 429
		case "UNAVAILABLE":
			return 503
		case "DEADLINE_EXCEEDED":
			return 504
		}
	}
	return anyToInt(env.Code)
}

func anyToInt(v any) int {
	switch n := v.(type) {
	case float64:
		if n > 0 && n < 1000 {
			return int(n)
		}
	case string:
		s := strings.TrimSpace(n)
		if len(s) == 3 && s[0] >= '1' && s[0] <= '5' {
			if d, err := parseSmallInt(s); err == nil {
				return d
			}
		}
	}
	return 0
}

func parseSmallInt(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errNotNumber
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

// LooksLikeRateLimitWrapped reports whether a non-429 response actually
// carries a rate-limit signal (Vertex/Gemini "Resource exhausted" inside a
// 5xx, OpenAI "Request too large" TPM rejections, Mistral "service tier
// capacity exceeded").
func LooksLikeRateLimitWrapped(body []byte) bool {
	if BodyErrorCode(body) == 429 {
		return true
	}
	s := strings.ToLower(string(body))
	return strings.Contains(s, "resource exhausted") ||
		strings.Contains(s, "resource_exhausted") ||
		strings.Contains(s, "request too large") ||
		strings.Contains(s, "service tier capacity exceeded") ||
		strings.Contains(s, "quota exceeded")
}

// MaxRetryAfterHeader caps upstream Retry-After hints taken from headers. A
// provider that answers with a day-long Retry-After would otherwise park the
// account for a day on a single response; anything beyond an hour is treated
// as "an hour, then probe again". Calendar-derived quota resets computed by
// KeiRouter itself are not subject to this cap.
const MaxRetryAfterHeader = time.Hour

func CapRetryAfter(d time.Duration) time.Duration {
	if d > MaxRetryAfterHeader {
		return MaxRetryAfterHeader
	}
	return d
}

var errNotNumber = errNotNumberT{}

type errNotNumberT struct{}

func (errNotNumberT) Error() string { return "not a number" }
