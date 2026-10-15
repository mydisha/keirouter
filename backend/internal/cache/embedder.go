package cache

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// Embedder turns a request's prompt into a vector for cache lookup.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// PromptText extracts a stable, normalized prompt string from a request for
// embedding. It concatenates system text and message text in order; tool-call
// and binary parts are ignored so cache keys stay text-stable.
func PromptText(req *core.ChatRequest) string {
	var b strings.Builder
	if req.System != "" {
		b.WriteString(req.System)
		b.WriteByte('\n')
	}
	for _, m := range req.Messages {
		b.WriteString(string(m.Role))
		b.WriteByte(':')
		for _, p := range m.Content {
			switch p.Type {
			case core.PartText:
				b.WriteString(p.Text)
			case core.PartToolResult:
				if p.ToolResult != nil {
					b.WriteString(p.ToolResult.Content)
				}
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// HashEmbedder is a deterministic, dependency-free embedder. Identical prompts
// map to identical vectors (cosine 1.0), giving exact-prompt caching with no
// embeddings provider required. For true semantic (near-match) caching, plug in
// a provider-backed embedder instead.
type HashEmbedder struct {
	dims int
}

// NewHashEmbedder builds a hash embedder producing vectors of the given length.
func NewHashEmbedder(dims int) *HashEmbedder {
	if dims <= 0 {
		dims = 16
	}
	return &HashEmbedder{dims: dims}
}

// Embed maps text to a deterministic unit-ish vector derived from its SHA-256
// digest. The mapping is stable, so identical text always yields the same
// vector; different text yields effectively unrelated vectors.
func (h *HashEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	vec := make([]float32, h.dims)
	// Hash the prompt once, then expand the digest deterministically across
	// the requested dimensions by re-hashing digest+counter. Hashing
	// text+counter per dimension copied the whole prompt dims times (32 MB
	// of allocation per 1 MB prompt).
	base := sha256.Sum256([]byte(text))
	var buf [sha256.Size + 8]byte
	copy(buf[:], base[:])
	for i := 0; i < h.dims; i++ {
		binary.LittleEndian.PutUint64(buf[sha256.Size:], uint64(i))
		sum := sha256.Sum256(buf[:])
		// Map the first 4 bytes to a float in [-1, 1].
		u := binary.LittleEndian.Uint32(sum[:4])
		vec[i] = float32(int32(u)) / float32(1<<31) // normalize to ~[-1,1]
	}
	return vec, nil
}
