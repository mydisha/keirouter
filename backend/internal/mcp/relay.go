package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/bytedance/sonic"
)

// progressRelay forwards the progress notifications an upstream streams for
// one tool call. The response switches to an SSE stream on the first one;
// a call that sends none is answered with plain JSON as before. The relay
// only exists when the client sent a progress token and accepts SSE.
type progressRelay struct {
	w       http.ResponseWriter
	token   []byte
	started bool
	sent    int
	buf     bytes.Buffer
}

func newProgressRelay(x *exchange) *progressRelay {
	n, err := sonic.Get(x.req.Params, "_meta", "progressToken")
	if err != nil {
		return nil
	}
	raw, err := n.Raw()
	if err != nil || raw == "" || raw == "null" || !acceptsSSE(x.r.Header.Values("Accept")) {
		return nil
	}
	return &progressRelay{w: x.w, token: []byte(raw)}
}

// acceptsSSE reports whether an Accept header lists text/event-stream.
func acceptsSSE(values []string) bool {
	for _, v := range values {
		for v != "" {
			var part string
			part, v, _ = strings.Cut(v, ",")
			mt, _, _ := strings.Cut(part, ";")
			if strings.EqualFold(strings.TrimSpace(mt), "text/event-stream") {
				return true
			}
		}
	}
	return false
}

// notify relays one upstream message when it is a progress notification for
// the client's token. Anything else (log messages, server requests the
// gateway cannot route back) is dropped.
func (p *progressRelay) notify(msg []byte) {
	var n struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			ProgressToken json.RawMessage `json:"progressToken"`
		} `json:"params"`
	}
	if json.Unmarshal(msg, &n) != nil || len(n.ID) > 0 || n.Method != "notifications/progress" || !sameID(n.Params.ProgressToken, p.token) {
		return
	}
	if !p.started {
		h := p.w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Accel-Buffering", "no")
		p.w.WriteHeader(http.StatusOK)
		p.started = true
	}
	p.event(msg)
	p.sent++
}

// event writes one SSE message event and flushes it. JSON with line breaks
// is compacted first, since a raw newline would end the data field.
func (p *progressRelay) event(data []byte) {
	if bytes.ContainsAny(data, "\r\n") {
		p.buf.Reset()
		if json.Compact(&p.buf, data) == nil {
			data = p.buf.Bytes()
		}
	}
	_, _ = p.w.Write([]byte("event: message\ndata: "))
	_, _ = p.w.Write(data)
	_, _ = p.w.Write([]byte("\n\n"))
	_ = http.NewResponseController(p.w).Flush()
}

// reply sends the final JSON-RPC message: on the stream when it started,
// otherwise as a plain JSON response. A nil relay always answers with JSON.
func (p *progressRelay) reply(w http.ResponseWriter, msg []byte) {
	if p == nil || !p.started {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(msg)
		return
	}
	p.event(msg)
}
