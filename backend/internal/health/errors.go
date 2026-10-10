// Package health implements the actionable provider health dashboard core:
// error classification, health scoring, status derivation, recommendation
// generation, telemetry recording, and aggregation.
//
// Health telemetry is best-effort: recording failures must never break the
// gateway request path. The Service owns an async event queue and a background
// aggregator that rolls recent events into provider_health_current and
// provider_health_snapshots.
package health

import (
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// ProviderErrorType is the normalized error classification used to compare
// providers consistently regardless of upstream dialect.
type ProviderErrorType string

const (
	ProviderErrorNone          ProviderErrorType = "none"
	ProviderErrorAuth          ProviderErrorType = "auth_error"
	ProviderErrorRateLimited   ProviderErrorType = "rate_limited"
	ProviderErrorQuotaExceeded ProviderErrorType = "quota_exceeded"
	ProviderErrorTimeout       ProviderErrorType = "timeout"
	ProviderErrorProvider5xx   ProviderErrorType = "provider_5xx"
	ProviderErrorBadRequest    ProviderErrorType = "bad_request"
	ProviderErrorNetwork       ProviderErrorType = "network_error"
	ProviderErrorUnsupported   ProviderErrorType = "unsupported_model_or_capability"
	ProviderErrorUnknown       ProviderErrorType = "unknown"
)

// ClassifyError maps a core.ProviderError (already classified by the connector
// layer from HTTP status + messages) to the dashboard's normalized type. This
// keeps provider health comparable across upstreams.
func ClassifyError(pe *core.ProviderError) ProviderErrorType {
	if pe == nil {
		return ProviderErrorNone
	}
	switch pe.Kind {
	case core.ErrAuth:
		return ProviderErrorAuth
	case core.ErrRateLimit:
		return ProviderErrorRateLimited
	case core.ErrQuotaExhausted:
		return ProviderErrorQuotaExceeded
	case core.ErrTimeout:
		return ProviderErrorTimeout
	case core.ErrUpstream:
		// Distinguish a 5xx response from a connection-level failure: a
		// missing HTTP status means the request never reached the server
		// (DNS, connection refused, TLS), which is a network error.
		if pe.StatusCode == 0 {
			return ProviderErrorNetwork
		}
		return ProviderErrorProvider5xx
	case core.ErrModelUnavailable, core.ErrCapability:
		return ProviderErrorUnsupported
	case core.ErrBadRequest, core.ErrContextWindow, core.ErrContentFilter:
		if isUnsupportedMessage(pe.Message) {
			return ProviderErrorUnsupported
		}
		return ProviderErrorBadRequest
	default:
		// ErrInternal, ErrBudgetBlocked, ErrPolicyBlocked — not provider
		// failures, but surface as unknown for accounting completeness.
		return ProviderErrorUnknown
	}
}

// isUnsupportedMessage detects model/capability mismatch errors from the
// upstream message text when the connector could not classify them precisely.
func isUnsupportedMessage(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "model not found") ||
		strings.Contains(m, "does not support") ||
		strings.Contains(m, "not supported") ||
		strings.Contains(m, "unsupported model") ||
		strings.Contains(m, "model_not_found")
}

// ErrorSeverityScore returns the 0-100 quality score contribution for an error
// type. Auth and quota errors are severe (require user action); rate limits and
// transient 5xx are medium (fallback may resolve them).
func ErrorSeverityScore(t ProviderErrorType) int {
	switch t {
	case ProviderErrorNone:
		return 100
	case ProviderErrorBadRequest:
		return 80
	case ProviderErrorRateLimited:
		return 65
	case ProviderErrorTimeout:
		return 60
	case ProviderErrorProvider5xx:
		return 55
	case ProviderErrorNetwork:
		return 50
	case ProviderErrorUnknown:
		return 50
	case ProviderErrorQuotaExceeded:
		return 30
	case ProviderErrorAuth:
		return 10
	default:
		return 50
	}
}

// ErrorTypeCountColumn maps an error type to the snapshot counter column it
// increments. Used by the aggregator.
func ErrorTypeCountColumn(t ProviderErrorType) string {
	switch t {
	case ProviderErrorRateLimited:
		return "rate_limited_count"
	case ProviderErrorAuth:
		return "auth_error_count"
	case ProviderErrorQuotaExceeded:
		return "quota_exceeded_count"
	case ProviderErrorTimeout:
		return "timeout_count"
	case ProviderErrorProvider5xx:
		return "provider_5xx_count"
	case ProviderErrorBadRequest:
		return "bad_request_count"
	case ProviderErrorNetwork:
		return "network_error_count"
	case ProviderErrorUnsupported:
		return "unsupported_count"
	default:
		return "unknown_error_count"
	}
}
