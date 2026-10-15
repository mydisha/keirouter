package consolelog

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// teeHandler forwards records at or above a minimum level to a console Buffer
// while passing every record through to the wrapped handler unchanged. It lets
// background components (OAuth keepalive, health checks, tunnels) surface their
// warnings on the dashboard console without each one knowing about it.
type teeHandler struct {
	next   slog.Handler
	buf    *Buffer
	min    slog.Level
	source string
	attrs  []slog.Attr
	groups string
}

// Tee returns a logger that writes to base and also mirrors records at level
// min or above into buf, tagged with source (e.g. "oauth", "health"). A nil
// buf returns base unchanged.
func Tee(base *slog.Logger, buf *Buffer, min slog.Level, source string) *slog.Logger {
	if buf == nil || base == nil {
		return base
	}
	return slog.New(&teeHandler{next: base.Handler(), buf: buf, min: min, source: source})
}

func (h *teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.min || h.next.Enabled(ctx, level)
}

func (h *teeHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= h.min {
		h.buf.Add(h.entry(r))
	}
	if h.next.Enabled(ctx, r.Level) {
		return h.next.Handle(ctx, r)
	}
	return nil
}

func (h *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.next = h.next.WithAttrs(attrs)
	c.attrs = append(append([]slog.Attr(nil), h.attrs...), h.qualify(attrs)...)
	return &c
}

func (h *teeHandler) WithGroup(name string) slog.Handler {
	c := *h
	c.next = h.next.WithGroup(name)
	if name != "" {
		c.groups = h.groups + name + "."
	}
	return &c
}

func (h *teeHandler) qualify(attrs []slog.Attr) []slog.Attr {
	if h.groups == "" {
		return attrs
	}
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = slog.Attr{Key: h.groups + a.Key, Value: a.Value}
	}
	return out
}

func (h *teeHandler) entry(r slog.Record) Entry {
	e := Entry{Level: levelName(r.Level), Kind: h.source, Message: r.Message}
	var detail strings.Builder
	add := func(a slog.Attr) {
		v := a.Value.Resolve()
		val := v.String()
		switch a.Key {
		case "provider":
			e.Provider = val
		case "model":
			e.Model = val
		}
		if detail.Len() > 0 {
			detail.WriteByte('\n')
		}
		fmt.Fprintf(&detail, "%s: %s", a.Key, val)
	}
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		add(slog.Attr{Key: h.groups + a.Key, Value: a.Value})
		return true
	})
	e.Detail = detail.String()
	return e
}

func levelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "ERROR"
	case l >= slog.LevelWarn:
		return "WARN"
	case l >= slog.LevelInfo:
		return "INFO"
	default:
		return "DEBUG"
	}
}
