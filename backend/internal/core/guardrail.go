package core

import (
	"sync"
	"time"
)

// GuardrailOutcome accumulates what the guardrails did to one request so its
// terminal usage row can record it. It is held by pointer in RequestMetadata:
// the pipeline copies metadata by value, and every copy must observe decisions
// made later (the streamed-output guard runs after those copies are taken).
//
// The guardrails engine owns action ranking; the outcome only keeps the
// decision with the highest rank it was handed.
type GuardrailOutcome struct {
	mu        sync.Mutex
	action    string
	rank      int
	detector  string
	direction string
	midStream bool
	elapsed   time.Duration
}

// GuardrailSummary is a point-in-time copy of a GuardrailOutcome.
type GuardrailSummary struct {
	// Action is the strictest action taken: "allow" when guardrails evaluated
	// the request and nothing fired, "" when no guardrail policy applied.
	Action    string
	Detector  string
	Direction string
	// MidStream is true when a streamed response was blocked after part of
	// it had already reached the client.
	MidStream bool
	Elapsed   time.Duration
}

// Note records a decision. It replaces the current one only when rank is
// strictly higher, so the first detector to reach the strictest action wins.
func (o *GuardrailOutcome) Note(action string, rank int, detector, direction string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	if rank > o.rank {
		o.action, o.rank, o.detector, o.direction = action, rank, detector, direction
	}
	o.mu.Unlock()
}

// AddElapsed adds detector evaluation time.
func (o *GuardrailOutcome) AddElapsed(d time.Duration) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.elapsed += d
	o.mu.Unlock()
}

// MarkMidStream flags that the block cut a stream the client was already
// receiving.
func (o *GuardrailOutcome) MarkMidStream() {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.midStream = true
	o.mu.Unlock()
}

// Summary returns the accumulated outcome. A nil outcome means guardrails
// never evaluated the request.
func (o *GuardrailOutcome) Summary() GuardrailSummary {
	if o == nil {
		return GuardrailSummary{}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	s := GuardrailSummary{
		Action:    o.action,
		Detector:  o.detector,
		Direction: o.direction,
		MidStream: o.midStream,
		Elapsed:   o.elapsed,
	}
	if s.Action == "" {
		s.Action = "allow"
	}
	return s
}
