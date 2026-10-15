// Package prettylog provides a TTY-aware slog handler and startup banner for
// KeiRouter's CLI output. When stdout is a terminal it renders colorized,
// compact log lines; when piped or redirected it falls back to plain text so
// CI logs and log aggregators stay parseable.
package prettylog

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"unicode/utf8"
)

// ANSI escape codes.
const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiCyan   = "\033[36m"
	ansiGray   = "\033[90m"
)

// Options configures the handler.
type Options struct {
	Level slog.Level
}

// Handler is a slog.Handler that renders colorized, compact output when
// writing to a terminal, and falls back to plain text otherwise.
type Handler struct {
	w      io.Writer
	level  slog.Level
	pretty bool
	mu     *sync.Mutex
	attrs  []slog.Attr
	groups []string
}

// NewHandler creates a handler. If w's file descriptor is a terminal, pretty
// mode is enabled; otherwise it falls back to plain text.
func NewHandler(w io.Writer, opts *Options) *Handler {
	level := slog.LevelInfo
	if opts != nil {
		level = opts.Level
	}

	pretty := false
	if f, ok := w.(interface{ Fd() uintptr }); ok {
		pretty = IsTerminal(f.Fd())
	}

	return &Handler{
		w:      w,
		level:  level,
		pretty: pretty,
		mu:     &sync.Mutex{},
	}
}

func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	var sb strings.Builder
	sb.Grow(256)

	if h.pretty {
		h.renderPretty(&sb, r)
	} else {
		h.renderPlain(&sb, r)
	}

	h.mu.Lock()
	_, err := io.WriteString(h.w, sb.String())
	h.mu.Unlock()
	return err
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(clone.attrs[:len(clone.attrs):len(clone.attrs)], attrs...)
	return &clone
}

func (h *Handler) WithGroup(name string) slog.Handler {
	clone := *h
	clone.groups = append(clone.groups[:len(clone.groups):len(clone.groups)], name)
	return &clone
}

// renderPretty produces: "15:04:05 ● message    key=value key=value\n"
func (h *Handler) renderPretty(sb *strings.Builder, r slog.Record) {
	ts := r.Time.Format("15:04:05")
	sb.WriteString(ansiGray)
	sb.WriteString(ts)
	sb.WriteString(ansiReset)
	sb.WriteByte(' ')

	icon, color := levelStyle(r.Level)
	sb.WriteString(color)
	sb.WriteString(icon)
	sb.WriteString(ansiReset)
	sb.WriteByte(' ')

	sb.WriteString(r.Message)

	// Errors go on their own continuation line: their wrapped chains are long
	// and would otherwise spill the attribute column across several rows.
	var pairs, errs []string
	collect := func(a slog.Attr) {
		if isErrorKey(a.Key) {
			errs = append(errs, a.Value.String())
			return
		}
		pairs = append(pairs, formatAttr(a, true))
	}
	for _, a := range h.attrs {
		collect(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		collect(a)
		return true
	})

	if len(pairs) > 0 {
		// Pad message to align attributes.
		pad := 36 - utf8.RuneCountInString(r.Message)
		if pad < 2 {
			pad = 2
		}
		sb.WriteString(strings.Repeat(" ", pad))
		sb.WriteString(ansiGray)
		sb.WriteString(strings.Join(pairs, " "))
		sb.WriteString(ansiReset)
	}
	sb.WriteByte('\n')

	errColor := ansiGray
	if r.Level >= slog.LevelWarn {
		_, errColor = levelStyle(r.Level)
	}
	for _, e := range errs {
		// Indent under the message (after "15:04:05 ● ").
		sb.WriteString("           ")
		sb.WriteString(ansiDim)
		sb.WriteString("└─ ")
		sb.WriteString(ansiReset)
		sb.WriteString(errColor)
		sb.WriteString(strings.ReplaceAll(e, "\n", "\n              "))
		sb.WriteString(ansiReset)
		sb.WriteByte('\n')
	}
}

func isErrorKey(k string) bool {
	return k == "err" || k == "error"
}

// renderPlain produces: "time=15:04:05 level=INFO msg=\"message\" key=value\n"
func (h *Handler) renderPlain(sb *strings.Builder, r slog.Record) {
	sb.WriteString("time=")
	sb.WriteString(r.Time.Format("15:04:05"))
	sb.WriteString(" level=")
	sb.WriteString(levelString(r.Level))
	sb.WriteString(" msg=")
	sb.WriteString(quoteIfNeeded(r.Message))

	for _, a := range h.attrs {
		sb.WriteByte(' ')
		sb.WriteString(formatAttr(a, false))
	}
	r.Attrs(func(a slog.Attr) bool {
		sb.WriteByte(' ')
		sb.WriteString(formatAttr(a, false))
		return true
	})
	sb.WriteByte('\n')
}

func levelStyle(l slog.Level) (icon, color string) {
	switch {
	case l >= slog.LevelError:
		return "✖", ansiBold + ansiRed
	case l >= slog.LevelWarn:
		return "▲", ansiYellow
	case l >= slog.LevelInfo:
		return "●", ansiGreen
	default:
		return "◆", ansiGray
	}
}

func levelString(l slog.Level) string {
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

func formatAttr(a slog.Attr, pretty bool) string {
	if pretty {
		return fmt.Sprintf("%s=%s", a.Key, a.Value.String())
	}
	return fmt.Sprintf("%s=%s", a.Key, quoteIfNeeded(a.Value.String()))
}

func quoteIfNeeded(s string) string {
	if strings.ContainsAny(s, " \"\t\n=") {
		return fmt.Sprintf("%q", s)
	}
	return s
}
