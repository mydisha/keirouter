package core

import (
	"encoding/json"
	"strings"
)

// SystemBlock is one block of a system prompt.
type SystemBlock struct {
	Text         string          `json:"text"`
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
}

// SystemBlocks splits System so each SystemCache block ends where the client
// put its breakpoint. Blocks are found by text, not offset, because hooks
// prepend to System; text around them becomes plain blocks.
func (r *ChatRequest) SystemBlocks() []SystemBlock {
	if strings.TrimSpace(r.System) == "" {
		return nil
	}
	if len(r.SystemCache) == 0 {
		return []SystemBlock{{Text: r.System}}
	}
	out := make([]SystemBlock, 0, 2*len(r.SystemCache)+1)
	rest := r.System
	var unplaced json.RawMessage
	for _, b := range r.SystemCache {
		i := -1
		if b.Text != "" {
			i = strings.Index(rest, b.Text)
		}
		if i < 0 {
			unplaced = b.CacheControl
			continue
		}
		out = appendGap(out, rest[:i])
		out = append(out, SystemBlock{Text: rest[i : i+len(b.Text)], CacheControl: b.CacheControl})
		rest = rest[i+len(b.Text):]
	}
	out = appendGap(out, rest)
	// A hook rewrote every cached block; caching the whole prompt beats none.
	if unplaced != nil && !anyCacheControl(out) {
		out[len(out)-1].CacheControl = unplaced
	}
	return out
}

// appendGap adds the text between cached blocks as a plain block. Newlines
// after a block are the separator a codec added when joining, not content.
func appendGap(out []SystemBlock, gap string) []SystemBlock {
	if len(out) > 0 {
		gap = strings.TrimLeft(gap, "\n")
	}
	if strings.TrimSpace(gap) == "" {
		return out
	}
	return append(out, SystemBlock{Text: gap})
}

func anyCacheControl(blocks []SystemBlock) bool {
	for _, b := range blocks {
		if len(b.CacheControl) > 0 {
			return true
		}
	}
	return false
}
