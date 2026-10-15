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
