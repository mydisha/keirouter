package health

import (
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
)

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name string
		pe   *core.ProviderError
		want ProviderErrorType
	}{
		{"nil", nil, ProviderErrorNone},
		{"auth", &core.ProviderError{Kind: core.ErrAuth}, ProviderErrorAuth},
		{"rate_limit", &core.ProviderError{Kind: core.ErrRateLimit}, ProviderErrorRateLimited},
		{"quota", &core.ProviderError{Kind: core.ErrQuotaExhausted}, ProviderErrorQuotaExceeded},
		{"timeout", &core.ProviderError{Kind: core.ErrTimeout}, ProviderErrorTimeout},
		{"upstream_5xx", &core.ProviderError{Kind: core.ErrUpstream, StatusCode: 503}, ProviderErrorProvider5xx},
		{"upstream_network", &core.ProviderError{Kind: core.ErrUpstream, StatusCode: 0}, ProviderErrorNetwork},
		{"bad_request", &core.ProviderError{Kind: core.ErrBadRequest}, ProviderErrorBadRequest},
		{"bad_request_unsupported_msg", &core.ProviderError{Kind: core.ErrBadRequest, Message: "model not found"}, ProviderErrorUnsupported},
		{"model_unavailable", &core.ProviderError{Kind: core.ErrModelUnavailable}, ProviderErrorUnsupported},
		{"capability", &core.ProviderError{Kind: core.ErrCapability}, ProviderErrorUnsupported},
		{"cloudflare_bad_account_id", &core.ProviderError{Kind: core.ErrModelUnavailable, StatusCode: 404,
			Message: `{"errors":[{"code":7003,"message":"Could not route to /client/v4/accounts/%7BaccountId%7D/ai/v1/chat/completions, perhaps your object identifier is invalid?"}]}`},
			ProviderErrorAuth},
		{"internal", &core.ProviderError{Kind: core.ErrInternal}, ProviderErrorUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClassifyError(c.pe); got != c.want {
				t.Errorf("ClassifyError(%v) = %v, want %v", c.pe, got, c.want)
			}
		})
	}
}

func TestErrorSeverityScore(t *testing.T) {
	if ErrorSeverityScore(ProviderErrorNone) != 100 {
		t.Fatal("none should be 100")
	}
	if ErrorSeverityScore(ProviderErrorAuth) >= ErrorSeverityScore(ProviderErrorRateLimited) {
		t.Fatal("auth must be more severe than rate_limited")
	}
	if ErrorSeverityScore(ProviderErrorQuotaExceeded) >= ErrorSeverityScore(ProviderErrorProvider5xx) {
		t.Fatal("quota must be more severe than 5xx")
	}
}

func TestComputeScore_Healthy(t *testing.T) {
	score := ComputeScore(ScoreInput{
		SuccessRate:         1.0,
		P95LatencyMs:        2_000,
		LatencyThresholdMs:  10_000,
		DominantErrorType:   ProviderErrorNone,
		ConsecutiveFailures: 0,
	})
	if score < 90 {
		t.Fatalf("perfect signals should be healthy (>=90), got %d", score)
	}
}

func TestComputeScore_AuthErrors(t *testing.T) {
	score := ComputeScore(ScoreInput{
		SuccessRate:         0.5,
		P95LatencyMs:        1_000,
		LatencyThresholdMs:  10_000,
		DominantErrorType:   ProviderErrorAuth,
		ConsecutiveFailures: 3,
	})
	if score >= 65 {
		t.Fatalf("auth errors with failures should be unhealthy (<65), got %d", score)
	}
}

func TestComputeScore_HighLatency(t *testing.T) {
	score := ComputeScore(ScoreInput{
		SuccessRate:        0.99,
		P95LatencyMs:       20_000,
		LatencyThresholdMs: 10_000,
		DominantErrorType:  ProviderErrorNone,
	})
	if score >= 90 {
		t.Fatalf("2x threshold latency should not be healthy, got %d", score)
	}
}

func TestStatusFromScore(t *testing.T) {
	if StatusFromScore(95, true, false) != StatusHealthy {
		t.Fatal("95 should be healthy")
	}
	if StatusFromScore(70, true, false) != StatusDegraded {
		t.Fatal("70 should be degraded")
	}
	if StatusFromScore(40, true, false) != StatusUnhealthy {
		t.Fatal("40 should be unhealthy")
	}
	if StatusFromScore(100, false, false) != StatusUnknown {
		t.Fatal("no data should be unknown")
	}
	if StatusFromScore(10, true, true) != StatusDisabled {
		t.Fatal("disabled overrides")
	}
}

func TestMainIssue(t *testing.T) {
	if MainIssue(ProviderErrorRateLimited, 0, 10_000, 0) != "rate_limited" {
		t.Fatal("rate_limited issue")
	}
	if MainIssue(ProviderErrorNone, 20_000, 10_000, 0) != "high_latency" {
		t.Fatal("high_latency issue")
	}
	if MainIssue(ProviderErrorNone, 1_000, 10_000, 5) != "fallback_spike" {
		t.Fatal("fallback_spike issue")
	}
	if MainIssue(ProviderErrorNone, 1_000, 10_000, 0) != "" {
		t.Fatal("no issue expected")
	}
}

func TestRecommendationFor(t *testing.T) {
	if r := RecommendationFor(ProviderErrorAuth); r == "" {
		t.Fatal("auth recommendation must not be empty")
	}
	if r := RecommendationForIssue("high_latency"); r == "" {
		t.Fatal("high_latency recommendation must not be empty")
	}
	if r := RecommendationForIssue(""); r != "" {
		t.Fatal("empty issue should have empty recommendation")
	}
}

func TestPercentile(t *testing.T) {
	s := SortInts([]int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100})
	if p := Percentile(s, 50); p < 40 || p > 60 {
		t.Fatalf("p50 expected ~50, got %d", p)
	}
	if p := Percentile(s, 95); p < 90 {
		t.Fatalf("p95 expected near max, got %d", p)
	}
	if p := Percentile([]int{}, 50); p != 0 {
		t.Fatal("empty percentile should be 0")
	}
}
