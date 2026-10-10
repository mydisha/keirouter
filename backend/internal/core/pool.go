// Reusable buffer pools to reduce GC pressure in the hot path.
package core

import (
	"bufio"
	"sync"
)

// SSEWriterPool pools *bufio.Writer sized for SSE event writing. Writers are
// reset to the target http.ResponseWriter before use and reset to nil before
// being returned so a pooled writer never pins a finished response.
var SSEWriterPool = sync.Pool{
	New: func() any { return bufio.NewWriterSize(nil, 16*1024) },
}
