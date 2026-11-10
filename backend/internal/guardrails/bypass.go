package guardrails

import "context"

type bypassKey struct{}

// Bypass marks ctx so the engine skips every detector. It is for requests
// KeiRouter makes on its own behalf, such as an LLM judge classifying a
// prompt, which would otherwise be inspected (and judged) recursively.
// Only server code can set it; nothing from a client reaches the context.
func Bypass(ctx context.Context) context.Context {
	return context.WithValue(ctx, bypassKey{}, true)
}

func bypassed(ctx context.Context) bool {
	v, _ := ctx.Value(bypassKey{}).(bool)
	return v
}

type scanOnlyKey struct{}

// ScanOnly marks ctx so detectors report decisions and masks but leave out
// request hardening meant for model prompts (spotlight delimiters, canary
// markers). Callers that inspect text which never reaches a model as a
// prompt, such as MCP tool arguments and results, set it.
func ScanOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, scanOnlyKey{}, true)
}

// IsScanOnly reports whether ctx was marked by ScanOnly.
func IsScanOnly(ctx context.Context) bool {
	v, _ := ctx.Value(scanOnlyKey{}).(bool)
	return v
}
