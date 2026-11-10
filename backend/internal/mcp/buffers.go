package mcp

import (
	"bytes"
	"sync"
)

var bufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

func getBuffer() *bytes.Buffer {
	b := bufPool.Get().(*bytes.Buffer)
	b.Reset()
	return b
}

// putBuffer returns a buffer to the pool; very large ones are dropped so
// one big tool list does not pin memory.
func putBuffer(b *bytes.Buffer) {
	if b.Cap() > 1<<20 {
		return
	}
	bufPool.Put(b)
}
