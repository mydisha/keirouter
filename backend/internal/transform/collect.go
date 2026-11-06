package transform

import (
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// CollectStream folds a stream into one unary response, for providers that
// only stream. Tool calls are joined per stream index like on the streaming
// path: continuation chunks carry no id, so joining by id would split a call.
func CollectStream(stream <-chan core.StreamChunk, model string) (*core.ChatResponse, error) {
	var text, thinking strings.Builder
	var calls []*core.ToolCall
	finish := core.FinishStop
	var usage core.Usage

	tools := NewToolArgSanitizer()
	collect := func(c core.StreamChunk) {
		if c.Type == core.ChunkToolCall && c.ToolCall != nil {
			tc := *c.ToolCall
			calls = append(calls, &tc)
		}
	}
	for ch := range stream {
		switch ch.Type {
		case core.ChunkText:
			text.WriteString(ch.Delta)
		case core.ChunkThinking:
			thinking.WriteString(ch.Delta)
		case core.ChunkToolCall:
			if ch.ToolCall != nil {
				tools.Process(ch, collect)
				finish = core.FinishToolCalls
			}
		case core.ChunkFinish:
			if ch.FinishReason != "" {
				finish = ch.FinishReason
			}
		case core.ChunkUsage:
			if ch.Usage != nil {
				usage = *ch.Usage
			}
		case core.ChunkError:
			if ch.Err != nil {
				return nil, ch.Err
			}
		}
	}
	tools.Flush(collect)
	if len(calls) > 0 && finish == core.FinishStop {
		finish = core.FinishToolCalls
	}

	msg := core.Message{Role: core.RoleAssistant}
	if thinking.Len() > 0 {
		msg.Content = append(msg.Content, core.ContentPart{Type: core.PartThinking, Text: thinking.String()})
	}
	if text.Len() > 0 {
		msg.Content = append(msg.Content, core.ContentPart{Type: core.PartText, Text: text.String()})
	}
	for _, tc := range calls {
		msg.Content = append(msg.Content, core.ContentPart{Type: core.PartToolCall, ToolCall: tc})
	}
	return &core.ChatResponse{Model: model, Message: msg, FinishReason: finish, Usage: usage}, nil
}
