package transform

import (
	json "github.com/mydisha/keirouter/backend/internal/fastjson"
)

// SSE frame encoders shared by the stream renderers.
//
// Every renderer used to build each event as nested map[string]any and marshal
// it, which cost 11–24 allocations per streamed chunk (map headers, interface
// boxing, sonic's map-iteration state, and three slice copies to add the
// "data: " prefix). Marshalling a typed struct straight into a frame-sized
// buffer costs two.

var (
	sseDataPrefix  = []byte("data: ")
	sseEventPrefix = []byte("event: ")
	sseFrameEnd    = []byte("\n\n")
)

// sseData encodes v as "data: <json>\n\n".
func sseData(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	out := make([]byte, 0, len(sseDataPrefix)+len(b)+len(sseFrameEnd))
	out = append(out, sseDataPrefix...)
	out = append(out, b...)
	return append(out, sseFrameEnd...)
}

// sseNamed encodes v as "event: <name>\ndata: <json>\n\n".
func sseNamed(name string, v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	out := make([]byte, 0, len(sseEventPrefix)+len(name)+1+len(sseDataPrefix)+len(b)+len(sseFrameEnd))
	out = append(out, sseEventPrefix...)
	out = append(out, name...)
	out = append(out, '\n')
	out = append(out, sseDataPrefix...)
	out = append(out, b...)
	return append(out, sseFrameEnd...)
}
