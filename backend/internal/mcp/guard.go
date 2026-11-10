package mcp

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/bytedance/sonic"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/fastjson"
	"github.com/mydisha/keirouter/backend/internal/guardrails"
)

// Guard is the guardrail engine as the MCP gateway uses it.
// *guardrails.Engine satisfies it.
type Guard interface {
	EffectivePolicy(ctx context.Context, k guardrails.Key) guardrails.Policy
	Inbound(ctx context.Context, req *core.ChatRequest) guardrails.Result
}

// GuardProvider is the provider name tool calls carry in guardrail policies
// and logs. The model is the prefixed tool name, slug__tool.
const GuardProvider = "mcp"

// guardCall applies guardrail policies to one tool call. Arguments are
// checked as a tool call the model made, results as a tool result fed back
// to the model, so the existing policies apply with their usual meaning:
// PII scans both (masking rewrites them), injection with scan_tool_results
// catches indirect injection in results. Detectors that only read user text
// (toxicity, topics) have nothing to read.
type guardCall struct {
	g    Guard
	ctx  context.Context
	meta core.RequestMetadata
	tool string // the upstream tool name
	name string // slug__tool
}

// guardVerdict is what a check decided.
type guardVerdict struct {
	blocked  bool
	masked   bool
	detector string
	reason   string
	part     string // "arguments" or "result"
}

// label is the short form for logs and the console, such as
// "pii masked arguments".
func (v guardVerdict) label() string {
	switch {
	case v.blocked:
		return v.detector + " blocked " + v.part
	case v.masked:
		return v.detector + " masked " + v.part
	}
	return ""
}

// guardFor returns the checker for a call, or nil when no guardrail policy
// can act on it, so calls without one pay only for the cached policy lookup.
func (m *Manager) guardFor(ctx context.Context, caller Caller, srv *Server, tool *Tool) *guardCall {
	if m.guard == nil {
		return nil
	}
	name := srv.Slug + ToolNameSeparator + tool.Name
	key := guardrails.Key{TenantID: m.tenant, Provider: GuardProvider, Model: name, APIKeyID: caller.KeyID}
	if !actsOnTools(m.guard.EffectivePolicy(ctx, key)) {
		return nil
	}
	return &guardCall{
		g:    m.guard,
		ctx:  guardrails.ScanOnly(ctx),
		meta: core.RequestMetadata{TenantID: m.tenant, RequestID: caller.RequestID, APIKeyID: caller.KeyID, Provider: GuardProvider},
		tool: tool.Name,
		name: name,
	}
}

// actsOnTools reports whether a policy enables a detector that reads tool
// arguments or results. An empty policy counts as active in the engine, but
// with no detector enabled there is nothing to check.
func actsOnTools(p guardrails.Policy) bool {
	if !p.IsActive() {
		return false
	}
	return (p.PII != nil && p.PII.Enabled) || (p.Injection != nil && p.Injection.Enabled && p.Injection.ScanToolResults)
}

func (c *guardCall) run(msg core.Message) guardrails.Result {
	req := &core.ChatRequest{Model: c.name, Messages: []core.Message{msg}, Metadata: c.meta}
	return c.g.Inbound(c.ctx, req)
}

func verdictOf(res guardrails.Result) guardVerdict {
	v := guardVerdict{reason: res.Reason}
	for _, d := range res.Decisions {
		switch {
		case d.Action == guardrails.ActionBlock:
			v.blocked, v.detector, v.reason = true, d.Detector, d.Reason
			return v
		case d.Action == guardrails.ActionMask && !v.masked:
			v.masked, v.detector = true, d.Detector
		}
	}
	return v
}

// checkArgs checks the arguments of a tools/call params object and returns
// the params to send upstream, rewritten when a detector masked them.
func (c *guardCall) checkArgs(params json.RawMessage) (json.RawMessage, guardVerdict) {
	out, v := c.argsVerdict(params)
	v.part = "arguments"
	return out, v
}

func (c *guardCall) argsVerdict(params json.RawMessage) (json.RawMessage, guardVerdict) {
	node, err := sonic.Get(params, "arguments")
	if err != nil {
		return params, guardVerdict{}
	}
	raw, err := node.Raw()
	if err != nil || raw == "" || raw == "null" || raw == "{}" {
		return params, guardVerdict{}
	}
	tc := &core.ToolCall{ID: "mcp", Name: c.tool, Arguments: json.RawMessage(raw)}
	res := c.run(core.Message{Role: core.RoleAssistant, Content: []core.ContentPart{{Type: core.PartToolCall, ToolCall: tc}}})
	v := verdictOf(res)
	if v.blocked || res.Action != guardrails.ActionMask || string(tc.Arguments) == raw {
		v.masked = v.masked && !v.blocked && string(tc.Arguments) != raw
		return params, v
	}
	if !json.Valid(tc.Arguments) {
		return params, guardVerdict{blocked: true, detector: v.detector, reason: "masking left the arguments invalid JSON"}
	}
	out, err := setField(params, "arguments", tc.Arguments)
	if err != nil {
		return params, guardVerdict{blocked: true, detector: v.detector, reason: "could not rewrite the masked arguments"}
	}
	return out, v
}

// resultText is one text field of a tool result that guardrails read.
type resultText struct {
	item       int  // index into content, or -1 for structuredContent
	inResource bool // content[item].resource.text rather than content[item].text
}

type contentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Resource *struct {
		Text string `json:"text"`
	} `json:"resource"`
}

// checkResult checks a tool result and returns the result to send to the
// client, rewritten when a detector masked it.
func (c *guardCall) checkResult(result json.RawMessage) (json.RawMessage, guardVerdict) {
	out, v := c.resultVerdict(result)
	v.part = "result"
	return out, v
}

func (c *guardCall) resultVerdict(result json.RawMessage) (json.RawMessage, guardVerdict) {
	var r struct {
		Content    []contentItem   `json:"content"`
		Structured json.RawMessage `json:"structuredContent"`
	}
	if json.Unmarshal(result, &r) != nil {
		return result, guardVerdict{}
	}
	var parts []core.ContentPart
	var refs []resultText
	for i, it := range r.Content {
		switch {
		case it.Type == "text" && it.Text != "":
			parts = append(parts, core.ContentPart{Type: core.PartText, Text: it.Text})
			refs = append(refs, resultText{item: i})
		case it.Type == "resource" && it.Resource != nil && it.Resource.Text != "":
			parts = append(parts, core.ContentPart{Type: core.PartText, Text: it.Resource.Text})
			refs = append(refs, resultText{item: i, inResource: true})
		}
	}
	if s := string(r.Structured); s != "" && s != "null" {
		parts = append(parts, core.ContentPart{Type: core.PartText, Text: s})
		refs = append(refs, resultText{item: -1})
	}
	if len(parts) == 0 {
		return result, guardVerdict{}
	}
	before := make([]string, len(parts))
	for i := range parts {
		before[i] = parts[i].Text
	}
	tr := &core.ToolResult{CallID: "mcp", Parts: parts}
	res := c.run(core.Message{Role: core.RoleTool, Content: []core.ContentPart{{Type: core.PartToolResult, ToolResult: tr}}})
	v := verdictOf(res)
	if v.blocked || res.Action != guardrails.ActionMask {
		v.masked = false
		return result, v
	}
	out, changed, err := rewriteResult(result, refs, before, tr.Parts)
	if err != nil {
		return result, guardVerdict{blocked: true, detector: v.detector, reason: err.Error()}
	}
	v.masked = changed
	return out, v
}

// rewriteResult writes masked texts back into a tool result. Only the fields
// that changed are touched; everything else is copied as received. Encoding
// skips HTML escaping so untouched text keeps its bytes.
func rewriteResult(result json.RawMessage, refs []resultText, before []string, after []core.ContentPart) (json.RawMessage, bool, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(result, &top); err != nil {
		return nil, false, err
	}
	var items []map[string]json.RawMessage
	if raw, ok := top["content"]; ok {
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, false, err
		}
	}
	changed := false
	for i, ref := range refs {
		text := after[i].Text
		if text == before[i] {
			continue
		}
		changed = true
		if ref.item < 0 {
			if !json.Valid([]byte(text)) {
				return nil, false, errors.New("masking left structuredContent invalid JSON")
			}
			top["structuredContent"] = json.RawMessage(text)
			continue
		}
		enc, _ := fastjson.Marshal(text)
		if !ref.inResource {
			items[ref.item]["text"] = enc
			continue
		}
		var res map[string]json.RawMessage
		if err := json.Unmarshal(items[ref.item]["resource"], &res); err != nil {
			return nil, false, err
		}
		res["text"] = enc
		items[ref.item]["resource"], _ = fastjson.Marshal(res)
	}
	if !changed {
		return result, false, nil
	}
	if items != nil {
		top["content"], _ = fastjson.Marshal(items)
	}
	out, err := fastjson.Marshal(top)
	return out, true, err
}

// setField returns obj with one top-level field replaced.
func setField(obj json.RawMessage, key string, value json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(obj, &fields); err != nil {
		return nil, err
	}
	fields[key] = value
	return fastjson.Marshal(fields)
}
