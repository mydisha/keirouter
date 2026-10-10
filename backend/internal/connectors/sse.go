package connectors

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// Shared SSE/NDJSON pump used by every streaming connector. It owns the three
// behaviours that used to be duplicated (and subtly wrong) per connector:
//
//   - every send to the output channel is guarded by ctx so a producer can
//     never block forever once the consumer is gone (a full 16-slot buffer
//     plus an unguarded terminal send leaked the goroutine, the scanner buffer
//     and the upstream connection);
//   - scanner failures are classified instead of being labelled "timeout":
//     an over-long frame is a request-scoped upstream fault, a read error is
//     a network fault, a deadline is a timeout, and a cancellation is silent;
//   - SSE comment heartbeats become ChunkPing so the pipeline stall detector
//     sees that the upstream is alive during long "thinking" pauses.

// sseMaxLineBytes bounds a single SSE/NDJSON frame. Base64 images in Gemini
// streams and multi-megabyte tool arguments exceed the old 2 MiB limit; the
// scanner only grows its buffer on demand, so the ceiling costs nothing for
// ordinary streams.
const sseMaxLineBytes = 16 << 20

// sseLineMode selects how raw lines are turned into payloads.
type sseLineMode int

const (
	// sseDataLines keeps only "data:" lines; ":" comments become pings.
	sseDataLines sseLineMode = iota
	// ndjsonLines forwards every non-empty line verbatim.
	ndjsonLines
)

// errStopStream may be returned by a parse callback to end the stream
// cleanly (for example on a provider-specific terminal sentinel).
var errStopStream = errors.New("stop stream")

// ssePump drives one upstream stream to completion.
type ssePump struct {
	ctx      context.Context
	provider string
	model    string
	out      chan<- core.StreamChunk
	ttft     *ttftTracker
	// errored is set once an error chunk has been delivered.
	errored bool
}

func newSSEPump(ctx context.Context, provider, model string, out chan<- core.StreamChunk, cfg core.StreamConfig) *ssePump {
	return &ssePump{ctx: ctx, provider: provider, model: model, out: out, ttft: newTTFTTracker(cfg)}
}

// emit forwards one chunk, returning false when the consumer is gone.
func (p *ssePump) emit(ch core.StreamChunk) bool {
	if ch.Type == core.ChunkError {
		p.errored = true
		if ch.Err != nil {
			ch.Err = p.attribute(ch.Err)
		}
	} else if p.ttft != nil {
		p.ttft.maybeReport(ch)
	}
	select {
	case p.out <- ch:
		return true
	case <-p.ctx.Done():
		return false
	}
}

// emitAll forwards a batch, stopping at the first error chunk (nothing after
// an in-band error is meaningful) and returning false if the consumer left or
// an error was delivered.
func (p *ssePump) emitAll(chunks []core.StreamChunk) bool {
	for _, ch := range chunks {
		if !p.emit(ch) {
			return false
		}
		if ch.Type == core.ChunkError {
			return false
		}
	}
	return true
}

// emitError delivers a terminal error chunk (guarded).
func (p *ssePump) emitError(err error) {
	if err == nil {
		return
	}
	p.emit(core.StreamChunk{Type: core.ChunkError, Err: err})
}

// attribute fills provider/model on errors produced by dialect codecs, which
// know the model but not which connector is driving them.
func (p *ssePump) attribute(err error) error {
	pe := core.AsProviderError(err)
	if pe.Provider == "" || pe.Model == "" {
		cp := *pe
		if cp.Provider == "" {
			cp.Provider = p.provider
		}
		if cp.Model == "" {
			cp.Model = p.model
		}
		return &cp
	}
	return pe
}

// run scans body line by line, hands each payload to parse, and forwards the
// resulting chunks. It returns once the stream ends, the consumer leaves, an
// error chunk is delivered, or parse reports a fatal error. Scanner failures
// are classified and delivered as a terminal error chunk.
func (p *ssePump) run(body io.Reader, mode sseLineMode, parse func(payload []byte) ([]core.StreamChunk, error)) {
	scanner := sseScanner(body)
	for scanner.Scan() {
		if p.ctx.Err() != nil {
			return
		}
		line := scanner.Bytes()
		var payload []byte
		switch mode {
		case sseDataLines:
			data, ok := sseDataPayload(line)
			if !ok {
				if isSSEKeepAliveBytes(line) && !p.emit(core.StreamChunk{Type: core.ChunkPing}) {
					return
				}
				continue
			}
			payload = data
		default:
			payload = bytes.TrimSpace(line)
			if len(payload) == 0 {
				continue
			}
		}
		// One copy per frame: the scanner reuses its buffer on the next Scan
		// and codecs may retain json.RawMessage slices of the input.
		payload = append([]byte(nil), payload...)
		chunks, perr := parse(payload)
		if errors.Is(perr, errStopStream) {
			p.emitAll(chunks)
			return
		}
		if perr != nil {
			// A malformed frame is skipped rather than aborting the stream.
			continue
		}
		if !p.emitAll(chunks) {
			return
		}
	}
	if err := scanner.Err(); err != nil {
		if pe := p.scanError(err); pe != nil {
			p.emitError(pe)
		}
	}
}

// scanError classifies a scanner failure. It returns nil when the failure is
// a cancellation (client left or the pipeline tore the stream down), which
// the consumer already knows about.
func (p *ssePump) scanError(err error) *core.ProviderError {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), p.ctx.Err() != nil && errors.Is(p.ctx.Err(), context.Canceled):
		return nil
	case errors.Is(err, bufio.ErrTooLong):
		return &core.ProviderError{Kind: core.ErrUpstream, Scope: core.FailureScopeRequest, Provider: p.provider, Model: p.model,
			Message: "upstream stream frame exceeds the maximum supported size", Cause: err}
	case errors.Is(err, context.DeadlineExceeded):
		return &core.ProviderError{Kind: core.ErrTimeout, Scope: core.FailureScopeRequest, Provider: p.provider, Model: p.model,
			Message: "stream read deadline exceeded", Cause: err}
	case isTimeoutNetError(err):
		return &core.ProviderError{Kind: core.ErrTimeout, Scope: core.FailureScopeProvider, Provider: p.provider, Model: p.model,
			Message: "stream read timed out: " + err.Error(), Cause: err}
	}
	var pe *core.ProviderError
	if errors.As(err, &pe) {
		return pe
	}
	var opErr *net.OpError
	_ = errors.As(err, &opErr)
	return &core.ProviderError{Kind: core.ErrUpstream, Scope: core.FailureScopeNetwork, Provider: p.provider, Model: p.model,
		Message: "stream read failed: " + err.Error(), Cause: err}
}

// streamSSE is the one-call form used by connectors whose stream is a plain
// SSE/NDJSON body: it closes the body and the output channel when done.
func streamSSE(ctx context.Context, provider, model string, body io.ReadCloser, out chan core.StreamChunk, cfg core.StreamConfig, mode sseLineMode, parse func(payload []byte) ([]core.StreamChunk, error)) {
	defer close(out)
	defer body.Close()
	newSSEPump(ctx, provider, model, out, cfg).run(body, mode, parse)
}

// sseDataPayload extracts the payload from a "data:" line without allocating.
func sseDataPayload(line []byte) ([]byte, bool) {
	line = bytes.TrimRight(line, "\r")
	if !bytes.HasPrefix(line, []byte("data:")) {
		return nil, false
	}
	return bytes.TrimSpace(line[len("data:"):]), true
}

func isSSEKeepAliveBytes(line []byte) bool {
	line = bytes.TrimSpace(line)
	return len(line) > 0 && line[0] == ':'
}
