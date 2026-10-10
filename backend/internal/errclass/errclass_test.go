package errclass

import (
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/stretchr/testify/require"
)

func TestLooksLikeContextWindow(t *testing.T) {
	for _, body := range []string{
		`{"error":{"message":"This model's maximum context length is 128000 tokens. However, your messages resulted in 130000 tokens.","code":"context_length_exceeded"}}`,
		`{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 210000 tokens > 200000 maximum"}}`,
		`{"error":{"code":400,"message":"The input token count (1200000) exceeds the maximum number of tokens allowed (1048576)."}}`,
		`Input is too long for requested model.`,
	} {
		require.True(t, LooksLikeContextWindow(body), body)
	}
	for _, body := range []string{
		`Rate limit reached for gpt-5-codex: too many tokens per min. Limit 30000, Requested 31000.`,
		`{"error":{"message":"Invalid 'user': string too long. Expected a string with maximum length 256"}}`,
		`{"error":{"message":"string_above_max_length"}}`,
		`{"error":{"message":"Incorrect API key provided"}}`,
	} {
		require.False(t, LooksLikeContextWindow(body), body)
	}
}

func TestLooksLikeContentFilter(t *testing.T) {
	require.True(t, LooksLikeContentFilter(`{"error":{"code":"content_policy_violation","message":"Your request was rejected"}}`))
	require.True(t, LooksLikeContentFilter(`The response was filtered due to the prompt triggering Azure OpenAI's content management policy.`))
	require.False(t, LooksLikeContentFilter(`{"error":{"message":"model not found"}}`))
}

func TestBodyErrorCode(t *testing.T) {
	require.Equal(t, 429, BodyErrorCode([]byte(`{"error":{"code":429,"message":"Resource exhausted","status":"RESOURCE_EXHAUSTED"}}`)))
	require.Equal(t, 429, BodyErrorCode([]byte(`{"error":{"code":"429","message":"x"}}`)))
	require.Equal(t, 429, BodyErrorCode([]byte(`{"error":{"status":"RESOURCE_EXHAUSTED","message":"x"}}`)))
	require.Equal(t, 503, BodyErrorCode([]byte(`{"code":503}`)))
	require.Equal(t, 0, BodyErrorCode([]byte(`{"error":{"code":"insufficient_quota"}}`)))
	require.Equal(t, 0, BodyErrorCode([]byte(`not json`)))
}

func TestLooksLikeRateLimitWrapped(t *testing.T) {
	require.True(t, LooksLikeRateLimitWrapped([]byte(`{"error":{"code":429,"message":"Resource exhausted"}}`)))
	require.True(t, LooksLikeRateLimitWrapped([]byte(`{"error":{"message":"Request too large for gpt-4o on tokens per min (TPM)"}}`)))
	require.False(t, LooksLikeRateLimitWrapped([]byte(`{"error":{"message":"internal error"}}`)))
}

func TestStreamErrorFrame(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		kind    core.ErrorKind
		scope   core.FailureScope
		status  int
	}{
		{"anthropic overloaded", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, core.ErrUpstream, core.FailureScopeProvider, 529},
		{"anthropic rate limit", `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`, core.ErrRateLimit, core.FailureScopeAccount, 429},
		{"openai code 429", `{"error":{"message":"Rate limit reached","type":"tokens","code":"429"}}`, core.ErrRateLimit, core.FailureScopeAccount, 429},
		{"openai context", `{"error":{"message":"This model's maximum context length is 8192 tokens","type":"invalid_request_error","code":"context_length_exceeded"}}`, core.ErrContextWindow, core.FailureScopeModel, 0},
		{"gemini resource exhausted", `{"error":{"code":429,"message":"Resource has been exhausted","status":"RESOURCE_EXHAUSTED"}}`, core.ErrRateLimit, core.FailureScopeAccount, 429},
		{"gemini unavailable", `{"error":{"code":503,"message":"The model is overloaded.","status":"UNAVAILABLE"}}`, core.ErrUpstream, core.FailureScopeProvider, 503},
		{"string error", `{"error":"model not found: foo"}`, core.ErrModelUnavailable, core.FailureScopeModel, 0},
		{"auth", `{"error":{"message":"invalid x-api-key","type":"authentication_error"}}`, core.ErrAuth, core.FailureScopeAccount, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pe, ok := StreamErrorFrame([]byte(tc.payload))
			require.True(t, ok)
			require.Equal(t, tc.kind, pe.Kind)
			require.Equal(t, tc.scope, pe.EffectiveScope())
			require.Equal(t, tc.status, pe.StatusCode)
		})
	}

	for _, payload := range []string{
		`{"choices":[{"delta":{"content":"no error here"}}]}`,
		`{"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
		`{"error":null,"choices":[]}`,
		`{"type":"response.failed","error":{"message":"handled by its codec"}}`,
		`[DONE]`,
		``,
	} {
		_, ok := StreamErrorFrame([]byte(payload))
		require.False(t, ok, payload)
	}
}
