package mcp

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// versionSetting is bumped on every change to MCP servers or tools so other
// gateway instances sharing the database reload their registry.
const versionSetting = "mcp.version"

// watchInterval is how often an instance checks versionSetting.
const watchInterval = 10 * time.Second

// Options configures a Manager.
type Options struct {
	Tenant       string
	Info         Implementation
	SyncInterval time.Duration
	CatalogFile  string
	CatalogOnly  bool
	Logger       *slog.Logger
}

// Manager owns the MCP gateway's state: registry, upstream client, syncer
// and preset catalog.
type Manager struct {
	Repo     *store.MCPRepo
	Registry *Registry
	Client   *Client
	Syncer   *Syncer
	Catalog  *Catalog
	Sealer   *crypto.Sealer

	tenant   string
	settings *store.SettingsRepo
	log      *slog.Logger
	seen     atomic.Value // string: last versionSetting this instance applied
}

// NewManager builds a Manager and loads the registry.
func NewManager(ctx context.Context, db *store.DB, sealer *crypto.Sealer, opts Options) (*Manager, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Tenant == "" {
		opts.Tenant = store.DefaultTenantID
	}
	catalog, err := NewCatalog(opts.CatalogFile, opts.CatalogOnly)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		Repo:     db.MCP(),
		Client:   NewClient(opts.Info, nil),
		Catalog:  catalog,
		Sealer:   sealer,
		tenant:   opts.Tenant,
		settings: db.Settings(),
		log:      opts.Logger,
	}
	m.Registry = NewRegistry(m.Repo, opts.Tenant)
	m.Syncer = NewSyncer(m.Repo, m.Client, sealer, opts.Logger, opts.SyncInterval, m.Changed)
	if err := m.Registry.Reload(ctx); err != nil {
		return nil, err
	}
	v, _ := m.settings.Get(ctx, versionSetting)
	m.seen.Store(v)
	return m, nil
}

// Tenant returns the tenant this manager serves.
func (m *Manager) Tenant() string { return m.tenant }

// Changed reloads the registry after a write and tells other instances.
func (m *Manager) Changed(ctx context.Context) {
	if err := m.Registry.Reload(ctx); err != nil {
		m.log.Warn("mcp: reload registry", "err", err)
	}
	v := strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := m.settings.Set(ctx, versionSetting, v); err != nil {
		m.log.Warn("mcp: bump registry version", "err", err)
		return
	}
	m.seen.Store(v)
}

// Run keeps the catalogs in sync and picks up changes made by other
// instances, until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	// Wait for the sync loop before returning: the caller closes the database
	// once Run returns.
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.Syncer.Run(ctx, m.tenant)
	}()
	defer func() { <-done }()
	tick := time.NewTicker(watchInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			v, err := m.settings.Get(ctx, versionSetting)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				continue
			}
			if seen, _ := m.seen.Load().(string); v == seen {
				continue
			}
			m.seen.Store(v)
			if err := m.Registry.Reload(ctx); err != nil {
				m.log.Warn("mcp: reload registry", "err", err)
			}
		}
	}
}
