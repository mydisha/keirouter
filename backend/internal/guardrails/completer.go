package guardrails

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// Completer runs a chat completion on behalf of the guardrails (LLM judges
// for prompt injection and topics), routed through KeiRouter's own
// providers. The gateway implements it.
type Completer interface {
	Complete(ctx context.Context, model string, req *core.ChatRequest) (*core.ChatResponse, error)
}

// VerificationCode returns a random code a judge must echo back. Untrusted
// text that takes over the judge can't know it, so a missing or wrong code
// exposes the takeover.
func VerificationCode() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// JudgeClientKind is the client recorded on the usage rows of every LLM judge
// call, so guardrail overhead can be told apart from client traffic.
const JudgeClientKind = "guardrails-judge"

// JudgeMetadata derives the metadata of a judge call from the request that
// triggered it, so its usage is metered against the same key. detector names
// the guardrail that asked; it suffixes the request id, keeping the judge row
// correlated with (but distinct from) the triggering request.
func JudgeMetadata(meta core.RequestMetadata, detector string) core.RequestMetadata {
	out := core.RequestMetadata{
		ClientKind: JudgeClientKind,
		APIKeyID:   meta.APIKeyID,
		TenantID:   meta.TenantID,
		ProjectID:  meta.ProjectID,
		RequestID:  meta.RequestID,
	}
	if out.RequestID != "" {
		out.RequestID += "-guardrails-" + detector
	}
	return out
}

// JSONObject extracts the outermost {...} of a model reply (models like to
// wrap JSON in prose or code fences).
func JSONObject(text string) (string, bool) {
	start, end := strings.IndexByte(text, '{'), strings.LastIndexByte(text, '}')
	if start < 0 || end <= start {
		return "", false
	}
	return text[start : end+1], true
}

// ClipMiddle keeps the head and tail of long text, where instructions and
// the actual question usually sit.
func ClipMiddle(s string, n int) string {
	if len(s) <= n {
		return s
	}
	half := n / 2
	head, tail := half, len(s)-half
	for head > 0 && s[head]&0xC0 == 0x80 {
		head--
	}
	for tail < len(s) && s[tail]&0xC0 == 0x80 {
		tail++
	}
	return s[:head] + "\n[…]\n" + s[tail:]
}
