package core

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrorKind classifies an upstream failure so the dispatcher can decide whether
// to retry the same account, fall back to the next account/model, or surface
// the error to the client immediately.
type ErrorKind string

const (
	// ErrAuth: credential rejected/expired. Try refresh, then next account.
	ErrAuth ErrorKind = "auth"
	// ErrRateLimit: quota/rate exceeded. Cool down this account, fall back.
	ErrRateLimit ErrorKind = "rate_limit"
	// ErrQuotaExhausted: subscription/budget fully consumed. Fall back, longer cooldown.
	ErrQuotaExhausted ErrorKind = "quota_exhausted"
	// ErrUpstream: 5xx / transient upstream fault. Retry then fall back.
	ErrUpstream ErrorKind = "upstream"
	// ErrTimeout: stream stalled or request timed out. Retry then fall back.
	ErrTimeout ErrorKind = "timeout"
	// ErrBadRequest: 4xx caused by the request itself. Do NOT fall back; surface.
	ErrBadRequest ErrorKind = "bad_request"
	// ErrContextWindow: the prompt exceeds the model's context window. The
	// request is valid for a model with a larger window, so the chain advances
	// to the next target (LiteLLM's context_window_fallbacks) without cooling
	// anything down. Surfaced as 400 when every target rejects it.
	ErrContextWindow ErrorKind = "context_window"
	// ErrContentFilter: the upstream safety system rejected the prompt or
	// output. Another provider may accept it (LiteLLM's
	// content_policy_fallbacks), so the chain advances; nothing is cooled down.
	ErrContentFilter ErrorKind = "content_filter"
	// ErrModelUnavailable: the selected model or endpoint is unavailable. Skip
	// the model without disabling credentials that may still serve other models.
	ErrModelUnavailable ErrorKind = "model_unavailable"
	// ErrCapability: chosen model lacks a required capability. Skip, no surface.
	ErrCapability ErrorKind = "capability"
	// ErrBudgetBlocked: KeiRouter budget guard rejected before dispatch.
	ErrBudgetBlocked ErrorKind = "budget_blocked"
	// ErrPolicyBlocked: a guardrail policy rejected the request before dispatch.
	// Not fallbackable — falling back to another provider would not change the
	// outcome of a content-safety decision.
	ErrPolicyBlocked ErrorKind = "policy_blocked"
	// ErrInternal: router-internal fault.
	ErrInternal ErrorKind = "internal"
	// ErrClientCanceled: the client disconnected mid-request. Not a provider
	// failure — must not trigger cooldowns or health penalties.
	ErrClientCanceled ErrorKind = "client_canceled"
)

// FailureScope identifies the narrowest resource affected by a provider error.
// Cooldowns and circuit breakers use this to avoid disabling healthy siblings.
type FailureScope string

const (
	FailureScopeRequest  FailureScope = "request"
	FailureScopeModel    FailureScope = "model"
	FailureScopeAccount  FailureScope = "account"
	FailureScopeProvider FailureScope = "provider"
	FailureScopeNetwork  FailureScope = "network"
)

// ProviderError is the structured error connectors and the pipeline return. It
// carries enough context for the dispatcher to make a fallback decision and for
// the gateway to render an accurate HTTP status to the client.
type ProviderError struct {
	Kind ErrorKind
	// Scope is the narrowest routing resource affected by the failure.
	Scope FailureScope
	// Provider and Model identify where the failure originated.
	Provider string
	Model    string
	// StatusCode is the upstream HTTP status, if any.
	StatusCode int
	// Message is a human-readable summary safe to log.
	Message string
	// RetryAfter, when non-zero, is the upstream-suggested cooldown.
	RetryAfter time.Duration
	// CreditsExhausted marks a quota_exhausted error caused by a depleted
	// paid balance rather than a scheduled quota window. A dry balance never
	// recovers on its own — the user must top up — so the dispatcher parks
	// the account for a long horizon and flags it for the dashboard.
	CreditsExhausted bool
	// RetrySystemInstruction requests one centrally routed retry with an
	// additional system instruction. Connectors use this for completed
	// responses that are structurally invalid or clearly incomplete.
	RetrySystemInstruction string
	// AttemptUsage captures tokens consumed by a completed attempt that is being
	// retried for response integrity. The pipeline adds it to terminal usage.
	AttemptUsage *Usage
	// Cause is the wrapped underlying error.
	Cause error
}

func (e *ProviderError) Error() string {
	if e.Provider != "" {
		return fmt.Sprintf("%s: %s (provider=%s model=%s status=%d)",
			e.Kind, e.Message, e.Provider, e.Model, e.StatusCode)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func (e *ProviderError) Unwrap() error { return e.Cause }

// Retryable reports whether trying the same model again (possibly on another
// account) could succeed.
func (e *ProviderError) Retryable() bool {
	switch e.Kind {
	case ErrUpstream, ErrTimeout, ErrRateLimit:
		return true
	default:
		return false
	}
}

// Fallbackable reports whether the dispatcher should advance to the next
// candidate in the chain rather than surfacing this error to the client.
func (e *ProviderError) Fallbackable() bool {
	switch e.Kind {
	case ErrBadRequest, ErrBudgetBlocked, ErrPolicyBlocked, ErrClientCanceled:
		return false
	default:
		return true
	}
}

// EffectiveScope returns the explicit failure scope or a conservative default
// derived from the error kind.
func (e *ProviderError) EffectiveScope() FailureScope {
	if e == nil {
		return FailureScopeRequest
	}
	if e.Scope != "" {
		return e.Scope
	}
	switch e.Kind {
	case ErrModelUnavailable, ErrCapability, ErrContextWindow:
		return FailureScopeModel
	case ErrAuth, ErrRateLimit, ErrQuotaExhausted:
		return FailureScopeAccount
	case ErrUpstream, ErrTimeout:
		return FailureScopeProvider
	default:
		return FailureScopeRequest
	}
}

// RetryDecision is the stable routing view of a ProviderError.
type RetryDecision struct {
	Retryable    bool
	Fallbackable bool
	Scope        FailureScope
	RetryAfter   time.Duration
}

// Decision returns the retry and fallback policy carried by this error.
func (e *ProviderError) Decision() RetryDecision {
	if e == nil {
		return RetryDecision{}
	}
	return RetryDecision{
		Retryable:    e.Retryable(),
		Fallbackable: e.Fallbackable(),
		Scope:        e.EffectiveScope(),
		RetryAfter:   e.RetryAfter,
	}
}

// IsClientDisconnect reports whether err indicates the client disconnected
// rather than a provider connection failure. Socket text is intentionally not
// inspected here because the same reset strings can originate upstream.
func IsClientDisconnect(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	var pe *ProviderError
	return errors.As(err, &pe) && pe.Kind == ErrClientCanceled
}

// AsProviderError extracts a *ProviderError from err, or wraps it as ErrInternal.
func AsProviderError(err error) *ProviderError {
	if err == nil {
		return nil
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe
	}
	if errors.Is(err, context.Canceled) {
		return &ProviderError{
			Kind:    ErrClientCanceled,
			Scope:   FailureScopeRequest,
			Message: "client canceled request",
			Cause:   err,
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &ProviderError{
			Kind:    ErrTimeout,
			Scope:   FailureScopeRequest,
			Message: "request deadline exceeded",
			Cause:   err,
		}
	}
	return &ProviderError{Kind: ErrInternal, Message: err.Error(), Cause: err}
}

// NewProviderError constructs a ProviderError with the given kind and message.
func NewProviderError(kind ErrorKind, msg string) *ProviderError {
	return &ProviderError{Kind: kind, Message: msg}
}
