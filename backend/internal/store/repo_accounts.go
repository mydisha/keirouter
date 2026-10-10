package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AccountRepo persists upstream provider accounts and their encrypted secrets.
type AccountRepo struct{ db *DB }

// Accounts returns the account repository.
func (db *DB) Accounts() *AccountRepo { return &AccountRepo{db: db} }

const accountColumns = `id, tenant_id, provider, label, auth_kind,
	secret_wrapped_dek, secret_ciphertext,
	token_wrapped_dek, token_ciphertext,
	refresh_wrapped_dek, refresh_ciphertext,
	token_expires_at, metadata, priority, backoff_level, disabled, cooldown_until,
	proxy_pool_id, needs_reconnect, credits_exhausted, created_at, updated_at`

// SetBackoffLevel updates the exponential backoff level for an account.
func (r *AccountRepo) SetBackoffLevel(ctx context.Context, id string, level int) error {
	q := r.db.rebind(`UPDATE accounts SET backoff_level = ?, updated_at = ? WHERE id = ?`)
	_, err := r.db.sql.ExecContext(ctx, q, level, formatTime(time.Now()), id)
	return err
}

// ResetBackoffLevel resets the exponential backoff level to 0 and clears
// cooldown and the credits-exhausted flag, called on a successful request
// (a success proves the balance covers traffic again).
func (r *AccountRepo) ResetBackoffLevel(ctx context.Context, id string) error {
	q := r.db.rebind(`UPDATE accounts SET backoff_level = 0, cooldown_until = NULL, credits_exhausted = 0, updated_at = ? WHERE id = ?`)
	_, err := r.db.sql.ExecContext(ctx, q, formatTime(time.Now()), id)
	return err
}

// Create inserts a new account.
func (r *AccountRepo) Create(ctx context.Context, a Account) error {
	q := r.db.rebind(`INSERT INTO accounts (` + accountColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.sql.ExecContext(ctx, q,
		a.ID, a.TenantID, a.Provider, a.Label, string(a.AuthKind),
		nullString(a.SecretWrappedDEK), nullString(a.SecretCiphertext),
		nullString(a.TokenWrappedDEK), nullString(a.TokenCiphertext),
		nullString(a.RefreshWrappedDEK), nullString(a.RefreshCiphertext),
		nullTime(a.TokenExpiresAt), a.Metadata, a.Priority, a.BackoffLevel,
		boolToInt(a.Disabled), nullTime(a.CooldownUntil), a.ProxyPoolID,
		boolToInt(a.NeedsReconnect), boolToInt(a.CreditsExhausted),
		formatTime(a.CreatedAt), formatTime(a.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store: create account: %w", err)
	}
	return nil
}

// Get returns one account by id.
func (r *AccountRepo) Get(ctx context.Context, id string) (Account, error) {
	q := r.db.rebind(`SELECT ` + accountColumns + ` FROM accounts WHERE id = ?`)
	row := r.db.sql.QueryRowContext(ctx, q, id)
	a, err := scanAccountRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return a, err
}

// ListByProvider returns enabled accounts for a provider within a tenant,
// ordered by ascending priority (lower number = higher priority).
func (r *AccountRepo) ListByProvider(ctx context.Context, tenantID, provider string) ([]Account, error) {
	q := r.db.rebind(`SELECT ` + accountColumns + `
		FROM accounts
		WHERE tenant_id = ? AND provider = ? AND disabled = 0
		ORDER BY priority ASC, created_at ASC`)
	return r.queryList(ctx, q, tenantID, provider)
}

// ListByProviders returns enabled accounts for multiple providers in one query.
// Providers are de-duplicated before building the IN clause so fallback chains
// with several models on the same provider do not add redundant placeholders.
func (r *AccountRepo) ListByProviders(ctx context.Context, tenantID string, providers []string) ([]Account, error) {
	unique := make([]string, 0, len(providers))
	seen := make(map[string]struct{}, len(providers))
	for _, provider := range providers {
		if provider == "" {
			continue
		}
		if _, ok := seen[provider]; ok {
			continue
		}
		seen[provider] = struct{}{}
		unique = append(unique, provider)
	}
	if len(unique) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(unique))
	args := make([]any, 0, len(unique)+1)
	args = append(args, tenantID)
	for i, provider := range unique {
		placeholders[i] = "?"
		args = append(args, provider)
	}

	q := r.db.rebind(`SELECT ` + accountColumns + `
		FROM accounts
		WHERE tenant_id = ? AND provider IN (` + strings.Join(placeholders, ", ") + `) AND disabled = 0
		ORDER BY provider ASC, priority ASC, created_at ASC`)
	return r.queryList(ctx, q, args...)
}

// ListByTenant returns all accounts for a tenant.
func (r *AccountRepo) ListByTenant(ctx context.Context, tenantID string) ([]Account, error) {
	q := r.db.rebind(`SELECT ` + accountColumns + `
		FROM accounts WHERE tenant_id = ? ORDER BY provider, priority ASC`)
	return r.queryList(ctx, q, tenantID)
}

// SetCooldown marks an account unavailable until the given time (used by the
// dispatcher after a rate-limit / quota error).
func (r *AccountRepo) SetCooldown(ctx context.Context, id string, until time.Time) error {
	q := r.db.rebind(`UPDATE accounts SET cooldown_until = ?, updated_at = ? WHERE id = ?`)
	_, err := r.db.sql.ExecContext(ctx, q, formatTime(until), formatTime(time.Now()), id)
	return err
}

// UpdateTokens replaces the sealed OAuth tokens and expiry after a refresh.
// It also clears needs_reconnect since a successful refresh means the token is
// valid again.
func (r *AccountRepo) UpdateTokens(ctx context.Context, a Account) error {
	q := r.db.rebind(`UPDATE accounts SET
		token_wrapped_dek = ?, token_ciphertext = ?,
		refresh_wrapped_dek = ?, refresh_ciphertext = ?,
		token_expires_at = ?, needs_reconnect = 0, updated_at = ?
		WHERE id = ?`)
	_, err := r.db.sql.ExecContext(ctx, q,
		nullString(a.TokenWrappedDEK), nullString(a.TokenCiphertext),
		nullString(a.RefreshWrappedDEK), nullString(a.RefreshCiphertext),
		nullTime(a.TokenExpiresAt), formatTime(time.Now()), a.ID)
	return err
}

// Update writes mutable fields (label, priority, disabled, proxy_pool_id).
func (r *AccountRepo) Update(ctx context.Context, a Account) error {
	q := r.db.rebind(`UPDATE accounts SET label = ?, priority = ?, disabled = ?,
		proxy_pool_id = ?, updated_at = ? WHERE id = ?`)
	_, err := r.db.sql.ExecContext(ctx, q,
		a.Label, a.Priority, boolToInt(a.Disabled),
		a.ProxyPoolID, formatTime(time.Now()), a.ID)
	return err
}

// ClearExpiredCooldowns resets cooldown and backoff for all accounts whose
// cooldown has already expired. Called on startup so stale cooldowns from a
// previous session don't block fresh requests.
func (r *AccountRepo) ClearExpiredCooldowns(ctx context.Context) (int64, error) {
	q := r.db.rebind(`UPDATE accounts SET cooldown_until = NULL, backoff_level = 0, credits_exhausted = 0, updated_at = ? WHERE cooldown_until IS NOT NULL AND cooldown_until < ?`)
	res, err := r.db.sql.ExecContext(ctx, q, formatTime(time.Now()), formatTime(time.Now()))
	if err != nil {
		return 0, fmt.Errorf("store: clear expired cooldowns: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ClearProviderCooldowns clears cooldown and backoff for all accounts of a
// given provider within a tenant. Called when a user reconnects a provider so
// stale cooldowns from a previous session don't block the fresh account.
// Also clears needs_reconnect since a successful reconnect means the provider
// is accessible again.
func (r *AccountRepo) ClearProviderCooldowns(ctx context.Context, tenantID, provider string) error {
	q := r.db.rebind(`UPDATE accounts SET cooldown_until = NULL, backoff_level = 0,
		needs_reconnect = 0, credits_exhausted = 0, updated_at = ?
		WHERE tenant_id = ? AND provider = ? AND (cooldown_until IS NOT NULL OR needs_reconnect != 0 OR credits_exhausted != 0)`)
	_, err := r.db.sql.ExecContext(ctx, q, formatTime(time.Now()), tenantID, provider)
	if err != nil {
		return fmt.Errorf("store: clear provider cooldowns: %w", err)
	}
	return nil
}

// Delete removes an account.
// DisableByProvider marks every account bound to the given provider as
// disabled and clears any active cooldown. It is used when a custom provider
// is deleted so leftover credentials cannot be selected for routing and the
// dashboard reflects the severed connection. Returns the number of accounts
// touched. Already-disabled accounts are included so the count reflects total
// rows matched, but only previously-enabled ones change state.
func (r *AccountRepo) DisableByProvider(ctx context.Context, tenantID, provider string) (int64, error) {
	q := r.db.rebind(`UPDATE accounts SET disabled = 1, cooldown_until = NULL WHERE tenant_id = ? AND provider = ?`)
	res, err := r.db.sql.ExecContext(ctx, q, tenantID, provider)
	if err != nil {
		return 0, fmt.Errorf("store: disable accounts by provider: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (r *AccountRepo) Delete(ctx context.Context, id string) error {
	q := r.db.rebind(`DELETE FROM accounts WHERE id = ?`)
	_, err := r.db.sql.ExecContext(ctx, q, id)
	return err
}

// SetNeedsReconnect marks an account as needing re-authentication because its
// OAuth refresh token was permanently rejected by the upstream. When flag is
// true the dispatcher will skip the account; the dashboard shows a "Reconnect"
// badge.
func (r *AccountRepo) SetNeedsReconnect(ctx context.Context, id string, flag bool) error {
	q := r.db.rebind(`UPDATE accounts SET needs_reconnect = ?, updated_at = ? WHERE id = ?`)
	_, err := r.db.sql.ExecContext(ctx, q, boolToInt(flag), formatTime(time.Now()), id)
	return err
}

// SetCreditsExhausted marks an account's paid balance as depleted (or clears
// the flag). Set by the dispatcher when a provider reports the balance is
// dry; cleared by a successful request, a provider reconnect, or expiry of
// the parking cooldown.
func (r *AccountRepo) SetCreditsExhausted(ctx context.Context, id string, flag bool) error {
	q := r.db.rebind(`UPDATE accounts SET credits_exhausted = ?, updated_at = ? WHERE id = ?`)
	_, err := r.db.sql.ExecContext(ctx, q, boolToInt(flag), formatTime(time.Now()), id)
	return err
}

func (r *AccountRepo) queryList(ctx context.Context, q string, args ...any) ([]Account, error) {
	rows, err := r.db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list accounts: %w", err)
	}
	defer rows.Close()

	var out []Account
	for rows.Next() {
		a, err := scanAccountRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// scanAccountRow scans an account from either a *sql.Row or *sql.Rows via the
// shared Scan signature.
func scanAccountRow(scan func(dest ...any) error) (Account, error) {
	var (
		a                Account
		authKind         string
		secretDEK        sql.NullString
		secretCT         sql.NullString
		tokenDEK         sql.NullString
		tokenCT          sql.NullString
		refreshDEK       sql.NullString
		refreshCT        sql.NullString
		tokenExpires     sql.NullString
		backoffLevel     int
		cooldown         sql.NullString
		disabled         int
		proxyPoolID      sql.NullString
		needsReconnect   int
		creditsExhausted int
		createdRaw       string
		updatedRaw       string
	)
	err := scan(
		&a.ID, &a.TenantID, &a.Provider, &a.Label, &authKind,
		&secretDEK, &secretCT, &tokenDEK, &tokenCT, &refreshDEK, &refreshCT,
		&tokenExpires, &a.Metadata, &a.Priority, &backoffLevel, &disabled, &cooldown,
		&proxyPoolID, &needsReconnect, &creditsExhausted, &createdRaw, &updatedRaw,
	)
	if err != nil {
		return Account{}, err
	}
	a.AuthKind = AuthKind(authKind)
	a.SecretWrappedDEK = secretDEK.String
	a.SecretCiphertext = secretCT.String
	a.TokenWrappedDEK = tokenDEK.String
	a.TokenCiphertext = tokenCT.String
	a.RefreshWrappedDEK = refreshDEK.String
	a.RefreshCiphertext = refreshCT.String
	a.BackoffLevel = backoffLevel
	a.Disabled = disabled != 0
	a.ProxyPoolID = proxyPoolID.String
	a.NeedsReconnect = needsReconnect != 0
	a.CreditsExhausted = creditsExhausted != 0
	a.CreatedAt = parseTime(createdRaw)
	a.UpdatedAt = parseTime(updatedRaw)
	if tokenExpires.Valid {
		t := parseTime(tokenExpires.String)
		a.TokenExpiresAt = &t
	}
	if cooldown.Valid {
		t := parseTime(cooldown.String)
		a.CooldownUntil = &t
	}
	return a, nil
}
