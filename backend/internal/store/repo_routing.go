package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// RoutingRepo handles model-level cooldowns and chain rotation state.
type RoutingRepo struct{ db *DB }

// Routing returns the routing repository.
func (db *DB) Routing() *RoutingRepo { return &RoutingRepo{db: db} }

// Model cooldowns -----------------------------------------------------------

// SetModelCooldown inserts or updates a model-level cooldown on an account.
// While active, the dispatch layer skips this account for the given model
// but still allows it for other models.
func (r *RoutingRepo) SetModelCooldown(ctx context.Context, accountID, model string, until time.Time) error {
	id := randHex(16)
	q := r.db.rebind(`INSERT INTO model_cooldowns (id, account_id, model, cooldown_until, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(account_id, model) DO UPDATE SET cooldown_until = excluded.cooldown_until`)
	_, err := r.db.sql.ExecContext(ctx, q, id, accountID, model, formatTime(until), formatTime(time.Now()))
	r.db.cooldownCache.invalidateAll()
	if err != nil {
		return fmt.Errorf("store: set model cooldown: %w", err)
	}
	return nil
}

// ClearModelCooldown removes a model cooldown (on success).
func (r *RoutingRepo) ClearModelCooldown(ctx context.Context, accountID, model string) error {
	q := r.db.rebind(`DELETE FROM model_cooldowns WHERE account_id = ? AND model = ?`)
	_, err := r.db.sql.ExecContext(ctx, q, accountID, model)
	r.db.cooldownCache.invalidateAll()
	return err
}

// ClearAccountModelCooldowns removes all model cooldowns for an account.
func (r *RoutingRepo) ClearAccountModelCooldowns(ctx context.Context, accountID string) error {
	q := r.db.rebind(`DELETE FROM model_cooldowns WHERE account_id = ?`)
	_, err := r.db.sql.ExecContext(ctx, q, accountID)
	r.db.cooldownCache.invalidateAll()
	return err
}

// IsModelCooldownActive checks if a specific model on an account is still
// locked. Returns true when the cooldown has not yet expired.
func (r *RoutingRepo) IsModelCooldownActive(ctx context.Context, accountID, model string) (bool, error) {
	q := r.db.rebind(`SELECT cooldown_until FROM model_cooldowns
		WHERE account_id = ? AND model = ?
		UNION ALL
		SELECT cooldown_until FROM model_cooldowns
		WHERE account_id = ? AND model = '__all__'`)
	rows, err := r.db.sql.QueryContext(ctx, q, accountID, model, accountID)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	now := time.Now()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		t := parseTime(raw)
		if t.After(now) {
			return true, nil
		}
	}
	return false, rows.Err()
}

// ActiveCooldowns returns the set of account IDs (from accountIDs) that have an
// active model-level cooldown for model or '__all__' at the current time. It
// batches what would otherwise be one IsModelCooldownActive query per account
// into a single query per target.
func (r *RoutingRepo) ActiveCooldowns(ctx context.Context, accountIDs []string, model string) (map[string]bool, error) {
	out := make(map[string]bool)
	if len(accountIDs) == 0 {
		return out, nil
	}

	placeholders := make([]string, len(accountIDs))
	args := make([]any, 0, len(accountIDs)+3)
	for i, id := range accountIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, model, "__all__", formatTime(time.Now()))

	q := r.db.rebind(`SELECT DISTINCT account_id FROM model_cooldowns
		WHERE account_id IN (` + strings.Join(placeholders, ",") + `)
		  AND model IN (?, ?)
		  AND cooldown_until > ?`)

	rows, err := r.db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: active model cooldowns: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		out[id] = true
	}
	return out, rows.Err()
}

// ActiveCooldownExpirations returns the latest active model cooldown per
// account for model or '__all__'. It is used to calculate the earliest safe
// retry instead of polling with fixed sleeps. The planner calls it for every
// target of every request, so results are cached briefly; cooldown writes
// through this DB invalidate the cache, and expiry is re-checked on each hit.
func (r *RoutingRepo) ActiveCooldownExpirations(ctx context.Context, accountIDs []string, model string) (map[string]time.Time, error) {
	if len(accountIDs) == 0 {
		return make(map[string]time.Time), nil
	}
	key := routingCacheKey(model, accountIDs)
	now := time.Now()
	cached, hit, gen := r.db.cooldownCache.get(key, now)
	if !hit {
		var err error
		if cached, err = r.queryCooldownExpirations(ctx, accountIDs, model); err != nil {
			return nil, err
		}
		r.db.cooldownCache.put(key, cached, gen, now)
	}
	// A fresh map: callers may modify it, and the cached one is shared.
	out := make(map[string]time.Time, len(cached))
	for id, until := range cached {
		if until.After(now) {
			out[id] = until
		}
	}
	return out, nil
}

func (r *RoutingRepo) queryCooldownExpirations(ctx context.Context, accountIDs []string, model string) (map[string]time.Time, error) {
	out := make(map[string]time.Time)

	placeholders := make([]string, len(accountIDs))
	args := make([]any, 0, len(accountIDs)+3)
	for i, id := range accountIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, model, "__all__", formatTime(time.Now()))

	q := r.db.rebind(`SELECT account_id, MAX(cooldown_until) FROM model_cooldowns
		WHERE account_id IN (` + strings.Join(placeholders, ",") + `)
		  AND model IN (?, ?)
		  AND cooldown_until > ?
		GROUP BY account_id`)

	rows, err := r.db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: active model cooldown expirations: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			continue
		}
		if until := parseTime(raw); !until.IsZero() {
			out[id] = until
		}
	}
	return out, rows.Err()
}

// ExpireModelCooldowns removes all expired model cooldowns (garbage collection).
func (r *RoutingRepo) ExpireModelCooldowns(ctx context.Context) (int64, error) {
	q := r.db.rebind(`DELETE FROM model_cooldowns WHERE cooldown_until < ?`)
	res, err := r.db.sql.ExecContext(ctx, q, formatTime(time.Now()))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Chain rotation ------------------------------------------------------------

// GetChainRotation returns the persisted round-robin index for a chain.
// Returns 0 if no rotation state exists yet.
func (r *RoutingRepo) GetChainRotation(ctx context.Context, chainID string) (int, error) {
	state, err := r.GetChainRotationState(ctx, chainID)
	return state.LastIndex, err
}

// SetChainRotation persists the round-robin cursor for a chain.
func (r *RoutingRepo) SetChainRotation(ctx context.Context, chainID string, index int) error {
	return r.SetChainRotationState(ctx, ChainRotation{ChainID: chainID, LastIndex: index})
}

// GetChainRotationState returns the persisted round-robin cursor and sticky
// hit count for a chain. Missing state is treated as a zero cursor.
func (r *RoutingRepo) GetChainRotationState(ctx context.Context, chainID string) (ChainRotation, error) {
	q := r.db.rebind(`SELECT chain_id, last_index, hit_count, updated_at FROM chain_rotation WHERE chain_id = ?`)
	var state ChainRotation
	var updated string
	err := r.db.sql.QueryRowContext(ctx, q, chainID).Scan(&state.ChainID, &state.LastIndex, &state.HitCount, &updated)
	if err == sql.ErrNoRows {
		return ChainRotation{ChainID: chainID}, nil
	}
	if err != nil {
		return ChainRotation{}, fmt.Errorf("store: get chain rotation: %w", err)
	}
	state.UpdatedAt = parseTime(updated)
	return state, nil
}

// SetChainRotationState persists the round-robin cursor and sticky hit count
// for a chain.
func (r *RoutingRepo) SetChainRotationState(ctx context.Context, state ChainRotation) error {
	q := r.db.rebind(`INSERT INTO chain_rotation (chain_id, last_index, hit_count, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(chain_id) DO UPDATE SET
			last_index = excluded.last_index,
			hit_count = excluded.hit_count,
			updated_at = excluded.updated_at`)
	_, err := r.db.sql.ExecContext(ctx, q, state.ChainID, state.LastIndex, state.HitCount, formatTime(time.Now()))
	if err != nil {
		return fmt.Errorf("store: set chain rotation: %w", err)
	}
	return nil
}

// GetTargetRotationState returns the persisted round-robin cursor and sticky
// hit count for a provider/model target. Missing state is treated as zero.
func (r *RoutingRepo) GetTargetRotationState(ctx context.Context, scopeKey string) (TargetRotation, error) {
	q := r.db.rebind(`SELECT scope_key, last_index, hit_count, updated_at FROM target_rotation WHERE scope_key = ?`)
	var state TargetRotation
	var updated string
	err := r.db.sql.QueryRowContext(ctx, q, scopeKey).Scan(&state.ScopeKey, &state.LastIndex, &state.HitCount, &updated)
	if err == sql.ErrNoRows {
		return TargetRotation{ScopeKey: scopeKey}, nil
	}
	if err != nil {
		return TargetRotation{}, fmt.Errorf("store: get target rotation: %w", err)
	}
	state.UpdatedAt = parseTime(updated)
	return state, nil
}

// SetTargetRotationState persists the round-robin cursor and sticky hit count
// for a provider/model target.
func (r *RoutingRepo) SetTargetRotationState(ctx context.Context, state TargetRotation) error {
	q := r.db.rebind(`INSERT INTO target_rotation (scope_key, last_index, hit_count, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(scope_key) DO UPDATE SET
			last_index = excluded.last_index,
			hit_count = excluded.hit_count,
			updated_at = excluded.updated_at`)
	_, err := r.db.sql.ExecContext(ctx, q, state.ScopeKey, state.LastIndex, state.HitCount, formatTime(time.Now()))
	if err != nil {
		return fmt.Errorf("store: set target rotation: %w", err)
	}
	return nil
}

// Account affinity ----------------------------------------------------------

// GetAccountAffinity returns the account pinned to a routing affinity key.
// Missing rows are treated as empty affinity.
func (r *RoutingRepo) GetAccountAffinity(ctx context.Context, scopeKey string) (AccountAffinity, error) {
	q := r.db.rebind(`SELECT scope_key, account_id, expires_at, updated_at FROM account_affinity WHERE scope_key = ?`)
	var state AccountAffinity
	var expires string
	var updated string
	err := r.db.sql.QueryRowContext(ctx, q, scopeKey).Scan(&state.ScopeKey, &state.AccountID, &expires, &updated)
	if err == sql.ErrNoRows {
		return AccountAffinity{ScopeKey: scopeKey}, nil
	}
	if err != nil {
		return AccountAffinity{}, fmt.Errorf("store: get account affinity: %w", err)
	}
	state.ExpiresAt = parseTime(expires)
	state.UpdatedAt = parseTime(updated)
	return state, nil
}

// SetAccountAffinity pins an affinity key to an account until ExpiresAt.
func (r *RoutingRepo) SetAccountAffinity(ctx context.Context, state AccountAffinity) error {
	q := r.db.rebind(`INSERT INTO account_affinity (scope_key, account_id, expires_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(scope_key) DO UPDATE SET
			account_id = excluded.account_id,
			expires_at = excluded.expires_at,
			updated_at = excluded.updated_at`)
	_, err := r.db.sql.ExecContext(ctx, q, state.ScopeKey, state.AccountID, formatTime(state.ExpiresAt), formatTime(time.Now()))
	if err != nil {
		return fmt.Errorf("store: set account affinity: %w", err)
	}
	return nil
}

// ExpireAccountAffinities removes expired smart-routing pins.
func (r *RoutingRepo) ExpireAccountAffinities(ctx context.Context) (int64, error) {
	q := r.db.rebind(`DELETE FROM account_affinity WHERE expires_at < ?`)
	res, err := r.db.sql.ExecContext(ctx, q, formatTime(time.Now()))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// randHex generates a random hex string of the given byte length.
func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
