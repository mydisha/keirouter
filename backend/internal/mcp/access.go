package mcp

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/mydisha/keirouter/backend/internal/store"
)

// accessTTL bounds how long a key's access is cached. Changes made from the
// dashboard invalidate the entry at once; the TTL covers other instances.
const accessTTL = 5 * time.Second

// Access is a key's resolved MCP access. The list and call paths both ask
// CanCall, so they cannot disagree.
type Access struct {
	Mode   string
	grants map[string]grant // server id -> grant, selected mode only
}

type grant struct {
	all   bool
	tools map[string]struct{}
}

// NewAccess builds an Access from stored rows.
func NewAccess(a store.MCPKeyAccess) *Access {
	acc := &Access{Mode: a.Mode, grants: map[string]grant{}}
	for _, g := range a.Grants {
		gr := grant{all: g.ToolMode == store.MCPModeAll, tools: map[string]struct{}{}}
		for _, t := range g.Tools {
			gr.tools[t] = struct{}{}
		}
		acc.grants[g.ServerID] = gr
	}
	return acc
}

// AllAccess is the access of a key in all mode.
var AllAccess = &Access{Mode: store.MCPModeAll}

// CanUseServer reports whether any tool of the server may be visible.
func (a *Access) CanUseServer(serverID string) bool {
	switch a.Mode {
	case store.MCPModeAll:
		return true
	case store.MCPModeSelected:
		_, ok := a.grants[serverID]
		return ok
	default:
		return false
	}
}

// CanCall reports whether the key may call tool on server. The caller has
// already checked that the server exposes the tool.
func (a *Access) CanCall(serverID, tool string) bool {
	switch a.Mode {
	case store.MCPModeAll:
		return true
	case store.MCPModeSelected:
		g, ok := a.grants[serverID]
		if !ok {
			return false
		}
		if g.all {
			return true
		}
		_, ok = g.tools[tool]
		return ok
	default:
		return false
	}
}

// AllToolsOf reports whether the key sees every exposed tool of the server.
func (a *Access) AllToolsOf(serverID string) bool {
	if a.Mode == store.MCPModeAll {
		return true
	}
	g, ok := a.grants[serverID]
	return ok && g.all
}

// accessCache caches resolved access per key.
type accessCache struct {
	repo  *store.MCPRepo
	group singleflight.Group

	mu      sync.Mutex
	entries map[string]accessEntry
}

type accessEntry struct {
	access  *Access
	expires time.Time
}

func newAccessCache(repo *store.MCPRepo) *accessCache {
	return &accessCache{repo: repo, entries: map[string]accessEntry{}}
}

func (c *accessCache) get(ctx context.Context, keyID string) (*Access, error) {
	now := time.Now()
	c.mu.Lock()
	e, ok := c.entries[keyID]
	c.mu.Unlock()
	if ok && now.Before(e.expires) {
		return e.access, nil
	}
	v, err, _ := c.group.Do(keyID, func() (any, error) {
		raw, err := c.repo.GetKeyAccess(ctx, keyID)
		if err != nil {
			return nil, err
		}
		acc := AllAccess
		if raw.Mode != store.MCPModeAll {
			acc = NewAccess(raw)
		}
		c.mu.Lock()
		if len(c.entries) > 4096 {
			c.entries = map[string]accessEntry{} // bounded; a miss costs one small query
		}
		c.entries[keyID] = accessEntry{access: acc, expires: time.Now().Add(accessTTL)}
		c.mu.Unlock()
		return acc, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*Access), nil
}

func (c *accessCache) invalidate(keyID string) {
	c.mu.Lock()
	delete(c.entries, keyID)
	c.mu.Unlock()
}
