package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// ToolNameSeparator joins a server slug and a tool name. Slugs only contain
// [a-z0-9-], so the first "__" in an exposed name is always the separator.
const ToolNameSeparator = "__"

// SplitToolName splits an exposed tool name into server slug and tool name.
func SplitToolName(name string) (slug, tool string, ok bool) {
	slug, tool, ok = strings.Cut(name, ToolNameSeparator)
	if !ok || slug == "" || tool == "" {
		return "", "", false
	}
	return slug, tool, true
}

// Tool is one tool as served to clients.
type Tool struct {
	Name string
	// Bare is the definition as received from upstream, for /mcp/{slug}.
	Bare json.RawMessage
	// Prefixed is the definition with name set to slug__name, for /mcp.
	Prefixed json.RawMessage
	// Params are the x-mcp-header annotations, mirrored as Mcp-Param-*.
	Params []ParamHeader
}

// Server is one enabled upstream server and the tools it exposes.
type Server struct {
	store.MCPServer
	Tools  map[string]*Tool
	Sorted []*Tool // exposed tools ordered by name

	// joinedBare and joinedPrefixed are the comma-joined definitions of every
	// exposed tool, served as is to keys that see the whole server.
	joinedBare     []byte
	joinedPrefixed []byte

	// target is built once per snapshot from the sealed secret.
	targetOnce sync.Once
	target     Target
	targetErr  error
}

// Snapshot is an immutable view of every enabled server and exposed tool.
// Readers never lock; a reload swaps the whole snapshot.
type Snapshot struct {
	BySlug  map[string]*Server
	ByID    map[string]*Server
	Ordered []*Server // ordered by slug

	// allPrefixed is every exposed tool of every server, comma-joined, for
	// keys in all mode.
	allPrefixed []byte
}

// Tool returns a server's exposed tool.
func (s *Server) Tool(name string) (*Tool, bool) {
	t, ok := s.Tools[name]
	return t, ok
}

// Registry holds the current snapshot for one tenant.
type Registry struct {
	repo   *store.MCPRepo
	tenant string
	snap   atomic.Pointer[Snapshot]
}

// NewRegistry builds an empty registry. Call Reload before serving.
func NewRegistry(repo *store.MCPRepo, tenant string) *Registry {
	r := &Registry{repo: repo, tenant: tenant}
	r.snap.Store(&Snapshot{BySlug: map[string]*Server{}, ByID: map[string]*Server{}})
	return r
}

// Snapshot returns the current snapshot.
func (r *Registry) Snapshot() *Snapshot { return r.snap.Load() }

// Reload rebuilds the snapshot from the store.
func (r *Registry) Reload(ctx context.Context) error {
	servers, err := r.repo.ListServers(ctx, r.tenant)
	if err != nil {
		return err
	}
	tools, err := r.repo.ListTenantTools(ctx, r.tenant)
	if err != nil {
		return err
	}
	snap, err := buildSnapshot(servers, tools)
	if err != nil {
		return err
	}
	r.snap.Store(snap)
	return nil
}

func buildSnapshot(servers []store.MCPServer, tools []store.MCPServerTool) (*Snapshot, error) {
	snap := &Snapshot{BySlug: map[string]*Server{}, ByID: map[string]*Server{}}
	for _, s := range servers {
		if !s.Enabled {
			continue
		}
		srv := &Server{MCPServer: s, Tools: map[string]*Tool{}}
		snap.BySlug[s.Slug] = srv
		snap.ByID[s.ID] = srv
		snap.Ordered = append(snap.Ordered, srv)
	}
	sort.Slice(snap.Ordered, func(i, j int) bool { return snap.Ordered[i].Slug < snap.Ordered[j].Slug })

	for _, t := range tools {
		srv := snap.ByID[t.ServerID]
		if srv == nil || !t.Enabled || t.InvalidReason != "" {
			continue
		}
		bare := json.RawMessage(t.Definition)
		prefixed, err := renameTool(bare, srv.Slug+ToolNameSeparator+t.Name)
		if err != nil {
			continue // stored definitions were validated at sync; skip a corrupt row
		}
		params, err := ParseParamHeaders(inputSchemaOf(bare))
		if err != nil {
			continue
		}
		tool := &Tool{Name: t.Name, Bare: bare, Prefixed: prefixed, Params: params}
		srv.Tools[t.Name] = tool
		srv.Sorted = append(srv.Sorted, tool)
	}
	var all bytes.Buffer
	for _, srv := range snap.Ordered {
		sort.Slice(srv.Sorted, func(i, j int) bool { return srv.Sorted[i].Name < srv.Sorted[j].Name })
		var bare, pre bytes.Buffer
		for i, t := range srv.Sorted {
			if i > 0 {
				bare.WriteByte(',')
				pre.WriteByte(',')
			}
			bare.Write(t.Bare)
			pre.Write(t.Prefixed)
		}
		srv.joinedBare, srv.joinedPrefixed = bare.Bytes(), pre.Bytes()
		if len(srv.joinedPrefixed) > 0 {
			if all.Len() > 0 {
				all.WriteByte(',')
			}
			all.Write(srv.joinedPrefixed)
		}
	}
	snap.allPrefixed = all.Bytes()
	return snap, nil
}

// renameTool returns def with its "name" field replaced. Other fields are
// copied unchanged.
func renameTool(def json.RawMessage, name string) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(def, &fields); err != nil {
		return nil, fmt.Errorf("tool definition: %w", err)
	}
	n, _ := json.Marshal(name)
	fields["name"] = n
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(fields); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

func inputSchemaOf(def json.RawMessage) json.RawMessage {
	var t struct {
		InputSchema json.RawMessage `json:"inputSchema"`
	}
	_ = json.Unmarshal(def, &t)
	return t.InputSchema
}
