// Package transform converts between provider wire formats and KeiRouter's
// canonical core types.
//
// A Codec parses an inbound request body of a given dialect into a
// core.ChatRequest, renders a canonical request back out to that dialect for an
// upstream call, and converts upstream responses (both unary and streaming)
// back into canonical form. The pipeline pairs the client's inbound Codec with
// the chosen connector's upstream Codec, translating only when the two dialects
// differ.
package transform

import (
	"fmt"
	"io"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// Codec translates one dialect to and from the canonical model.
type Codec interface {
	// Dialect identifies the wire format this codec handles.
	Dialect() core.Dialect

	// ParseRequest decodes an inbound request body into canonical form.
	ParseRequest(body []byte) (*core.ChatRequest, error)

	// RenderRequest encodes a canonical request into this dialect's body for an
	// upstream call.
	RenderRequest(req *core.ChatRequest) ([]byte, error)

	// ParseResponse decodes a unary upstream response body into canonical form.
	ParseResponse(body []byte, model string) (*core.ChatResponse, error)

	// RenderResponse encodes a canonical response into this dialect's body for
	// returning to the client.
	RenderResponse(resp *core.ChatResponse) ([]byte, error)
}

// StreamCodec adds streaming translation for dialects that support SSE.
type StreamCodec interface {
	Codec

	// ParseStreamLine decodes a single SSE data line from the upstream into zero
	// or more canonical chunks. Returning (nil, nil) means "ignore this line"
	// (e.g. comments, blank keep-alives).
	ParseStreamLine(line []byte, model string) ([]core.StreamChunk, error)

	// RenderStreamChunk encodes a canonical chunk into one or more complete SSE
	// events (including the "data: " prefix and trailing blank line) for the
	// client. Returning an empty slice means "emit nothing for this chunk".
	RenderStreamChunk(chunk core.StreamChunk, state *StreamState) ([][]byte, error)

	// RenderStreamDone returns the terminal bytes to flush when the stream ends
	// (e.g. OpenAI's "data: [DONE]"). May be empty.
	RenderStreamDone(state *StreamState) [][]byte
}

// StreamState carries per-stream rendering state across chunks (message ids,
// whether the opening event was sent, running tool-call indices, ...). Each
// streaming response gets its own zero-valued StreamState.

// StreamingResponseCodec is optionally implemented by codecs that can parse a
// unary response directly from an io.Reader, avoiding the intermediate []byte
// allocation from io.ReadAll. Connectors check for this via type assertion and
// fall back to ParseResponse when absent.
type StreamingResponseCodec interface {
	// ParseResponseFrom decodes a unary upstream response from a stream reader
	// into canonical form. The reader is the raw HTTP response body.
	ParseResponseFrom(r io.Reader, model string) (*core.ChatResponse, error)
}
type StreamState struct {
	MessageID   string
	Model       string
	OpenedBlock bool
	SentRole    bool
	ToolIndex   int
	// Custom lets a codec stash dialect-specific bookkeeping.
	Custom map[string]any
	// ReasoningOrigin is who can verify the stream's thinking signatures.
	ReasoningOrigin string

	// oaiToolUpstream maps each OpenAI tool_calls index (the slice position)
	// to the upstream chunk index it was opened with.
	oaiToolUpstream []int
	oaiToolBuf      [8]int
}

// ResetStreamState resets a StreamState to its initial zero values.
// Called when a stream is being restarted or retried to prevent state
// corruption from a previous partial stream.
func ResetStreamState(state *StreamState) {
	state.MessageID = ""
	state.OpenedBlock = false
	state.SentRole = false
	state.ToolIndex = 0
	state.Custom = make(map[string]any)
	state.oaiToolUpstream = state.oaiToolUpstream[:0]
}

// hoistLeadingSystem folds system and developer messages that open the
// conversation into the system prompt, for dialects that have no such roles
// (Anthropic, Gemini). It returns req itself when there is nothing to move.
func hoistLeadingSystem(req *core.ChatRequest) *core.ChatRequest {
	n := 0
	for n < len(req.Messages) && (req.Messages[n].Role == core.RoleSystem || req.Messages[n].Role == core.RoleDeveloper) {
		n++
	}
	if n == 0 {
		return req
	}
	clone := *req
	for _, m := range req.Messages[:n] {
		clone.System = appendSystem(clone.System, messageText(m))
	}
	clone.Messages = req.Messages[n:]
	return &clone
}

func messageText(m core.Message) string {
	var b strings.Builder
	for _, p := range m.Content {
		if p.Type == core.PartText {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// Registry resolves codecs by dialect.
type Registry struct {
	codecs map[core.Dialect]Codec
}

// NewRegistry builds a registry from the given codecs.
func NewRegistry(codecs ...Codec) *Registry {
	m := make(map[core.Dialect]Codec, len(codecs))
	for _, c := range codecs {
		m[c.Dialect()] = c
	}
	return &Registry{codecs: m}
}

// DefaultRegistry returns a registry with all built-in codecs registered.
func DefaultRegistry() *Registry {
	return NewRegistry(
		OpenAICodec{},
		AnthropicCodec{},
		GeminiCodec{},
		OllamaCodec{},
		OpenAIResponsesCodec{},
		CommandCodeCodec{},
		KiroCodec{},
	)
}

// Codec returns the codec for a dialect, or an error if none is registered.
func (r *Registry) Codec(d core.Dialect) (Codec, error) {
	c, ok := r.codecs[d]
	if !ok {
		return nil, fmt.Errorf("transform: no codec for dialect %q", d)
	}
	return c, nil
}

// StreamCodec returns the codec for a dialect as a StreamCodec, or an error if
// it is not registered or does not support streaming.
func (r *Registry) StreamCodec(d core.Dialect) (StreamCodec, error) {
	c, err := r.Codec(d)
	if err != nil {
		return nil, err
	}
	sc, ok := c.(StreamCodec)
	if !ok {
		return nil, fmt.Errorf("transform: codec %q does not support streaming", d)
	}
	return sc, nil
}
