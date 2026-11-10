package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mydisha/keirouter/backend/internal/httputil"
)

// fakeServer is a scriptable upstream MCP server for tests. It speaks the
// modern era, the legacy era, or both, and records what it received.
type fakeServer struct {
	t      *testing.T
	modern bool
	legacy bool
	// sse answers requests as an SSE stream with a progress notification
	// before the response.
	sse bool
	// requireAuth, when set, is the Authorization value every request needs.
	requireAuth string
	// tools is the tools/list result; pageSize > 0 paginates it.
	tools    []string
	pageSize int
	// callResult is the raw result of tools/call.
	callResult string

	mu        sync.Mutex
	sessions  map[string]bool
	nextSess  int
	requests  []recorded
	initCount int
}

type recorded struct {
	Header http.Header
	Body   Request
}

func newFake(t *testing.T, f *fakeServer) *httptest.Server {
	t.Helper()
	f.t = t
	f.sessions = map[string]bool{}
	if f.callResult == "" {
		f.callResult = `{"content":[{"type":"text","text":"ok"}],"isError":false}`
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return srv
}

func allowPrivate(t *testing.T) {
	t.Helper()
	httputil.SetAllowPrivateBaseURL(true)
	t.Cleanup(func() { httputil.SetAllowPrivateBaseURL(false) })
}

func (f *fakeServer) dropSessions() {
	f.mu.Lock()
	f.sessions = map[string]bool{}
	f.mu.Unlock()
}

func (f *fakeServer) recorded() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.requests...)
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.requests = append(f.requests, recorded{Header: r.Header.Clone(), Body: req})
	f.mu.Unlock()

	if f.requireAuth != "" && r.Header.Get("Authorization") != f.requireAuth {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	var params struct {
		Meta   map[string]json.RawMessage `json:"_meta"`
		Cursor string                     `json:"cursor"`
		Name   string                     `json:"name"`
	}
	_ = json.Unmarshal(req.Params, &params)
	_, isModernReq := params.Meta[MetaProtocolVersion]

	switch {
	case req.Method == "initialize":
		if !f.legacy {
			writeRPCError(w, http.StatusBadRequest, req.ID, CodeHeaderMismatch, "missing MCP-Protocol-Version")
			return
		}
		f.mu.Lock()
		f.nextSess++
		f.initCount++
		id := fmt.Sprintf("sess-%d", f.nextSess)
		f.sessions[id] = true
		f.mu.Unlock()
		w.Header().Set(HeaderSessionID, id)
		writeResult(w, req.ID, `{"protocolVersion":"`+VersionLegacyLatest+`","capabilities":{"tools":{}},"serverInfo":{"name":"fake","version":"1"}}`)
		return
	case req.IsNotification():
		w.WriteHeader(http.StatusAccepted)
		return
	}

	if isModernReq {
		if !f.modern {
			// A legacy server: no session header, so 400 without a modern error.
			http.Error(w, "Bad Request: No valid session ID provided", http.StatusBadRequest)
			return
		}
		if r.Header.Get(HeaderProtocolVersion) != VersionModern || r.Header.Get(HeaderMethod) != req.Method {
			writeRPCError(w, http.StatusBadRequest, req.ID, CodeHeaderMismatch, "header mismatch")
			return
		}
		if req.Method == "tools/call" {
			if got, _ := DecodeHeaderValue(r.Header.Get(HeaderName)); got != params.Name {
				writeRPCError(w, http.StatusBadRequest, req.ID, CodeHeaderMismatch, "Mcp-Name mismatch")
				return
			}
		}
	} else {
		if !f.legacy {
			writeRPCError(w, http.StatusBadRequest, req.ID, CodeHeaderMismatch, "missing headers")
			return
		}
		f.mu.Lock()
		ok := f.sessions[r.Header.Get(HeaderSessionID)]
		f.mu.Unlock()
		if !ok {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
	}

	switch req.Method {
	case "server/discover":
		writeResult(w, req.ID, `{"resultType":"complete","supportedVersions":["`+VersionModern+`"],"capabilities":{"tools":{}}}`)
	case "tools/list":
		start := 0
		if params.Cursor != "" {
			fmt.Sscanf(params.Cursor, "c%d", &start)
		}
		end := len(f.tools)
		next := ""
		if f.pageSize > 0 && start+f.pageSize < end {
			end = start + f.pageSize
			next = fmt.Sprintf("c%d", end)
		}
		res := `{"tools":[` + strings.Join(f.tools[start:end], ",") + `]`
		if next != "" {
			res += `,"nextCursor":"` + next + `"`
		}
		res += `}`
		f.respond(w, req.ID, res)
	case "tools/call":
		f.respond(w, req.ID, f.callResult)
	default:
		code := http.StatusOK
		if isModernReq {
			code = http.StatusNotFound
		}
		writeRPCError(w, code, req.ID, CodeMethodNotFound, "method not found")
	}
}

func (f *fakeServer) respond(w http.ResponseWriter, id json.RawMessage, result string) {
	if !f.sse {
		writeResult(w, id, result)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progress\":1}}\n\n")
	fmt.Fprintf(w, ": keepalive\n\n")
	fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":%s}\n\n", id, result)
}

func writeResult(w http.ResponseWriter, id json.RawMessage, result string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, id, result)
}

func writeRPCError(w http.ResponseWriter, status int, id json.RawMessage, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":%d,"message":%q}}`, id, code, msg)
}

const toolWeather = `{"name":"get_weather","description":"Weather","inputSchema":{"type":"object","properties":{"city":{"type":"string"}}}}`
const toolRegion = `{"name":"run_sql","inputSchema":{"type":"object","properties":{"region":{"type":"string","x-mcp-header":"Region"},"query":{"type":"string"}}}}`
const toolBadHeader = `{"name":"bad_header","inputSchema":{"type":"object","properties":{"ratio":{"type":"number","x-mcp-header":"Ratio"}}}}`
