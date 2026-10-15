package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// Cursor drives Cursor IDE's StreamUnifiedChatWithTools endpoint. Unlike every
// other provider, Cursor speaks a Connect-RPC protobuf transport (not JSON):
// the request body is a hand-encoded protobuf wrapped in a Connect-RPC frame,
// authenticated by the x-cursor-checksum "Jyh cipher" header set, and the
// response is a sequence of Connect-RPC frames carrying protobuf payloads. This
// connector builds the framed body, reads the
// full framed response, and decodes each frame into canonical chunks.
type Cursor struct {
	id          string
	defaultBase string
	chatPath    string
}

// NewCursor builds a Cursor connector.
func NewCursor(id, defaultBaseURL string) *Cursor {
	base := defaultBaseURL
	if base == "" {
		base = "https://api2.cursor.sh"
	}
	return &Cursor{
		id:          id,
		defaultBase: base,
		chatPath:    "/aiserver.v1.ChatService/StreamUnifiedChatWithTools",
	}
}

func (c *Cursor) ID() string            { return c.id }
func (c *Cursor) Dialect() core.Dialect { return core.DialectCursor }

func (c *Cursor) baseURL(creds core.Credentials) string {
	if creds.BaseURL != "" {
		return creds.BaseURL
	}
	return c.defaultBase
}

func (c *Cursor) url(creds core.Credentials) string {
	return strings.TrimRight(c.baseURL(creds), "/") + c.chatPath
}

// Validate confirms a token is present. Cursor speaks a Connect-RPC protobuf
// transport with no cheap probe endpoint, so only token presence can be
// checked without issuing a billable chat request.
func (c *Cursor) Validate(ctx context.Context, creds core.Credentials) error {
	if creds.AccessToken == "" && creds.APIKey == "" {
		return fmt.Errorf("validation failed for %s: no access token", c.id)
	}
	return nil
}

// headers builds the Cursor Connect-RPC + checksum header set. machine_id and
// ghost_mode come from the account's extra metadata; machine_id is derived from
// the token when absent.
func (c *Cursor) headers(creds core.Credentials) map[string]string {
	token := creds.AccessToken
	if token == "" {
		token = creds.APIKey
	}
	machineID := creds.Extra["machine_id"]
	ghost := creds.Extra["ghost_mode"] != "false" // default true
	h := buildCursorHeaders(token, machineID, stainlessOSLower(), cursorArch(), ghost)
	return mergeHeaders(h, creds.Headers)
}

// Chat performs a non-streaming Cursor call: it reads the full framed protobuf
// response and folds it into a single response.
func (c *Cursor) Chat(ctx context.Context, req *core.ChatRequest, creds core.Credentials) (*core.ChatResponse, error) {
	results, err := c.do(ctx, req, creds)
	if err != nil {
		return nil, err
	}

	msg := core.Message{Role: core.RoleAssistant}
	var text, thinking string
	toolCalls := map[string]*core.ToolCall{}
	var toolOrder []string
	finish := core.FinishStop

	for _, r := range results {
		switch {
		case r.toolCall != nil:
			tc, ok := toolCalls[r.toolCall.id]
			if !ok {
				tc = &core.ToolCall{ID: r.toolCall.id, Name: r.toolCall.name, Arguments: json.RawMessage(r.toolCall.args)}
				toolCalls[r.toolCall.id] = tc
				toolOrder = append(toolOrder, r.toolCall.id)
			} else {
				tc.Arguments = append(tc.Arguments, []byte(r.toolCall.args)...)
			}
			finish = core.FinishToolCalls
		case r.text != "":
			text += r.text
		case r.thinking != "":
			thinking += r.thinking
		}
	}

	if thinking != "" {
		msg.Content = append(msg.Content, core.ContentPart{Type: core.PartThinking, Text: thinking})
	}
	if text != "" {
		msg.Content = append(msg.Content, core.ContentPart{Type: core.PartText, Text: text})
	}
	for _, id := range toolOrder {
		tc := toolCalls[id]
		if len(tc.Arguments) == 0 {
			tc.Arguments = json.RawMessage("{}")
		}
		msg.Content = append(msg.Content, core.ContentPart{Type: core.PartToolCall, ToolCall: tc})
	}

	return &core.ChatResponse{Model: req.Model, Message: msg, FinishReason: finish}, nil
}

// Stream performs a Cursor call and emits canonical chunks. Cursor returns the
// whole framed protobuf response (not incremental SSE), so the connector reads
// it fully then replays decoded frames as chunks.
func (c *Cursor) Stream(ctx context.Context, req *core.ChatRequest, creds core.Credentials, cfg core.StreamConfig) (<-chan core.StreamChunk, error) {
	results, err := c.do(ctx, req, creds)
	if err != nil {
		return nil, err
	}

	out := make(chan core.StreamChunk, 16)
	go func() {
		defer close(out)

		ttft := newTTFTTracker(cfg)

		seen := map[string]bool{}
		hadTool := false

		emit := func(ch core.StreamChunk) bool {
			ttft.maybeReport(ch)
			select {
			case out <- ch:
				return true
			case <-ctx.Done():
				return false
			}
		}

		for _, r := range results {
			switch {
			case r.toolCall != nil:
				hadTool = true
				tc := r.toolCall
				if !seen[tc.id] {
					seen[tc.id] = true
					ch := core.StreamChunk{Type: core.ChunkToolCall, ToolCall: &core.ToolCall{ID: tc.id, Name: tc.name, Arguments: json.RawMessage("")}}
					if !emit(ch) {
						return
					}
				}
				if tc.args != "" && tc.args != "{}" {
					ch := core.StreamChunk{Type: core.ChunkToolCall, ToolCall: &core.ToolCall{ID: tc.id, Arguments: json.RawMessage(tc.args)}}
					if !emit(ch) {
						return
					}
				}
			case r.thinking != "":
				ch := core.StreamChunk{Type: core.ChunkThinking, Delta: r.thinking}
				if !emit(ch) {
					return
				}
			case r.text != "":
				ch := core.StreamChunk{Type: core.ChunkText, Delta: r.text}
				if !emit(ch) {
					return
				}
			}
		}

		finish := core.FinishStop
		if hadTool {
			finish = core.FinishToolCalls
		}
		emit(core.StreamChunk{Type: core.ChunkFinish, FinishReason: finish})
	}()
	return out, nil
}

// do performs the HTTP POST and decodes the framed protobuf response into an
// ordered slice of cursorResults.
func (c *Cursor) do(ctx context.Context, req *core.ChatRequest, creds core.Credentials) ([]cursorResult, error) {
	forceAgent := strings.Contains(strings.ToLower(req.Metadata.ClientKind), "claude")
	body := buildCursorBody(req, forceAgent)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(creds), bytes.NewReader(body))
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrInternal, Provider: c.id, Model: req.Model, Message: err.Error(), Cause: err}
	}
	for k, v := range c.headers(creds) {
		httpReq.Header.Set(k, v)
	}

	resp, err := proxyDo(egressCtx(ctx, creds), httpReq)
	if err != nil {
		return nil, transportError(ctx, c.id, req.Model, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &core.ProviderError{Kind: core.ErrUpstream, Provider: c.id, Model: req.Model, Message: "read body: " + err.Error(), Cause: err}
	}
	if resp.StatusCode >= 400 {
		return nil, httpStatusError(c.id, req.Model, resp, raw)
	}

	// Decode all Connect-RPC frames.
	var results []cursorResult
	offset := 0
	for offset < len(raw) {
		payload, consumed, ok := parseConnectRPCFrame(raw[offset:])
		if !ok {
			break
		}
		offset += consumed

		// A JSON error frame begins with '{'.
		if len(payload) > 0 && payload[0] == '{' {
			if cerr := cursorErrorFromJSON(payload); cerr != nil {
				if len(results) == 0 {
					return nil, &core.ProviderError{Kind: cerr.kind, Provider: c.id, Model: req.Model, Message: cerr.message}
				}
				break // already have content; stop on trailing error frame
			}
		}

		res := extractCursorResult(payload)
		if res.text != "" || res.thinking != "" || res.toolCall != nil {
			results = append(results, res)
		}
	}
	return results, nil
}

type cursorErr struct {
	kind    core.ErrorKind
	message string
}

// cursorErrorFromJSON parses a Cursor JSON error frame, returning nil if the
// payload is not actually an error.
func cursorErrorFromJSON(payload []byte) *cursorErr {
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(payload, &e) != nil || e.Error.Message == "" && e.Error.Code == "" {
		return nil
	}
	kind := core.ErrUpstream
	if e.Error.Code == "resource_exhausted" {
		kind = core.ErrRateLimit
	}
	msg := e.Error.Message
	if msg == "" {
		msg = e.Error.Code
	}
	return &cursorErr{kind: kind, message: msg}
}

func stainlessOSLower() string {
	switch stainlessOS() {
	case "MacOS":
		return "macos"
	case "Windows":
		return "windows"
	default:
		return "linux"
	}
}

func cursorArch() string {
	if stainlessArch() == "arm64" {
		return "aarch64"
	}
	return "x64"
}
