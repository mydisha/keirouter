package connectors

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/stretchr/testify/require"
)

func TestLooksLikeQuotaExhausted(t *testing.T) {
	tests := []struct {
		body string
		want bool
	}{
		{"", false},
		{"rate limit exceeded", false},
		{"too many requests", false},
		{"daily limit reached", true},
		{"daily quota exceeded", true},
		{"daily free allocation used", true},
		{"monthly limit reached", true},
		{"per day limit exceeded", true},
		{"per month limit reached", true},
		{"quota exceed", false},
		{"exceed quota", false},
		{"requests per minute quota exceeded", false},
		{"insufficient quota", true},
		{"billing cap reached", true},
		{"credit exhaust", true},
		{"out of credits", true},
		{"hard limit", true},
		{"plan limit", true},
		{"subscription limit", true},
		{"weekly quota", true},
		{"session limit", true},
		{"Rate limit exceeded", false}, // case insensitive check — should NOT match quota
		{"RATE LIMIT", false},
	}

	for _, tt := range tests {
		t.Run(tt.body, func(t *testing.T) {
			if got := looksLikeQuotaExhausted(tt.body); got != tt.want {
				t.Errorf("looksLikeQuotaExhausted(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}

func TestParseResetFromBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want time.Duration
	}{
		{
			name: "nested retry after seconds",
			body: `{"error":{"retry_after":2.5}}`,
			want: 2500 * time.Millisecond,
		},
		{
			name: "camel case retry after duration",
			body: `{"retryAfter":"3s"}`,
			want: 3 * time.Second,
		},
		{
			name: "past reset ignored",
			body: `{"resetAt":1000000001}`,
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseResetFromBody([]byte(tt.body)); got != tt.want {
				t.Fatalf("parseResetFromBody() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLooksLikeRateLimit(t *testing.T) {
	tests := []struct {
		body string
		want bool
	}{
		{"", false},
		{"rate limit exceeded", true},
		{"too many requests", true},
		{"requests per minute", true},
		{"RPM exceeded", true},
		{"TPM limit", true},
		{"concurrent requests", true},
		{"throttled", true},
		{"daily quota", false},
	}

	for _, tt := range tests {
		t.Run(tt.body, func(t *testing.T) {
			if got := looksLikeRateLimit(tt.body); got != tt.want {
				t.Errorf("looksLikeRateLimit(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}

func TestClassify429_QuotaExhausted(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Content-Type", "application/json")

	body := []byte(`{"error": {"message": "daily free allocation used up", "type": "quota_error"}}`)
	kind, retryAfter, _ := classify429(resp, body)

	require.Equal(t, core.ErrQuotaExhausted, kind)
	require.Equal(t, 30*time.Minute, retryAfter, "default quota cooldown should be 30m")
}

func TestClassify429_QuotaExhausted_WithRetryAfter(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", "3600")

	body := []byte(`{"error": {"message": "monthly quota exceeded"}}`)
	kind, retryAfter, _ := classify429(resp, body)

	require.Equal(t, core.ErrQuotaExhausted, kind)
	require.Equal(t, time.Hour, retryAfter, "should use upstream Retry-After for quota")
}

func TestClassify429_RateLimit(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}

	body := []byte(`{"error": {"message": "too many requests", "type": "rate_limit"}}`)
	kind, retryAfter, _ := classify429(resp, body)

	require.Equal(t, core.ErrRateLimit, kind)
	require.Equal(t, 5*time.Second, retryAfter, "default rate-limit backoff should be 5s")
}

func TestClassify429_RateLimit_WithRetryAfter(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", "10")

	body := []byte(`{"error": {"message": "rate limit exceeded"}}`)
	kind, retryAfter, _ := classify429(resp, body)

	require.Equal(t, core.ErrRateLimit, kind)
	require.Equal(t, 10*time.Second, retryAfter)
}

func TestClassify429_XRateLimitReset(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	// Set reset to 60 seconds from now.
	resetTime := time.Now().Add(60 * time.Second).Unix()
	resp.Header.Set("X-RateLimit-Reset", strconv.FormatInt(resetTime, 10))

	body := []byte(`{"error": {"message": "rate limit"}}`)
	kind, retryAfter, _ := classify429(resp, body)

	require.Equal(t, core.ErrRateLimit, kind)
	require.True(t, retryAfter > 55*time.Second && retryAfter <= 60*time.Second,
		"retryAfter should be ~60s, got %v", retryAfter)
}

func TestHTTPStatusError_429Quota(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
	}
	body := []byte(`{"error": {"message": "daily limit reached"}}`)

	err := httpStatusError("test", "model", resp, body)
	pe := core.AsProviderError(err)

	require.Equal(t, core.ErrQuotaExhausted, pe.Kind)
	require.Equal(t, 30*time.Minute, pe.RetryAfter)
}

func TestHTTPStatusError_429RateLimit(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
	}
	body := []byte(`{"error": {"message": "too many requests"}}`)

	err := httpStatusError("test", "model", resp, body)
	pe := core.AsProviderError(err)

	require.Equal(t, core.ErrRateLimit, pe.Kind)
	require.Equal(t, 5*time.Second, pe.RetryAfter)
}

func TestHTTPStatusError_429RateLimit_RetryAfter(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
	}
	resp.Header.Set("Retry-After", "120")
	body := []byte(`{"error": {"message": "rate limit"}}`)

	err := httpStatusError("test", "model", resp, body)
	pe := core.AsProviderError(err)

	require.Equal(t, core.ErrRateLimit, pe.Kind)
	require.Equal(t, 2*time.Minute, pe.RetryAfter)
}

func TestLooksLikeCreditsExhausted(t *testing.T) {
	tests := []struct {
		body string
		want bool
	}{
		{"", false},
		{"rate limit exceeded", false},
		{"daily quota exceeded", false},
		{"insufficient credits", true},
		{"Insufficient balance", true},
		{"insufficient funds for this request", true},
		{"insufficient_quota", true},
		{"You exceeded your current quota, please check your plan and billing details. insufficient_quota", true},
		{"out of credits", true},
		{"no credits remaining", true},
		{"Your credit balance is too low to access the API.", true},
		{"account balance depleted", true},
		{"please purchase more credits", true},
		{"top up your account to continue", true},
		{"stop updating the dashboard", false},
		{"Payment Required", true},
	}

	for _, tt := range tests {
		t.Run(tt.body, func(t *testing.T) {
			if got := looksLikeCreditsExhausted(tt.body); got != tt.want {
				t.Errorf("looksLikeCreditsExhausted(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}

func TestClassify429_CreditsExhausted(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	body := []byte(`{"error": {"message": "Insufficient credits. Please top up your account.", "type": "insufficient_quota"}}`)

	kind, _, credits := classify429(resp, body)

	require.Equal(t, core.ErrQuotaExhausted, kind)
	require.True(t, credits, "a dry balance must be flagged as credits exhausted")
}

func TestHTTPStatusError_402SetsCreditsExhausted(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusPaymentRequired,
		Header:     http.Header{},
	}
	body := []byte(`{"error": {"message": "Payment required"}}`)

	pe := core.AsProviderError(httpStatusError("test", "model", resp, body))

	require.Equal(t, core.ErrQuotaExhausted, pe.Kind)
	require.True(t, pe.CreditsExhausted)
	require.Equal(t, core.FailureScopeAccount, pe.EffectiveScope())
}

func TestGitHubMonthlyUsageRetryAfter(t *testing.T) {
	now := time.Date(2026, time.August, 4, 19, 30, 0, 0, time.UTC)
	body := []byte(`{"error":{"message":"You've reached your additional usage limit for your plan."}}`)

	wait := githubMonthlyUsageRetryAfter("github", http.StatusPaymentRequired, body, now)
	require.Equal(t, 27*24*time.Hour+4*time.Hour+30*time.Minute, wait)
	require.Zero(t, githubMonthlyUsageRetryAfter("github", http.StatusPaymentRequired, []byte("Payment required"), now))
	require.Zero(t, githubMonthlyUsageRetryAfter("other", http.StatusPaymentRequired, body, now))
}

func TestHTTPStatusError_GitHubMonthlyUsageIsResettableQuota(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusPaymentRequired, Header: http.Header{}}
	body := []byte(`{"error":{"message":"You've reached your additional usage limit for your plan."}}`)

	pe := core.AsProviderError(httpStatusError("github", "model", resp, body))

	require.Equal(t, core.ErrQuotaExhausted, pe.Kind)
	require.Equal(t, core.FailureScopeAccount, pe.EffectiveScope())
	require.False(t, pe.CreditsExhausted)
	require.Greater(t, pe.RetryAfter, 24*time.Hour)
	require.LessOrEqual(t, pe.RetryAfter, 32*24*time.Hour)
}

func TestHTTPStatusError_400CreditBalanceTooLow(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{},
	}
	body := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"Your credit balance is too low to access the API. Please go to Plans & Billing to upgrade or purchase credits."}}`)

	pe := core.AsProviderError(httpStatusError("test", "model", resp, body))

	require.Equal(t, core.ErrQuotaExhausted, pe.Kind,
		"a 400 describing a dry balance must fall back, not surface as bad request")
	require.True(t, pe.CreditsExhausted)
	require.Equal(t, core.FailureScopeAccount, pe.EffectiveScope())
}

func TestHTTPStatusError_403InsufficientBalance(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     http.Header{},
	}
	body := []byte(`{"error": {"message": "Insufficient Balance", "type": "unknown_error"}}`)

	pe := core.AsProviderError(httpStatusError("test", "model", resp, body))

	require.Equal(t, core.ErrQuotaExhausted, pe.Kind)
	require.True(t, pe.CreditsExhausted)
}

// inferhub answers a bid nobody meets with 402 no_provider_under_bid. That is a
// price miss for this request, not a depleted balance: the chain must advance
// without parking the account or cooling the model.
func TestHTTPStatusError_402NoProviderUnderBid(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusPaymentRequired, Header: http.Header{}}
	body := []byte(`{"error":{"code":"no_provider_under_bid","min_input_per_mtok":0.0024,"min_output_per_mtok":0.0096}}`)

	pe := core.AsProviderError(httpStatusError("inferhub", "ali/deepseek-v4.1-flash", resp, body))

	require.Equal(t, core.ErrModelUnavailable, pe.Kind)
	require.Equal(t, core.FailureScopeRequest, pe.Scope)
	require.False(t, pe.CreditsExhausted)
	require.True(t, pe.Fallbackable())
}

// Surplus answers 503 no_healthy_sellers when every seller for a model is
// down, and 404 no_sellers_for_model when nobody serves it. Both are seller
// stock for this model, shared by every buyer key: nothing on our side should
// cool down, and the client gets a short, fixed retry hint instead of the
// upstream Retry-After.
func TestHTTPStatusError_SurplusModelOutOfStock(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"503 no_healthy_sellers", http.StatusServiceUnavailable,
			`{"error":{"code":"no_healthy_sellers","message":"No healthy sellers. Contact support@surplusintelligence.ai","type":"service_unavailable"}}`},
		{"404 no_sellers_for_model", http.StatusNotFound,
			`{"error":{"code":"no_sellers_for_model","message":"nobody is serving gemini-3.8-flash"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tc.status, Header: http.Header{"Retry-After": []string{"180"}}}

			pe := core.AsProviderError(httpStatusError("surplus", "gemini-3.8-flash", resp, []byte(tc.body)))

			require.Equal(t, core.ErrModelOutOfStock, pe.Kind)
			require.Equal(t, core.FailureScopeRequest, pe.EffectiveScope())
			require.Equal(t, core.ModelOutOfStockRetryAfter, pe.RetryAfter)
			require.Equal(t, tc.status, pe.StatusCode)
			require.True(t, pe.Fallbackable())
		})
	}
}

// A plain 503 without the Surplus code keeps the existing upstream handling.
func TestHTTPStatusError_Plain503StaysUpstream(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}}

	pe := core.AsProviderError(httpStatusError("openai", "gpt-4o", resp, []byte(`{"error":{"message":"overloaded"}}`)))

	require.Equal(t, core.ErrUpstream, pe.Kind)
	require.Equal(t, core.FailureScopeProvider, pe.EffectiveScope())
}

// Surplus answers 404 minimum_discount_not_met when the X-Min-Discount floor
// excluded every seller. Nothing was charged and nothing on our side is at
// fault, so nothing cools down and the chain advances.
func TestHTTPStatusError_SurplusMinimumDiscountNotMet(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}}
	body := `{"error":{"type":"invalid_request_error","code":"minimum_discount_not_met","message":"No marketplace sellers currently meet the minimum 42% estimated discount for 'glm-5.3'. Best otherwise-eligible estimated discount is 18.4%."}}`

	pe := core.AsProviderError(httpStatusError("surplus", "glm-5.3", resp, []byte(body)))

	require.Equal(t, core.ErrMinDiscountNotMet, pe.Kind)
	require.Equal(t, core.FailureScopeRequest, pe.EffectiveScope())
	require.Equal(t, core.ModelOutOfStockRetryAfter, pe.RetryAfter)
	require.True(t, pe.Fallbackable())
	require.False(t, pe.Retryable())
}
