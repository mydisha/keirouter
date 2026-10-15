package guardrails

import (
	"context"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// Detector is the plugin contract every guardrail implements. New detectors
// drop into the Engine via Register; the rest of the system stays oblivious
// to which detectors are configured.
//
// Inbound runs against the request before dispatch. A Decision with
// Action == ActionMask MUST populate Mutated and MutatedField so the engine
// can apply the rewrite back to the request.
//
// Outbound runs against an LLM response. Detectors that only inspect input
// return nil from Outbound; engines short-circuit on nil.
type Detector interface {
	Name() string
	Inbound(ctx context.Context, in *InboundRequest, policy Policy) (*Decision, error)
	Outbound(ctx context.Context, out *OutboundResponse, policy Policy) (*Decision, error)
}

// CapabilityReporter is implemented by detectors whose optional engines
// depend on server configuration (an ML sidecar, an embedder, an LLM judge),
// so the dashboard can tell which policy options will take effect.
type CapabilityReporter interface {
	Capabilities() map[string]bool
}

// InboundRequest is the canonical view of a prompt that detectors inspect.
// It is built once by the engine and shared across all detectors so each one
// avoids re-scanning the underlying ChatRequest.
type InboundRequest struct {
	// Source is a pointer to the live ChatRequest so detectors can rewrite
	// fields in place (after producing a Decision with Action == ActionMask).
	Source *core.ChatRequest

	// FlatText is the concatenated user-visible content (System + Messages)
	// with delimiters, used by text-based detectors so they don't have to walk
	// the message slice themselves.
	FlatText string

	views *inboundViews
}

func (in *InboundRequest) view() *inboundViews {
	if in.views == nil {
		if in.Source == nil {
			in.views = &inboundViews{user: in.FlatText, recent: in.FlatText, lastUser: in.FlatText}
		} else {
			in.views = buildViews(in.Source)
		}
	}
	return in.views
}

// UserText is the text of every user message. It is the untrusted surface
// that classifiers (injection, toxicity, topics) evaluate: the operator's
// system prompt and earlier model turns are excluded so instructions such as
// "act as a support agent" in a system prompt never trip a detector.
func (in *InboundRequest) UserText() string { return in.view().user }

// RecentUserText is the text of the last few user messages.
func (in *InboundRequest) RecentUserText() string { return in.view().recent }

// ToolText is the content of tool results fed back to the model, the channel
// used by indirect prompt injection (web pages, files, API responses).
func (in *InboundRequest) ToolText() string { return in.view().tools }

// RecentToolText is the content of tool results in the current turn (after
// the last user message), which the model is about to act on.
func (in *InboundRequest) RecentToolText() string { return in.view().recentTools }

// LastUserText is the text of the most recent user message.
func (in *InboundRequest) LastUserText() string { return in.view().lastUser }

// Refresh rebuilds the cached views after the Source request was rewritten.
func (in *InboundRequest) Refresh() {
	in.views = nil
	if in.Source != nil {
		in.FlatText = flattenRequest(in.Source)
	}
}

// OutboundResponse is the canonical view of an LLM completion handed to
// outbound detectors. For streaming, the engine assembles the response as
// chunks arrive — see Engine.ScanChunk.
type OutboundResponse struct {
	Source *core.ChatResponse

	// Request is the request that produced this response, for detectors
	// that act on state set during the inbound pass.
	Request *core.ChatRequest

	// Text is the model's textual output (concatenated content parts).
	Text string

	// Streaming is true when the engine is inspecting accumulating chunks
	// rather than a finalized response. Detectors that need the full text to
	// be useful (e.g. coherence-level bias detection) may return nil for
	// streaming calls and act only on the final assembled text.
	Streaming bool
}
