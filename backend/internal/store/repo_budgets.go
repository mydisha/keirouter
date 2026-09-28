package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"
)

// BudgetRepo persists spend limits and chains.
type BudgetRepo struct{ db *DB }

// Budgets returns the budget repository.
func (db *DB) Budgets() *BudgetRepo { return &BudgetRepo{db: db} }

// Create inserts a budget.
func (r *BudgetRepo) Create(ctx context.Context, b Budget) error {
	return r.insert(ctx, r.db.sql, b)
}

// CreateOnTx inserts a budget within an existing transaction.
func (r *BudgetRepo) CreateOnTx(ctx context.Context, tx *sql.Tx, b Budget) error {
	return r.insert(ctx, tx, b)
}

func (r *BudgetRepo) insert(ctx context.Context, ex sqlExec, b Budget) error {
	q := r.db.rebind(`INSERT INTO budgets
		(id, tenant_id, scope_kind, scope_id, limit_micros, limit_tokens, period, alert_pct, hard_cutoff, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := ex.ExecContext(ctx, q,
		b.ID, b.TenantID, string(b.ScopeKind), b.ScopeID, b.LimitMicros, b.LimitTokens, b.Period,
		b.AlertPct, boolToInt(b.HardCutoff), formatTime(b.CreatedAt), formatTime(b.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store: create budget: %w", err)
	}
	return nil
}

const budgetSelectCols = `id, tenant_id, scope_kind, scope_id, limit_micros, limit_tokens, period, alert_pct, hard_cutoff, created_at, updated_at`

// ListByScope returns budgets attached to a specific scope (kind + id),
// newest first. The ordering matches ListByTenant so callers that take the
// first row (e.g. the key top-up handler) agree with what the UI displays
// when a scope accidentally has more than one budget.
func (r *BudgetRepo) ListByScope(ctx context.Context, kind BudgetScope, scopeID string) ([]Budget, error) {
	q := r.db.rebind(`SELECT ` + budgetSelectCols + ` FROM budgets WHERE scope_kind = ? AND scope_id = ? ORDER BY created_at DESC, id DESC`)
	return r.queryList(ctx, q, string(kind), scopeID)
}

// ListByTenant returns all budgets for a tenant.
func (r *BudgetRepo) ListByTenant(ctx context.Context, tenantID string) ([]Budget, error) {
	q := r.db.rebind(`SELECT ` + budgetSelectCols + ` FROM budgets WHERE tenant_id = ? ORDER BY created_at DESC`)
	return r.queryList(ctx, q, tenantID)
}

// Get returns one budget by id.
func (r *BudgetRepo) Get(ctx context.Context, id string) (Budget, error) {
	q := r.db.rebind(`SELECT ` + budgetSelectCols + ` FROM budgets WHERE id = ?`)
	b, err := scanBudget(r.db.sql.QueryRowContext(ctx, q, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Budget{}, ErrNotFound
	}
	return b, err
}

// Update modifies an existing budget's mutable fields.
func (r *BudgetRepo) Update(ctx context.Context, b Budget) error {
	q := r.db.rebind(`UPDATE budgets SET limit_micros = ?, limit_tokens = ?, period = ?, alert_pct = ?, hard_cutoff = ?, updated_at = ? WHERE id = ?`)
	res, err := r.db.sql.ExecContext(ctx, q, b.LimitMicros, b.LimitTokens, b.Period, b.AlertPct, boolToInt(b.HardCutoff), formatTime(b.UpdatedAt), b.ID)
	if err != nil {
		return fmt.Errorf("store: update budget: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// IncrementLimitOnTx atomically adds deltaMicros to a budget's spend limit
// inside an existing transaction and returns the limit before and after.
//
// On Postgres the row is read with FOR UPDATE so concurrent top-ups serialize
// and the returned "before" snapshot cannot go stale. On SQLite the write lock
// already serializes the transaction. The persisted value is computed by the
// database (`limit_micros + ?`), never by a Go read-modify-write, so a stale
// snapshot can never clobber a concurrent increment. All arithmetic is int64
// micro-USD.
//
// Overflow is checked against the value read under the lock and reported as
// ErrLimitOverflow before any write, so a wrap can never be persisted.
func (r *BudgetRepo) IncrementLimitOnTx(ctx context.Context, tx *sql.Tx, id string, deltaMicros int64) (int64, int64, error) {
	sel := r.db.rebind(`SELECT limit_micros FROM budgets WHERE id = ?`)
	if r.db.Dialect() == DialectPostgres {
		sel += " FOR UPDATE"
	}
	var before int64
	if err := tx.QueryRowContext(ctx, sel, id).Scan(&before); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, ErrNotFound
		}
		return 0, 0, fmt.Errorf("store: read budget limit: %w", err)
	}
	if deltaMicros > 0 && before > math.MaxInt64-deltaMicros {
		return before, before, ErrLimitOverflow
	}
	if deltaMicros < 0 && before < math.MinInt64-deltaMicros {
		return before, before, ErrLimitOverflow
	}
	after := before + deltaMicros
	up := r.db.rebind(`UPDATE budgets SET limit_micros = limit_micros + ?, updated_at = ? WHERE id = ?`)
	res, err := tx.ExecContext(ctx, up, deltaMicros, formatTime(time.Now()), id)
	if err != nil {
		return 0, 0, fmt.Errorf("store: increment budget limit: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, 0, ErrNotFound
	}
	return before, after, nil
}

// LockKeyTopup serializes concurrent top-ups for one API key. On Postgres it
// takes a transaction-scoped advisory lock keyed by the key id, so two requests
// cannot both create the key's budget or lose an increment. On SQLite, whose
// single writer already serializes transactions, it is a no-op.
func (r *BudgetRepo) LockKeyTopup(ctx context.Context, tx *sql.Tx, keyID string) error {
	if r.db.Dialect() != DialectPostgres {
		return nil
	}
	sum := sha256.Sum256([]byte("key_topup:" + keyID))
	lockID := int64(binary.BigEndian.Uint64(sum[:8]))
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", lockID); err != nil {
		return fmt.Errorf("store: lock key topup: %w", err)
	}
	return nil
}

// Delete removes a budget.
func (r *BudgetRepo) Delete(ctx context.Context, id string) error {
	q := r.db.rebind(`DELETE FROM budgets WHERE id = ?`)
	_, err := r.db.sql.ExecContext(ctx, q, id)
	return err
}

func (r *BudgetRepo) queryList(ctx context.Context, q string, args ...any) ([]Budget, error) {
	rows, err := r.db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list budgets: %w", err)
	}
	defer rows.Close()

	var out []Budget
	for rows.Next() {
		b, err := scanBudget(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func scanBudget(scan func(dest ...any) error) (Budget, error) {
	var (
		b          Budget
		scopeKind  string
		hardCutoff int
		created    string
		updated    string
	)
	err := scan(&b.ID, &b.TenantID, &scopeKind, &b.ScopeID, &b.LimitMicros, &b.LimitTokens, &b.Period,
		&b.AlertPct, &hardCutoff, &created, &updated)
	if err != nil {
		return Budget{}, err
	}
	b.ScopeKind = BudgetScope(scopeKind)
	b.HardCutoff = hardCutoff != 0
	b.CreatedAt = parseTime(created)
	b.UpdatedAt = parseTime(updated)
	return b, nil
}

// ChainRepo persists routing chains and their steps.
type ChainRepo struct{ db *DB }

// Chains returns the chain repository.
func (db *DB) Chains() *ChainRepo { return &ChainRepo{db: db} }

// Create inserts a chain and its steps in a transaction.
func (r *ChainRepo) Create(ctx context.Context, c Chain) error {
	tx, err := r.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	cq := r.db.rebind(`INSERT INTO chains (id, tenant_id, name, strategy, fallback_provider, fallback_model,
		input_per_m, output_per_m, cache_write_per_m, cache_read_per_m, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if _, err := tx.ExecContext(ctx, cq, c.ID, c.TenantID, c.Name, c.Strategy, c.FallbackProvider, c.FallbackModel,
		c.InputPerM, c.OutputPerM, c.CacheWritePerM, c.CacheReadPerM, formatTime(c.CreatedAt), formatTime(c.UpdatedAt)); err != nil {
		return fmt.Errorf("store: create chain: %w", err)
	}

	sq := r.db.rebind(`INSERT INTO chain_steps (id, chain_id, position, provider, model, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`)
	for _, s := range c.Steps {
		if _, err := tx.ExecContext(ctx, sq, s.ID, c.ID, s.Position, s.Provider, s.Model,
			formatTime(s.CreatedAt)); err != nil {
			return fmt.Errorf("store: create chain step: %w", err)
		}
	}
	return tx.Commit()
}

// Get returns a chain with its ordered steps.
func (r *ChainRepo) Get(ctx context.Context, id string) (Chain, error) {
	cq := r.db.rebind(`SELECT id, tenant_id, name, strategy, fallback_provider, fallback_model, input_per_m, output_per_m, cache_write_per_m, cache_read_per_m, created_at, updated_at FROM chains WHERE id = ?`)
	var (
		c       Chain
		created string
		updated string
	)
	err := r.db.sql.QueryRowContext(ctx, cq, id).Scan(&c.ID, &c.TenantID, &c.Name, &c.Strategy, &c.FallbackProvider, &c.FallbackModel, &c.InputPerM, &c.OutputPerM, &c.CacheWritePerM, &c.CacheReadPerM, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Chain{}, ErrNotFound
	}
	if err != nil {
		return Chain{}, fmt.Errorf("store: get chain: %w", err)
	}
	c.CreatedAt = parseTime(created)
	c.UpdatedAt = parseTime(updated)

	steps, err := r.steps(ctx, id)
	if err != nil {
		return Chain{}, err
	}
	c.Steps = steps
	return c, nil
}

// ListByTenant returns all chains (with steps) for a tenant.
func (r *ChainRepo) ListByTenant(ctx context.Context, tenantID string) ([]Chain, error) {
	cq := r.db.rebind(`SELECT id, tenant_id, name, strategy, fallback_provider, fallback_model, input_per_m, output_per_m, cache_write_per_m, cache_read_per_m, created_at, updated_at FROM chains WHERE tenant_id = ? ORDER BY created_at DESC`)
	rows, err := r.db.sql.QueryContext(ctx, cq, tenantID)
	if err != nil {
		return nil, fmt.Errorf("store: list chains: %w", err)
	}
	defer rows.Close()

	var out []Chain
	for rows.Next() {
		var (
			c       Chain
			created string
			updated string
		)
		if err := rows.Scan(&c.ID, &c.TenantID, &c.Name, &c.Strategy, &c.FallbackProvider, &c.FallbackModel, &c.InputPerM, &c.OutputPerM, &c.CacheWritePerM, &c.CacheReadPerM, &created, &updated); err != nil {
			return nil, err
		}
		c.CreatedAt = parseTime(created)
		c.UpdatedAt = parseTime(updated)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Attach steps (separate pass to avoid holding two queries open at once,
	// which SQLite's single connection disallows).
	for i := range out {
		steps, err := r.steps(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Steps = steps
	}
	return out, nil
}

// Delete removes a chain (steps cascade via FK).
func (r *ChainRepo) Delete(ctx context.Context, id string) error {
	q := r.db.rebind(`DELETE FROM chains WHERE id = ?`)
	_, err := r.db.sql.ExecContext(ctx, q, id)
	return err
}

// Update replaces a chain's name, strategy, and steps in a transaction.
func (r *ChainRepo) Update(ctx context.Context, c Chain) error {
	tx, err := r.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	uq := r.db.rebind(`UPDATE chains SET name = ?, strategy = ?, fallback_provider = ?, fallback_model = ?,
		input_per_m = ?, output_per_m = ?, cache_write_per_m = ?, cache_read_per_m = ?, updated_at = ? WHERE id = ?`)
	if _, err := tx.ExecContext(ctx, uq, c.Name, c.Strategy, c.FallbackProvider, c.FallbackModel,
		c.InputPerM, c.OutputPerM, c.CacheWritePerM, c.CacheReadPerM, formatTime(time.Now()), c.ID); err != nil {
		return fmt.Errorf("store: update chain: %w", err)
	}

	// Replace steps: delete old, insert new.
	dq := r.db.rebind(`DELETE FROM chain_steps WHERE chain_id = ?`)
	if _, err := tx.ExecContext(ctx, dq, c.ID); err != nil {
		return fmt.Errorf("store: delete chain steps: %w", err)
	}

	sq := r.db.rebind(`INSERT INTO chain_steps (id, chain_id, position, provider, model, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`)
	for _, s := range c.Steps {
		if _, err := tx.ExecContext(ctx, sq, s.ID, c.ID, s.Position, s.Provider, s.Model,
			formatTime(time.Now())); err != nil {
			return fmt.Errorf("store: create chain step: %w", err)
		}
	}
	return tx.Commit()
}

func (r *ChainRepo) steps(ctx context.Context, chainID string) ([]ChainStep, error) {
	q := r.db.rebind(`SELECT id, chain_id, position, provider, model, created_at FROM chain_steps WHERE chain_id = ? ORDER BY position ASC`)
	rows, err := r.db.sql.QueryContext(ctx, q, chainID)
	if err != nil {
		return nil, fmt.Errorf("store: list chain steps: %w", err)
	}
	defer rows.Close()

	var out []ChainStep
	for rows.Next() {
		var (
			s       ChainStep
			created string
		)
		if err := rows.Scan(&s.ID, &s.ChainID, &s.Position, &s.Provider, &s.Model, &created); err != nil {
			return nil, err
		}
		s.CreatedAt = parseTime(created)
		out = append(out, s)
	}
	return out, rows.Err()
}
