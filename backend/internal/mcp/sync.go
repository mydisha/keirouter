package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// maxToolsPerServer bounds one server's catalog.
const maxToolsPerServer = 2000

// syncParallel bounds concurrent upstream syncs in the background loop.
const syncParallel = 4

// ToolInfo summarises a synced tool for the dashboard.
type ToolInfo struct {
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	InvalidReason string `json:"invalid_reason,omitempty"`
}

// ProbeResult is what a connection test or sync learned about a server.
type ProbeResult struct {
	ProtocolVersion string     `json:"protocol_version"`
	Tools           []ToolInfo `json:"tools"`
	synced          []store.MCPSyncedTool
}

// Syncer pulls tool catalogs from upstream servers into the store.
type Syncer struct {
	repo     *store.MCPRepo
	client   *Client
	sealer   *crypto.Sealer
	log      *slog.Logger
	interval time.Duration
	// onChange runs after a sync changed the store.
	onChange func(context.Context)

	inflight sync.Map // server id -> struct{}; one sync per server at a time
}

// NewSyncer builds a syncer. interval <= 0 disables the background loop.
func NewSyncer(repo *store.MCPRepo, client *Client, sealer *crypto.Sealer, log *slog.Logger, interval time.Duration, onChange func(context.Context)) *Syncer {
	if log == nil {
		log = slog.Default()
	}
	return &Syncer{repo: repo, client: client, sealer: sealer, log: log, interval: interval, onChange: onChange}
}

// Probe connects to a server configuration without saving anything: it
// negotiates the protocol version and lists tools.
func (s *Syncer) Probe(ctx context.Context, srv store.MCPServer, sec Secret) (ProbeResult, error) {
	t := TargetFor(srv, sec)
	t.Key = "probe:" + srv.URL
	defer s.client.Forget(t.Key)
	version, err := s.client.Negotiate(ctx, t)
	if err != nil {
		return ProbeResult{}, err
	}
	t.Version = version
	return s.list(ctx, t)
}

func (s *Syncer) list(ctx context.Context, t Target) (ProbeResult, error) {
	raw, err := s.client.ListTools(ctx, t)
	if err != nil {
		return ProbeResult{}, err
	}
	if len(raw) > maxToolsPerServer {
		return ProbeResult{}, fmt.Errorf("server lists %d tools; the limit is %d", len(raw), maxToolsPerServer)
	}
	res := ProbeResult{ProtocolVersion: t.Version}
	for _, def := range raw {
		var head struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		}
		if err := json.Unmarshal(def, &head); err != nil || head.Name == "" {
			continue
		}
		invalid := ""
		if _, err := ParseParamHeaders(head.InputSchema); err != nil {
			invalid = err.Error()
		}
		hash, err := canonicalHash(def)
		if err != nil {
			continue
		}
		res.Tools = append(res.Tools, ToolInfo{Name: head.Name, Description: head.Description, InvalidReason: invalid})
		res.synced = append(res.synced, store.MCPSyncedTool{
			Name: head.Name, Definition: string(def), DefinitionHash: hash, InvalidReason: invalid,
		})
	}
	return res, nil
}

// SyncServer refreshes one server's catalog and health. A failed sync keeps
// the previous catalog and marks the server failing.
func (s *Syncer) SyncServer(ctx context.Context, id string) (ProbeResult, error) {
	if _, busy := s.inflight.LoadOrStore(id, struct{}{}); busy {
		return ProbeResult{}, errors.New("a sync for this server is already running")
	}
	defer s.inflight.Delete(id)

	srv, err := s.repo.GetServer(ctx, id)
	if err != nil {
		return ProbeResult{}, err
	}
	res, syncErr := s.syncTarget(ctx, srv)
	now := time.Now()
	if syncErr != nil {
		if err := s.repo.SetServerHealth(ctx, id, store.MCPHealthFailing, syncErr.Error(), "", now); err != nil {
			s.log.Warn("mcp: record sync failure", "server", srv.Slug, "err", err)
		}
		s.changed(ctx)
		return ProbeResult{}, syncErr
	}
	if err := s.repo.ReplaceTools(ctx, id, res.synced, now); err != nil {
		return ProbeResult{}, err
	}
	if err := s.repo.SetServerHealth(ctx, id, store.MCPHealthHealthy, "", res.ProtocolVersion, now); err != nil {
		return ProbeResult{}, err
	}
	s.changed(ctx)
	return res, nil
}

func (s *Syncer) syncTarget(ctx context.Context, srv store.MCPServer) (ProbeResult, error) {
	sec, err := OpenSecret(s.sealer, srv)
	if err != nil {
		return ProbeResult{}, err
	}
	t := TargetFor(srv, sec)
	if t.Version != "" {
		res, err := s.list(ctx, t)
		if err == nil {
			return res, nil
		}
		var he *HTTPError
		if !errors.As(err, &he) || he.Status == http.StatusUnauthorized || he.Status == http.StatusForbidden || he.Status >= 500 {
			return ProbeResult{}, err
		}
		// The server may have changed protocol era; negotiate again.
		s.client.Forget(t.Key)
	}
	version, err := s.client.Negotiate(ctx, t)
	if err != nil {
		return ProbeResult{}, err
	}
	t.Version = version
	return s.list(ctx, t)
}

func (s *Syncer) changed(ctx context.Context) {
	if s.onChange != nil {
		s.onChange(ctx)
	}
}

// Run syncs every enabled server now and then on each interval until ctx ends.
func (s *Syncer) Run(ctx context.Context, tenant string) {
	if s.interval <= 0 {
		return
	}
	s.SyncAll(ctx, tenant)
	tick := time.NewTicker(s.interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.SyncAll(ctx, tenant)
		}
	}
}

// SyncAll syncs every enabled server of a tenant, a few at a time.
func (s *Syncer) SyncAll(ctx context.Context, tenant string) {
	servers, err := s.repo.ListServers(ctx, tenant)
	if err != nil {
		s.log.Warn("mcp: list servers for sync", "err", err)
		return
	}
	sem := make(chan struct{}, syncParallel)
	var wg sync.WaitGroup
	for _, srv := range servers {
		if !srv.Enabled {
			continue
		}
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(id, slug string, timeout int) {
			defer wg.Done()
			defer func() { <-sem }()
			sctx, cancel := context.WithTimeout(ctx, syncTimeout(timeout))
			defer cancel()
			if _, err := s.SyncServer(sctx, id); err != nil && ctx.Err() == nil {
				s.log.Info("mcp: sync failed", "server", slug, "err", err)
			}
		}(srv.ID, srv.Slug, srv.TimeoutMS)
	}
	wg.Wait()
}

func syncTimeout(timeoutMS int) time.Duration {
	d := time.Duration(timeoutMS) * time.Millisecond
	if d < 10*time.Second {
		d = 10 * time.Second
	}
	return 2 * d
}

// canonicalHash hashes a JSON value with object keys sorted, so a server
// that reorders keys between calls does not look like it changed a tool.
func canonicalHash(raw json.RawMessage) (string, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
