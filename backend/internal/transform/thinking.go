package transform

import (
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// thinkStart and thinkEnd are the XML tags some models (MiMo, QwQ, etc.)
// embed in the content field to demarcate reasoning/thinking blocks.
const (
	thinkStart = "<think>"
	thinkEnd   = "</think>"
)

// StripThinkTags extracts content enclosed in <think> tags from raw text.
// It returns any thinking content found (as a ChunkThinking Delta if non-empty)
// and the remaining text with the tags removed. For non-streaming use where the
// full content is available at once.
func StripThinkTags(content string) (thinkingChunks []core.StreamChunk, cleanContent string) {
	if !strings.Contains(content, thinkStart) {
		return nil, content
	}

	var out strings.Builder
	pos := 0
	inThink := false

	for pos < len(content) {
		if inThink {
			if endIdx := strings.Index(content[pos:], thinkEnd); endIdx >= 0 {
				thinkingText := content[pos : pos+endIdx]
				if thinkingText != "" {
					thinkingChunks = append(thinkingChunks, core.StreamChunk{
						Type:  core.ChunkThinking,
						Delta: thinkingText,
					})
				}
				pos += endIdx + len(thinkEnd)
				inThink = false
				continue
			}
			// No closing tag — emit remainder as thinking.
			thinkingText := content[pos:]
			if thinkingText != "" {
				thinkingChunks = append(thinkingChunks, core.StreamChunk{
					Type:  core.ChunkThinking,
					Delta: thinkingText,
				})
			}
			return thinkingChunks, out.String()
		}

		if startIdx := strings.Index(content[pos:], thinkStart); startIdx >= 0 {
			out.WriteString(content[pos : pos+startIdx])
			pos += startIdx + len(thinkStart)
			inThink = true
			continue
		}

		// No more think tags — write remainder.
		out.WriteString(content[pos:])
		break
	}

	return thinkingChunks, out.String()
}

// ThinkTagState tracks stateful extraction of think tags across streaming
// content chunks. Because <think> and </think> tags may arrive split across
// multiple SSE events, the parser buffers potential tag prefixes/suffixes.
//
// Flush() must be called when the stream ends to emit any remaining buffered
// content; calling it again does not re-emit what it already returned.
type ThinkTagState struct {
	thinkingMode bool
	buf          string // buffered text that might be part of a tag
}

// ProcessFeed ingests one streaming content delta and returns thinking and/or
// text chunks. The caller must call Flush() when the stream ends.
func (ts *ThinkTagState) ProcessFeed(delta string) []core.StreamChunk {
	// Fast path: when no tag scan is in flight (not inside a think block and
	// nothing buffered as a potential partial tag) and the delta carries no
	// '<' that could open one, forward it verbatim with no buffering and no
	// allocation. This is the common case for models that never emit inline
	// think tags, so per-chunk overhead stays at zero and no token is ever
	// held back waiting for the next frame.
	if !ts.thinkingMode && ts.buf == "" {
		if delta == "" {
			return nil
		}
		if !strings.ContainsRune(delta, '<') {
			return []core.StreamChunk{{Type: core.ChunkText, Delta: delta}}
		}
	}

	ts.buf += delta
	var chunks []core.StreamChunk

	for len(ts.buf) > 0 {
		if !ts.thinkingMode {
			// Not in thinking mode — look for start tag.

			if idx := strings.Index(ts.buf, thinkStart); idx >= 0 {
				// Emit text before the tag.
				if idx > 0 {
					chunks = append(chunks, core.StreamChunk{
						Type:  core.ChunkText,
						Delta: ts.buf[:idx],
					})
				}
				ts.buf = ts.buf[idx+len(thinkStart):]
				ts.thinkingMode = true
				continue
			}

			// No start tag found. Check if suffix of buf could be partial tag.
			partial := longestTagPrefix(ts.buf)
			if partial == len(ts.buf) {
				// Entire buf is a potential tag prefix — hold.
				return chunks
			}
			// Emit safe portion, keep potential prefix.
			safe := len(ts.buf) - partial
			if safe > 0 {
				chunks = append(chunks, core.StreamChunk{
					Type:  core.ChunkText,
					Delta: ts.buf[:safe],
				})
			}
			ts.buf = ts.buf[safe:]
			return chunks
		}

		// In thinking mode — look for end tag.
		if idx := strings.Index(ts.buf, thinkEnd); idx >= 0 {
			thinkingText := ts.buf[:idx]
			if thinkingText != "" {
				chunks = append(chunks, core.StreamChunk{
					Type:  core.ChunkThinking,
					Delta: thinkingText,
				})
			}
			ts.buf = ts.buf[idx+len(thinkEnd):]
			ts.thinkingMode = false
			continue
		}

		// No end tag. Check for partial end tag suffix.
		partial := longestTagSuffix(ts.buf)
		if partial == len(ts.buf) {
			return chunks // entirely potential suffix
		}
		safe := len(ts.buf) - partial
		if safe > 0 {
			chunks = append(chunks, core.StreamChunk{
				Type:  core.ChunkThinking,
				Delta: ts.buf[:safe],
			})
		}
		ts.buf = ts.buf[safe:]
		return chunks
	}

	return chunks
}

// Flush emits any buffered content. Call it before a non-content event and at
// stream end; it keeps the open/closed tag state, and a call with nothing
// buffered returns nil.
func (ts *ThinkTagState) Flush() []core.StreamChunk {
	if len(ts.buf) == 0 {
		return nil
	}
	var chunks []core.StreamChunk
	if ts.thinkingMode {
		chunks = append(chunks, core.StreamChunk{
			Type:  core.ChunkThinking,
			Delta: ts.buf,
		})
	} else {
		chunks = append(chunks, core.StreamChunk{
			Type:  core.ChunkText,
			Delta: ts.buf,
		})
	}
	ts.buf = ""
	return chunks
}

// longestTagPrefix returns the length of the longest prefix of s that matches
// a prefix of <think>. Used to detect partial tag arrivals in streaming.
func longestTagPrefix(s string) int {
	tag := thinkStart
	max := 0
	// Check from longest possible match down to 1.
	for l := min(len(s), len(tag)); l > 0; l-- {
		if s[len(s)-l:] == tag[:l] {
			max = l
			break
		}
	}
	return max
}

// longestTagSuffix returns the length of the longest suffix of s that matches
// a prefix of </think>. Used to detect partial end tag arrivals in thinking mode.
func longestTagSuffix(s string) int {
	tag := thinkEnd // </think>
	max := 0
	for l := min(len(s), len(tag)); l > 0; l-- {
		if s[len(s)-l:] == tag[:l] {
			max = l
			break
		}
	}
	return max
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
