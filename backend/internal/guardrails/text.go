package guardrails

import (
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// messageText returns the concatenated plain-text content of a message. It
// reuses core.Message.TextContent for the read side and pairs with
// setMessageText for the write side.
func messageText(m core.Message) string {
	return m.TextContent()
}

// setMessageText overwrites the text content of a message with the rewritten
// value. Non-text parts (images, tool calls, thinking) are preserved.
//
// When the message has at least one PartText part, only the FIRST PartText
// is replaced and the others are dropped — this matches the dominant case of
// a single text part per message and keeps downstream serialization simple.
// When the message has no PartText, we prepend one.
func setMessageText(m *core.Message, text string) {
	if m == nil {
		return
	}
	wrote := false
	out := m.Content[:0]
	for _, p := range m.Content {
		if p.Type == core.PartText {
			if !wrote {
				p.Text = text
				out = append(out, p)
				wrote = true
			}
			continue
		}
		out = append(out, p)
	}
	if !wrote {
		out = append([]core.ContentPart{{Type: core.PartText, Text: text}}, out...)
	}
	m.Content = out
}

// responseText returns the plain-text content of a chat response.
func responseText(r *core.ChatResponse) string {
	if r == nil {
		return ""
	}
	return r.Message.TextContent()
}

// setResponseText overwrites the response's text content with the masked
// value. Mirrors setMessageText semantics.
func setResponseText(r *core.ChatResponse, text string) {
	if r == nil {
		return
	}
	setMessageText(&r.Message, text)
}

// SurfaceKind names the kind of field a TextSurface reads and writes.
type SurfaceKind string

const (
	SurfaceSystem     SurfaceKind = "system"
	SurfaceText       SurfaceKind = "text"
	SurfaceToolResult SurfaceKind = "tool_result"
	SurfaceToolArgs   SurfaceKind = "tool_args"
)

// TextSurface is one rewritable text field of a request or response. Thinking
// blocks are never exposed: providers sign them and a rewrite would break the
// follow-up turn.
type TextSurface struct {
	Kind SurfaceKind
	Role core.Role
	// Message is the index into ChatRequest.Messages, or -1 for the system
	// prompt and for response surfaces.
	Message int
	Get     func() string
	Set     func(string)
}

// RequestSurfaces lists every text field of a request in conversation order:
// the hoisted system prompt, then each message's text parts, tool results
// and tool call arguments.
func RequestSurfaces(req *core.ChatRequest) []TextSurface {
	if req == nil {
		return nil
	}
	out := make([]TextSurface, 0, len(req.Messages)+1)
	if req.System != "" {
		out = append(out, TextSurface{
			Kind: SurfaceSystem, Role: core.RoleSystem, Message: -1,
			Get: func() string { return req.System },
			Set: func(s string) { req.System = s },
		})
	}
	for i := range req.Messages {
		out = appendPartSurfaces(out, req.Messages[i].Role, i, req.Messages[i].Content)
	}
	return out
}

// ResponseSurfaces lists the text parts and tool call arguments of a response.
func ResponseSurfaces(resp *core.ChatResponse) []TextSurface {
	if resp == nil {
		return nil
	}
	return appendPartSurfaces(nil, core.RoleAssistant, -1, resp.Message.Content)
}

func appendPartSurfaces(out []TextSurface, role core.Role, msg int, parts []core.ContentPart) []TextSurface {
	for j := range parts {
		p := &parts[j]
		switch p.Type {
		case core.PartText:
			out = append(out, TextSurface{
				Kind: SurfaceText, Role: role, Message: msg,
				Get: func() string { return p.Text },
				Set: func(s string) { p.Text = s },
			})
		case core.PartToolResult:
			if p.ToolResult == nil {
				continue
			}
			tr := p.ToolResult
			out = append(out, TextSurface{
				Kind: SurfaceToolResult, Role: role, Message: msg,
				Get: func() string { return tr.Content },
				Set: func(s string) { tr.Content = s },
			})
			for k := range tr.Parts {
				if tr.Parts[k].Type != core.PartText {
					continue
				}
				q := &tr.Parts[k]
				out = append(out, TextSurface{
					Kind: SurfaceToolResult, Role: role, Message: msg,
					Get: func() string { return q.Text },
					Set: func(s string) { q.Text = s },
				})
			}
		case core.PartToolCall:
			if p.ToolCall == nil {
				continue
			}
			tc := p.ToolCall
			out = append(out, TextSurface{
				Kind: SurfaceToolArgs, Role: role, Message: msg,
				Get: func() string { return string(tc.Arguments) },
				Set: func(s string) { tc.Arguments = []byte(s) },
			})
		}
	}
	return out
}

// inboundViews caches the role-scoped text views of a request.
type inboundViews struct {
	user        string
	recent      string
	tools       string
	recentTools string
	lastUser    string
}

// recentUserTurns is how many trailing user messages RecentUserText covers.
// Follow-ups like "tell me more" carry no topic of their own, so allow-list
// checks need a little conversation context.
const recentUserTurns = 3

func buildViews(req *core.ChatRequest) *inboundViews {
	// The current turn starts after the last user message that carries text;
	// tool results after it are what the model is about to act on.
	turnStart := 0
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == core.RoleUser && strings.TrimSpace(req.Messages[i].TextContent()) != "" {
			turnStart = i
			break
		}
	}
	var user, tools, recentTools []string
	for i, m := range req.Messages {
		addTool := func(t string) {
			tools = append(tools, t)
			if i >= turnStart {
				recentTools = append(recentTools, t)
			}
		}
		for _, p := range m.Content {
			switch p.Type {
			case core.PartText:
				switch m.Role {
				case core.RoleUser:
					user = append(user, p.Text)
				case core.RoleTool:
					addTool(p.Text)
				}
			case core.PartToolResult:
				if p.ToolResult == nil {
					continue
				}
				addTool(p.ToolResult.Content)
				for _, q := range p.ToolResult.Parts {
					if q.Type == core.PartText {
						addTool(q.Text)
					}
				}
			}
		}
	}
	var recent []string
	for i := len(req.Messages) - 1; i >= 0 && len(recent) < recentUserTurns; i-- {
		if req.Messages[i].Role != core.RoleUser {
			continue
		}
		if t := req.Messages[i].TextContent(); strings.TrimSpace(t) != "" {
			recent = append([]string{t}, recent...)
		}
	}
	v := &inboundViews{
		user:        strings.Join(user, "\n\n"),
		recent:      strings.Join(recent, "\n\n"),
		tools:       strings.Join(tools, "\n\n"),
		recentTools: strings.Join(recentTools, "\n\n"),
	}
	if len(recent) > 0 {
		v.lastUser = recent[len(recent)-1]
	}
	return v
}
