package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// ErrAlreadyExists is returned when an insert conflicts with an existing row.
var ErrAlreadyExists = errors.New("store: already exists")

// TenantRepo persists tenants and projects.
type TenantRepo struct{ db *DB }

// Tenants returns the tenant repository.
func (db *DB) Tenants() *TenantRepo { return &TenantRepo{db: db} }

// EnsureDefault creates the implicit default tenant if it does not exist. Called
// once on startup so local single-user mode has a tenant to attach data to.
func (r *TenantRepo) EnsureDefault(ctx context.Context) error {
	return r.Upsert(ctx, Tenant{ID: DefaultTenantID, Name: "Default", CreatedAt: time.Now()})
}

// Upsert inserts a tenant or leaves an existing one untouched.
func (r *TenantRepo) Upsert(ctx context.Context, t Tenant) error {
	q := r.db.rebind(`INSERT INTO tenants (id, name, created_at) VALUES (?, ?, ?)
		ON CONFLICT (id) DO NOTHING`)
	_, err := r.db.sql.ExecContext(ctx, q, t.ID, t.Name, formatTime(t.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: upsert tenant: %w", err)
	}
	return nil
}

// Get returns one tenant by id.
func (r *TenantRepo) Get(ctx context.Context, id string) (Tenant, error) {
	q := r.db.rebind(`SELECT id, name, created_at FROM tenants WHERE id = ?`)
	var (
		t       Tenant
		created string
	)
	err := r.db.sql.QueryRowContext(ctx, q, id).Scan(&t.ID, &t.Name, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Tenant{}, ErrNotFound
	}
	if err != nil {
		return Tenant{}, fmt.Errorf("store: get tenant: %w", err)
	}
	t.CreatedAt = parseTime(created)
	return t, nil
}

// SettingsRepo is a simple key/value configuration store.
type SettingsRepo struct{ db *DB }

// Settings returns the settings repository.
func (db *DB) Settings() *SettingsRepo { return &SettingsRepo{db: db} }

// Get returns a setting value, or ErrNotFound if the key is absent.
func (r *SettingsRepo) Get(ctx context.Context, key string) (string, error) {
	q := r.db.rebind(`SELECT value FROM settings WHERE key = ?`)
	var v string
	err := r.db.sql.QueryRowContext(ctx, q, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: get setting: %w", err)
	}
	return v, nil
}

// Set writes (inserts or updates) a setting value.
func (r *SettingsRepo) Set(ctx context.Context, key, value string) error {
	q := r.db.rebind(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`)
	_, err := r.db.sql.ExecContext(ctx, q, key, value, formatTime(time.Now()))
	if err != nil {
		return fmt.Errorf("store: set setting: %w", err)
	}
	return nil
}

// Delete removes a setting key. Absent keys are not an error.
func (r *SettingsRepo) Delete(ctx context.Context, key string) error {
	q := r.db.rebind(`DELETE FROM settings WHERE key = ?`)
	if _, err := r.db.sql.ExecContext(ctx, q, key); err != nil {
		return fmt.Errorf("store: delete setting: %w", err)
	}
	return nil
}

// InsertIfAbsentOnTx inserts a setting inside an existing transaction and
// returns ErrAlreadyExists when the key is already present. Unlike Set, this
// never overwrites, so it can be used as an exclusive lock: a concurrent
// creator that loses the race gets ErrAlreadyExists instead of clobbering the
// winner's row. The check is the DB's unique constraint, not a read-then-write.
func (r *SettingsRepo) InsertIfAbsentOnTx(ctx context.Context, tx *sql.Tx, key, value string) error {
	q := r.db.rebind(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)`)
	_, err := tx.ExecContext(ctx, q, key, value, formatTime(time.Now()))
	if err != nil {
		if isUniqueViolation(err) {
			return ErrAlreadyExists
		}
		return fmt.Errorf("store: insert setting: %w", err)
	}
	return nil
}

// isUniqueViolation reports whether err is a unique/primary-key constraint
// violation from either supported engine. modernc SQLite reports result codes
// SQLITE_CONSTRAINT_PRIMARYKEY (1555) and SQLITE_CONSTRAINT_UNIQUE (2067);
// Postgres reports SQLSTATE 23505.
func isUniqueViolation(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		code := se.Code()
		return code == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY || code == sqlite3.SQLITE_CONSTRAINT_UNIQUE
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code == "23505"
	}
	return false
}

// AuditRepo appends and reads audit entries. Rows are never updated; the only
// delete is the retention sweep.
type AuditRepo struct{ db *DB }

// Audit returns the audit repository.
func (db *DB) Audit() *AuditRepo { return &AuditRepo{db: db} }

// auditTimeLayout stores audit timestamps with fixed-width microseconds so
// string order matches time order within a second (RFC3339 alone would not).
const auditTimeLayout = "2006-01-02T15:04:05.000000Z07:00"

func formatAuditTime(t time.Time) string { return t.UTC().Format(auditTimeLayout) }

const auditCols = `id, tenant_id, actor, actor_type, action, target, resource_type, outcome,
	severity, client_ip, user_agent, http_method, http_route, status_code, request_id,
	trace_id, span_id, detail, chain_id, chain_seq, prev_hash, hash, created_at`

const auditInsert = `INSERT INTO audit_log (` + auditCols + `)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func auditArgs(e AuditEntry) []any {
	detail := e.Detail
	if detail == "" {
		detail = "{}"
	}
	severity := e.Severity
	if severity == "" {
		severity = "INFO"
	}
	return []any{e.ID, nullString(e.TenantID), e.Actor, e.ActorType, e.Action, nullString(e.Target),
		e.ResourceType, e.Outcome, severity, e.ClientIP, e.UserAgent, e.HTTPMethod, e.HTTPRoute,
		e.StatusCode, e.RequestID, e.TraceID, e.SpanID, detail, e.ChainID, e.ChainSeq,
		e.PrevHash, e.Hash, formatAuditTime(e.CreatedAt)}
}

// Append writes one audit entry.
func (r *AuditRepo) Append(ctx context.Context, e AuditEntry) error {
	if _, err := r.db.sql.ExecContext(ctx, r.db.rebind(auditInsert), auditArgs(e)...); err != nil {
		return fmt.Errorf("store: append audit: %w", err)
	}
	return nil
}

// BatchInsert writes entries in one transaction, in order.
func (r *AuditRepo) BatchInsert(ctx context.Context, entries []AuditEntry) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := r.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, r.db.rebind(auditInsert))
	if err != nil {
		return fmt.Errorf("store: prepare audit: %w", err)
	}
	defer stmt.Close()
	for _, e := range entries {
		if _, err := stmt.ExecContext(ctx, auditArgs(e)...); err != nil {
			return fmt.Errorf("store: batch insert audit: %w", err)
		}
	}
	return tx.Commit()
}

// AuditFilter narrows audit queries. Empty fields are ignored.
type AuditFilter struct {
	// Action matches exactly, or as a prefix when it ends in ".*"
	// ("keys.*" matches keys.create and keys.delete).
	Action       string
	Actor        string
	Outcome      string
	ResourceType string
	Target       string
	// Query is a substring over action, target, actor, route and client IP
	// (LIKE semantics: ASCII case-insensitive on SQLite, exact on Postgres).
	Query string
	Since time.Time
	Until time.Time
	// BeforeTime/BeforeID continue a newest-first page after the last row
	// of the previous page.
	BeforeTime time.Time
	BeforeID   string
	Limit      int
}

// List returns audit entries for a tenant matching f, newest first.
func (r *AuditRepo) List(ctx context.Context, tenantID string, f AuditFilter) ([]AuditEntry, error) {
	clauses := []string{"tenant_id = ?"}
	args := []any{tenantID}
	if f.Action != "" {
		if prefix, ok := strings.CutSuffix(f.Action, ".*"); ok {
			clauses = append(clauses, `action LIKE ? ESCAPE '\'`)
			args = append(args, escapeLike(prefix)+".%")
		} else {
			clauses = append(clauses, "action = ?")
			args = append(args, f.Action)
		}
	}
	for _, c := range []struct{ col, val string }{
		{"actor", f.Actor}, {"outcome", f.Outcome}, {"resource_type", f.ResourceType}, {"target", f.Target},
	} {
		if c.val != "" {
			clauses = append(clauses, c.col+" = ?")
			args = append(args, c.val)
		}
	}
	if f.Query != "" {
		like := "%" + escapeLike(f.Query) + "%"
		clauses = append(clauses, `(action LIKE ? ESCAPE '\' OR target LIKE ? ESCAPE '\' OR actor LIKE ? ESCAPE '\'
			OR http_route LIKE ? ESCAPE '\' OR client_ip LIKE ? ESCAPE '\')`)
		args = append(args, like, like, like, like, like)
	}
	if !f.Since.IsZero() {
		clauses = append(clauses, "created_at >= ?")
		args = append(args, formatAuditTime(f.Since))
	}
	if !f.Until.IsZero() {
		clauses = append(clauses, "created_at < ?")
		args = append(args, formatAuditTime(f.Until))
	}
	if !f.BeforeTime.IsZero() {
		ts := formatAuditTime(f.BeforeTime)
		clauses = append(clauses, "(created_at < ? OR (created_at = ? AND id < ?))")
		args = append(args, ts, ts, f.BeforeID)
	}
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := r.db.rebind(`SELECT ` + auditCols + ` FROM audit_log WHERE ` + strings.Join(clauses, " AND ") +
		` ORDER BY created_at DESC, id DESC LIMIT ` + strconv.Itoa(limit))
	return r.query(ctx, q, args...)
}

// Chain returns up to limit rows of one writer chain with chain_seq > afterSeq,
// in chain order. Used to verify the hash chain.
func (r *AuditRepo) Chain(ctx context.Context, chainID string, afterSeq int64, limit int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 500
	}
	q := r.db.rebind(`SELECT ` + auditCols + ` FROM audit_log WHERE chain_id = ? AND chain_seq > ?
		ORDER BY chain_seq LIMIT ` + strconv.Itoa(limit))
	return r.query(ctx, q, chainID, afterSeq)
}

// ChainIDs lists every writer chain present in the log.
func (r *AuditRepo) ChainIDs(ctx context.Context) ([]string, error) {
	rows, err := r.db.sql.QueryContext(ctx, `SELECT DISTINCT chain_id FROM audit_log WHERE chain_id <> ''`)
	if err != nil {
		return nil, fmt.Errorf("store: list audit chains: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// DeleteOlderThan removes entries created before cutoff and reports how many.
func (r *AuditRepo) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := r.db.sql.ExecContext(ctx, r.db.rebind(`DELETE FROM audit_log WHERE created_at < ?`), formatAuditTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("store: delete audit: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (r *AuditRepo) query(ctx context.Context, q string, args ...any) ([]AuditEntry, error) {
	rows, err := r.db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list audit: %w", err)
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var (
			e         AuditEntry
			tenant    sql.NullString
			target    sql.NullString
			createdAt string
		)
		if err := rows.Scan(&e.ID, &tenant, &e.Actor, &e.ActorType, &e.Action, &target, &e.ResourceType,
			&e.Outcome, &e.Severity, &e.ClientIP, &e.UserAgent, &e.HTTPMethod, &e.HTTPRoute, &e.StatusCode,
			&e.RequestID, &e.TraceID, &e.SpanID, &e.Detail, &e.ChainID, &e.ChainSeq, &e.PrevHash, &e.Hash,
			&createdAt); err != nil {
			return nil, fmt.Errorf("store: scan audit: %w", err)
		}
		e.TenantID = tenant.String
		e.Target = target.String
		e.CreatedAt = parseTime(createdAt)
		out = append(out, e)
	}
	return out, rows.Err()
}

// escapeLike escapes LIKE wildcards in user input for an ESCAPE '\' clause.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s)
}
