// Package consolelog provides a thread-safe log buffer that captures request
// pipeline output and streams it to dashboard clients via SSE.
//
// Each record is a structured Entry carrying a short, human-readable message,
// an optional Detail block, and typed context fields (request ID, API key,
// model, provider, status, latency). The fields let the dashboard group the
// lines of one request together and filter on them without parsing message
// text, while the message stays readable on its own.
package consolelog

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// MaxEntries bounds the ring buffer. Requests emit two to four lines each, so
// this keeps the last few hundred requests plus system events.
const MaxEntries = 1000

// Kinds group entries by where they come from. KindRequest lines belong to an
// inbound API request and carry its request ID; everything else is a
// background component (oauth, health, tunnel, ...) or admin action.
const (
	KindRequest = "request"
	KindAdmin   = "admin"
)

// Entry is a single structured console log record.
type Entry struct {
	// Seq is a monotonic sequence number, unique per buffer epoch. The
	// dashboard uses it as a stable key and to detect dropped lines.
	Seq uint64 `json:"seq"`
	// Time is the formatted wall-clock time (HH:MM:SS.mmm).
	Time string `json:"time"`
	// TS is the Unix time in milliseconds.
	TS int64 `json:"ts"`
	// Level is one of DEBUG, INFO, WARN, ERROR, LOG.
	Level string `json:"level"`
	// Message is the short, human-readable summary shown inline.
	Message string `json:"msg"`
	// Detail is optional technical context revealed when the row is expanded
	// (raw error text, fallback chains, etc.).
	Detail string `json:"detail,omitempty"`
	// Hint is a plain-language explanation of what the line means for the
	// operator, e.g. why a request failed and what to check.
	Hint string `json:"hint,omitempty"`

	Kind     string `json:"kind,omitempty"`
	ReqID    string `json:"req,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Client   string `json:"client,omitempty"`
	Key      string `json:"key,omitempty"`
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
	// Target is the provider/model that actually served the request, when it
	// differs from what the client asked for.
	Target     string `json:"target,omitempty"`
	Status     int    `json:"status,omitempty"`
	LatencyMs  int    `json:"ms,omitempty"`
	Tokens     int    `json:"tokens,omitempty"`
	CostMicros int64  `json:"cost_micros,omitempty"`
	Fallbacks  int    `json:"fallbacks,omitempty"`
	Stream     bool   `json:"stream,omitempty"`
	CacheHit   bool   `json:"cache,omitempty"`
	// Final marks the terminal summary line of a request (success or
	// failure); the dashboard treats it as the request's headline.
	Final bool `json:"final,omitempty"`
}

// Event represents a log update. If Clear is true, the client should clear its
// buffer; otherwise Entry holds the appended record.
type Event struct {
	Entry Entry
	Clear bool
}

// Listener receives log events via a buffered channel. The channel-based
// approach ensures that slow listeners never block the publisher; a listener
// that falls behind loses events, which the client detects as a Seq gap.
type Listener struct {
	C chan Event
}

// NewListener creates a listener with a buffered event channel.
func NewListener(bufSize int) *Listener {
	if bufSize <= 0 {
		bufSize = 1024 // Log streams can be chatty
	}
	return &Listener{C: make(chan Event, bufSize)}
}

// Buffer is a ring buffer of log entries with pub/sub for SSE streaming.
type Buffer struct {
	mu        sync.RWMutex
	entries   []Entry
	head      int // index of the oldest entry once the ring is full
	listeners map[*Listener]struct{}
	seq       uint64 // guarded by mu; monotonic entry counter
	epoch     string
	now       func() time.Time
}

// New creates an empty log buffer.
func New() *Buffer {
	var raw [6]byte
	_, _ = rand.Read(raw[:])
	return &Buffer{
		entries:   make([]Entry, 0, 128),
		listeners: make(map[*Listener]struct{}),
		epoch:     hex.EncodeToString(raw[:]),
		now:       time.Now,
	}
}

// Epoch identifies this buffer's lifetime. It changes when the process
// restarts, telling a reconnecting client that sequence numbers started over.
func (b *Buffer) Epoch() string {
	if b == nil {
		return ""
	}
	return b.epoch
}

// Entries returns a snapshot of all buffered log entries, oldest first.
func (b *Buffer) Entries() []Entry {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]Entry, 0, len(b.entries))
	out = append(out, b.entries[b.head:]...)
	out = append(out, b.entries[:b.head]...)
	return out
}

// Add stamps the entry with a sequence number and time, appends it to the
// ring and notifies all listeners. Seq, Time and TS on the input are ignored.
func (b *Buffer) Add(e Entry) {
	if b == nil {
		return
	}
	now := b.now()
	e.Time = now.Format("15:04:05.000")
	e.TS = now.UnixMilli()
	if e.Level == "" {
		e.Level = "LOG"
	}

	b.mu.Lock()
	b.seq++
	e.Seq = b.seq
	if len(b.entries) < MaxEntries {
		b.entries = append(b.entries, e)
	} else {
		// Overwrite the oldest slot in place: no copying, no reallocation.
		b.entries[b.head] = e
		b.head = (b.head + 1) % MaxEntries
	}
	// Publish under the lock so listeners see events in Seq order.
	for l := range b.listeners {
		select {
		case l.C <- Event{Entry: e}:
		default:
			// Drop event for slow listener — non-blocking.
		}
	}
	b.mu.Unlock()
}

// Clear resets the buffer and notifies all listeners.
func (b *Buffer) Clear() {
	if b == nil {
		return
	}
	b.mu.Lock()
	clear(b.entries)
	b.entries = b.entries[:0]
	b.head = 0
	for l := range b.listeners {
		select {
		case l.C <- Event{Clear: true}:
		default:
		}
	}
	b.mu.Unlock()
}

// Subscribe registers a listener and returns the buffered history as of the
// moment of subscription, so no line falls between the snapshot and the live
// feed. Call Unsubscribe when done.
func (b *Buffer) Subscribe(l *Listener) []Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.listeners[l] = struct{}{}
	out := make([]Entry, 0, len(b.entries))
	out = append(out, b.entries[b.head:]...)
	out = append(out, b.entries[:b.head]...)
	return out
}

// Unsubscribe removes a listener.
func (b *Buffer) Unsubscribe(l *Listener) {
	b.mu.Lock()
	delete(b.listeners, l)
	b.mu.Unlock()
}

// Log appends a human-readable message at the given level with an optional
// detail block. Pass an empty detail when there is nothing extra to surface.
// Levels: LOG, INFO, WARN, ERROR, DEBUG.
func (b *Buffer) Log(level, message, detail string) {
	b.Add(Entry{Level: level, Message: message, Detail: detail})
}

// Logf appends a formatted, message-only log line (no detail block). Retained
// for call sites that only need a one-liner.
func (b *Buffer) Logf(level, format string, args ...any) {
	b.Add(Entry{Level: level, Message: fmt.Sprintf(format, args...)})
}
