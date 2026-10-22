package gateway

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// earlyCommit sends the stream's 200 headers and SSE heartbeats while the
// pipeline is still looking for a first response: retries across accounts,
// fallbacks, or an upstream that holds the request before answering. Without
// it the client sees no byte at all until the first chunk, and Cloudflare
// drops the request with a 524 after 100 seconds while KeiRouter still
// finishes and logs it as a success.
//
// The pipeline goroutine never touches the ResponseWriter, so the timer
// goroutine owns it until finish returns.
type earlyCommit struct {
	mu        sync.Mutex
	timer     *time.Timer
	w         http.ResponseWriter
	flush     func()
	interval  time.Duration
	committed bool
	finished  bool
	hw        *heartbeatWriter
}

// startEarlyCommit arms the commit after delay. A non-positive delay or
// interval returns nil, which finish treats as never committed.
func startEarlyCommit(w http.ResponseWriter, flush func(), delay, interval time.Duration) *earlyCommit {
	if delay <= 0 || interval <= 0 {
		return nil
	}
	ec := &earlyCommit{w: w, flush: flush, interval: interval}
	ec.timer = time.AfterFunc(delay, ec.commit)
	return ec
}

func (ec *earlyCommit) commit() {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	if ec.finished {
		return
	}
	setStreamHeaders(ec.w, "text/event-stream")
	ec.w.WriteHeader(http.StatusOK)
	_, _ = ec.w.Write(sseHeartbeatFrame)
	ec.flush()
	ec.committed = true
	ec.hw = newHeartbeatWriter(ec.w, ec.flush, ec.interval)
}

// finish stops the timer and any heartbeats, and reports whether headers were
// already sent. After it returns the caller owns the ResponseWriter again.
func (ec *earlyCommit) finish() bool {
	if ec == nil {
		return false
	}
	ec.timer.Stop()
	ec.mu.Lock()
	ec.finished = true
	committed, hw := ec.committed, ec.hw
	ec.mu.Unlock()
	if hw != nil {
		hw.stop()
	}
	return committed
}

func setStreamHeaders(w http.ResponseWriter, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
}

// committedStreamErrorEvent renders a request failure as an SSE event for a
// stream whose 200 headers are already sent. It carries what the HTTP status
// would have: the Anthropic error type and the Gemini status follow
// providerErrorStatus, and the OpenAI code is the error kind (for example
// model_out_of_stock) so clients can tell a retryable failure apart.
func committedStreamErrorEvent(dialect core.Dialect, pe *core.ProviderError) []byte {
	status := providerErrorStatus(pe)
	message := sanitizeUpstreamError(pe)
	var payload map[string]any
	switch dialect {
	case core.DialectAnthropic:
		payload = map[string]any{
			"type":  "error",
			"error": map[string]any{"type": anthropicErrorType(status), "message": message},
		}
	case core.DialectGemini:
		payload = map[string]any{
			"error": map[string]any{"code": status, "message": message, "status": googleStatusName(status)},
		}
	default:
		code := string(pe.Kind)
		if code == "" {
			code = "upstream_error"
		}
		payload = map[string]any{
			"error": map[string]any{"message": message, "type": "upstream_error", "code": code},
		}
	}
	body, _ := json.Marshal(payload)
	prefix := "data: "
	if dialect == core.DialectAnthropic {
		prefix = "event: error\ndata: "
	}
	out := make([]byte, 0, len(prefix)+len(body)+2)
	out = append(out, prefix...)
	out = append(out, body...)
	return append(out, '\n', '\n')
}
