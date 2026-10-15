// Package guardrails is KeiRouter's content-safety layer. It runs pluggable
// detectors against inbound prompts and outbound responses, using a per-scope
// policy (global < provider < model < chain < apikey) merged at request time
// so the most specific override wins.
//
// Detectors implement the Detector interface and are registered with the
// Engine at startup. Each detector reads its slice of the merged Policy and
// returns a Decision; the Engine aggregates Decisions into a final action
// (Allow, Warn, Mask, Block) and emits an audit log row asynchronously.
package guardrails

import (
	"encoding/json"
)

// Action is what the engine will do with a request given a detector finding.
// Multiple detectors can fire on the same request; the strictest action wins
// (Block > Mask > Warn > LogOnly > Allow).
type Action string

const (
	ActionAllow   Action = "allow"
	ActionLogOnly Action = "log_only"
	ActionWarn    Action = "warn"
	ActionMask    Action = "mask"
	ActionBlock   Action = "block"
)

// rank orders actions by strictness for aggregation.
func rank(a Action) int {
	switch a {
	case ActionBlock:
		return 4
	case ActionMask:
		return 3
	case ActionWarn:
		return 2
	case ActionLogOnly:
		return 1
	default:
		return 0
	}
}

// StrictestAction returns the more restrictive of two actions.
func StrictestAction(a, b Action) Action {
	if rank(a) >= rank(b) {
		return a
	}
	return b
}

// Direction is whether the engine is inspecting the prompt going to the
// provider or the response coming back.
type Direction string

const (
	DirectionInbound  Direction = "inbound"
	DirectionOutbound Direction = "outbound"
)

// Severity labels the seriousness of a finding. Detectors map their internal
// confidence to one of these buckets, and policies may set a threshold to
// suppress decisions below a given level.
type Severity string

const (
	SeverityLow    Severity = "low"
	SeverityMedium Severity = "medium"
	SeverityHigh   Severity = "high"
)

// severityRank lets us compare against a configured threshold.
func severityRank(s Severity) int {
	switch s {
	case SeverityHigh:
		return 3
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 1
	default:
		return 0
	}
}

// MeetsThreshold reports whether s is at least as severe as threshold.
func MeetsThreshold(s, threshold Severity) bool {
	if threshold == "" {
		return true
	}
	return severityRank(s) >= severityRank(threshold)
}

// Finding is one match produced by a detector. Findings are exposed in audit
// logs so users can investigate false positives.
type Finding struct {
	// Entity is the canonical type name (e.g. "EMAIL_ADDRESS", "PROMPT_INJECTION").
	// Naming follows Microsoft Presidio for forward compatibility with a
	// Presidio sidecar.
	Entity string `json:"entity"`
	// Score is the detector's confidence, 0.0–1.0. Presidio-compatible.
	Score float64 `json:"score"`
	// Start and End are byte offsets into the inspected text (-1 when n/a).
	Start int `json:"start"`
	End   int `json:"end"`
	// Original holds the matched substring (truncated to 64 chars in logs).
	Original string `json:"original,omitempty"`
	// Redacted is the replacement applied when Action == Mask.
	Redacted string `json:"redacted,omitempty"`
}

// Decision is what a detector wants the engine to do for this request.
// A nil Decision is equivalent to ActionAllow with no findings.
type Decision struct {
	Detector  string    `json:"detector"`
	Action    Action    `json:"action"`
	Severity  Severity  `json:"severity,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Findings  []Finding `json:"findings,omitempty"`
	Direction Direction `json:"direction,omitempty"`
	// Mutated holds the rewritten content when Action == Mask. The engine
	// applies it back to the request before dispatch.
	Mutated string `json:"-"`
	// MutatedField indicates which textual surface was rewritten. The pipeline
	// re-injects the value into the corresponding field.
	MutatedField MutatedField `json:"-"`
}

// MutatedField identifies which request surface a detector rewrote.
type MutatedField string

const (
	MutatedFieldNone     MutatedField = ""
	MutatedFieldMessages MutatedField = "messages"
	MutatedFieldSystem   MutatedField = "system"
	MutatedFieldResponse MutatedField = "response"
	// MutatedFieldInPlace means the detector already rewrote every affected
	// field of the live request or response itself; Mutated is unused.
	MutatedFieldInPlace MutatedField = "in_place"
)

// MarshalDecisionFindings serializes findings into the JSON column shape used
// by the audit log table.
func MarshalDecisionFindings(d *Decision) string {
	if d == nil || len(d.Findings) == 0 {
		return "[]"
	}
	b, err := json.Marshal(d.Findings)
	if err != nil {
		return "[]"
	}
	return string(b)
}
