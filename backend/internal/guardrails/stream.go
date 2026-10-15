package guardrails

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mydisha/keirouter/backend/internal/core"
)

const (
	// streamHoldback is how many trailing bytes of streamed text are kept
	// back until more output arrives. A finding shorter than this (emails,
	// phone numbers, card numbers, most keys) is always seen whole before any
	// of it is forwarded, so it can be masked or blocked in time.
	streamHoldback = 96
	// streamMinRelease is the smallest slice released at once, to avoid a
	// rescan per token.
	streamMinRelease = 32
	// streamContext is how much already-forwarded text is prepended to every
	// scan so phrases split across a release boundary are still matched.
	streamContext = 96
	// streamMaxPending forces a release when a single finding keeps growing
	// (a long private key or URL) so the client is never starved.
	streamMaxPending = 8192
)

// StreamGuard runs outbound guardrails over a streamed response. Text deltas
// are held back by a small window: every release is scanned, masked, and only
// then forwarded, so leaked PII never reaches the client and a block cuts the
// stream before the offending text is sent.
//
// A StreamGuard is used by a single goroutine.
type StreamGuard struct {
	e       *Engine
	ctx     context.Context
	req     *core.ChatRequest
	policy  Policy
	pending string
	tail    string
	once    map[string]bool

	toolChecked bool
	toolResult  Result
}

// NewStreamGuard returns a guard for one streamed response, or nil when the
// effective policy has no outbound detector enabled (callers then forward
// chunks untouched).
func (e *Engine) NewStreamGuard(ctx context.Context, req *core.ChatRequest) *StreamGuard {
	if e == nil || req == nil || bypassed(ctx) {
		return nil
	}
	policy := e.policyFor(ctx, req)
	if !policy.IsActive() || !policy.scansOutput() {
		return nil
	}
	// Inbound normally attached the outcome already; this covers callers
	// that stream without an inbound pass.
	attachOutcome(req)
	return &StreamGuard{e: e, ctx: ctx, req: req, policy: policy, once: map[string]bool{}}
}

// ScansStreamOutput reports whether a streamed response to req must pass
// through a StreamGuard. Callers that could pipe raw upstream bytes straight
// to the client use it to keep such responses on the parsed path, where the
// guard can mask or block output before it is forwarded.
func (e *Engine) ScansStreamOutput(ctx context.Context, req *core.ChatRequest) bool {
	if e == nil || req == nil || bypassed(ctx) {
		return false
	}
	policy := e.policyFor(ctx, req)
	return policy.IsActive() && policy.scansOutput()
}

// scansOutput reports whether any detector inspects model output.
func (p Policy) scansOutput() bool {
	return (p.PII != nil && p.PII.Enabled && p.PII.ScanOutput) ||
		(p.Toxicity != nil && p.Toxicity.Enabled) ||
		(p.Bias != nil && p.Bias.Enabled) ||
		(p.Injection != nil && p.Injection.Enabled && (p.Injection.Canary || p.Injection.GuardToolCalls))
}

// Push adds a text delta. It returns the text that is now safe to forward
// (possibly empty while it is held back) and the scan result. On ActionBlock
// the caller must end the stream and must not forward anything else.
func (g *StreamGuard) Push(delta string) (string, Result) {
	if g == nil {
		return delta, Result{Action: ActionAllow}
	}
	g.pending += delta
	if len(g.pending) < streamHoldback+streamMinRelease {
		return "", Result{Action: ActionAllow}
	}
	return g.release(false)
}

// ToolCall vets a streamed tool call. It refuses the call when the policy
// guards tool calls and the inbound pass found that this turn's tool results
// look like a prompt injection: injected text must not be able to trigger
// actions. The verdict is reported once per stream.
func (g *StreamGuard) ToolCall() Result {
	if g == nil {
		return Result{Action: ActionAllow}
	}
	inj := g.policy.Injection
	if inj == nil || !inj.Enabled || !inj.GuardToolCalls || !g.req.Metadata.UntrustedToolInput {
		return Result{Action: ActionAllow}
	}
	if g.toolChecked {
		return g.toolResult
	}
	g.toolChecked = true
	action := inj.Action
	if action == "" {
		action = DefaultInjectionAction
	}
	dec := Decision{
		Detector:  "injection",
		Direction: DirectionOutbound,
		Action:    action,
		Severity:  SeverityHigh,
		Reason:    "injection: tool call refused after tool results that look like a prompt injection",
		Findings:  []Finding{{Entity: "PROMPT_INJECTION:tainted_tool_call", Score: 1, Start: -1, End: -1}},
	}
	g.e.report(g.req, &dec)
	g.toolResult = Result{Action: ActionAllow}
	g.toolResult.add(dec)
	return g.toolResult
}

// Flush releases everything still held back. Call it before forwarding any
// non-text event (so ordering is preserved) and when the upstream ends.
func (g *StreamGuard) Flush() (string, Result) {
	if g == nil || g.pending == "" {
		return "", Result{Action: ActionAllow}
	}
	return g.release(true)
}

func (g *StreamGuard) release(final bool) (string, Result) {
	scan := g.tail + g.pending
	off := len(g.tail)
	res := g.e.evalOutbound(g.ctx, g.policy, &OutboundResponse{Request: g.req, Text: scan, Streaming: true}, false)

	cut := len(g.pending)
	if !final {
		cut = safeCut(g.pending, len(g.pending)-streamHoldback)
		// Never split a finding: move the cut to its start.
		for moved := true; moved; {
			moved = false
			for _, d := range res.Decisions {
				for _, f := range d.Findings {
					s, e := f.Start-off, f.End-off
					if f.Start >= 0 && s < cut && e > cut {
						cut = max(s, 0)
						moved = true
					}
				}
			}
		}
		if cut <= 0 {
			if len(g.pending) < streamMaxPending {
				return "", Result{Action: ActionAllow}
			}
			cut = len(g.pending)
		}
	}

	// Report only what is being released now: findings inside the context
	// prefix were reported with an earlier release, and findings past the
	// cut will be reported with a later one.
	out := Result{Action: ActionAllow}
	masked := false
	for _, d := range res.Decisions {
		kept := d.Findings[:0:0]
		for _, f := range d.Findings {
			if f.Start < 0 {
				key := d.Detector + "|" + f.Entity
				if g.once[key] {
					continue
				}
				g.once[key] = true
				kept = append(kept, f)
				continue
			}
			s := f.Start - off
			if s >= 0 && s < cut {
				f.Start, f.End = s, f.End-off
				kept = append(kept, f)
			}
		}
		if len(d.Findings) == 0 {
			key := d.Detector + "|"
			if g.once[key] {
				continue
			}
			g.once[key] = true
		} else if len(kept) == 0 {
			continue
		}
		d.Findings = kept
		if d.Action == ActionMask {
			masked = true
		}
		out.add(d)
	}
	for i := range out.Decisions {
		g.e.report(g.req, &out.Decisions[i])
	}
	if out.Action == ActionBlock {
		g.pending = ""
		return "", out
	}

	emit := g.pending[:cut]
	raw := emit
	if masked {
		emit = g.e.evalOutbound(g.ctx, g.policy, &OutboundResponse{Request: g.req, Text: emit, Streaming: true}, false).Text
	}
	g.pending = g.pending[cut:]
	g.tail = lastBytes(g.tail+raw, streamContext)
	out.Text = emit
	return emit, out
}

// safeCut picks a release boundary at or before target: right after the last
// whitespace in the held-back window, or at a rune boundary if there is none.
func safeCut(s string, target int) int {
	if target <= 0 {
		return 0
	}
	if target > len(s) {
		target = len(s)
	}
	floor := max(target-streamHoldback, 0)
	for i := target - 1; i >= floor; i-- {
		if s[i] < utf8.RuneSelf && unicode.IsSpace(rune(s[i])) {
			return i + 1
		}
	}
	for target > 0 && !utf8.RuneStart(s[target]) {
		target--
	}
	return target
}

func lastBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := len(s) - n
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return strings.Clone(s[i:])
}
