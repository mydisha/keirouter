package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mydisha/keirouter/backend/internal/crypto"
)

// HostingStatus is the lifecycle state of a hosted 9router instance.
type HostingStatus string

const (
	HostingAwaitingPayment HostingStatus = "awaiting_payment"
	HostingPaid            HostingStatus = "paid" // paid, waiting for an admin to create it
	HostingProvisioning    HostingStatus = "provisioning"
	HostingActive          HostingStatus = "active"
	HostingFailed          HostingStatus = "failed"
	HostingExpired         HostingStatus = "expired"
	HostingStopped         HostingStatus = "stopped"
	HostingDeleted         HostingStatus = "deleted"
)

// HostingInstance is one customer's hosted 9router container on Dokploy.
// PaidUntil is empty until the first payment settles.
type HostingInstance struct {
	ID              string
	TenantID        string
	KeyID           string
	Status          HostingStatus
	Image           string
	Subdomain       string
	FQDN            string
	DokployAppID    string
	DokployAppName  string
	DokployDomainID string
	// DataVolume is the Docker volume mounted at the data dir; set once the
	// mount exists so a retry never mounts twice.
	DataVolume string
	// NodeID is the hosting node (Dokploy server) the instance runs on;
	// empty until it is first provisioned.
	NodeID string
	// Region is the region the customer bought it in; empty for instances
	// from before regions.
	Region      string
	LastError   string
	PaidUntil   time.Time
	PrepaidDays int // paid days not yet started; they start at provisioning
	// Password is the sealed initial 9router dashboard password.
	Password      crypto.Sealed
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ProvisionedAt time.Time
}

// ErrHostingConflict means the instance row changed between read and update.
var ErrHostingConflict = errors.New("store: hosting instance changed concurrently")

// HostingRepo persists hosting instances.
type HostingRepo struct{ db *DB }

// Hosting returns the hosting instance repository.
func (db *DB) Hosting() *HostingRepo { return &HostingRepo{db: db} }

const hostingCols = `id, tenant_id, key_id, status, image, subdomain, fqdn,
	dokploy_app_id, dokploy_app_name, dokploy_domain_id, data_volume, last_error, paid_until,
	prepaid_days, password_wrapped_dek, password_ciphertext,
	created_at, updated_at, provisioned_at, node_id, region`

// Create inserts an instance.
func (r *HostingRepo) Create(ctx context.Context, h HostingInstance) error {
	return r.insert(ctx, r.db.sql, h)
}

// CreateOnTx inserts an instance within a transaction.
func (r *HostingRepo) CreateOnTx(ctx context.Context, tx *sql.Tx, h HostingInstance) error {
	return r.insert(ctx, tx, h)
}

func (r *HostingRepo) insert(ctx context.Context, ex sqlExec, h HostingInstance) error {
	q := r.db.rebind(`INSERT INTO hosting_instances (` + hostingCols + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := ex.ExecContext(ctx, q,
		h.ID, h.TenantID, h.KeyID, string(h.Status), h.Image, h.Subdomain, h.FQDN,
		h.DokployAppID, h.DokployAppName, h.DokployDomainID, h.DataVolume, h.LastError,
		formatTime(h.PaidUntil), h.PrepaidDays, h.Password.WrappedDEK, h.Password.Ciphertext, formatTime(h.CreatedAt), formatTime(h.UpdatedAt),
		formatTime(h.ProvisionedAt), h.NodeID, h.Region)
	if err != nil {
		return fmt.Errorf("store: create hosting instance: %w", err)
	}
	return nil
}

// Get returns one instance by id.
func (r *HostingRepo) Get(ctx context.Context, id string) (HostingInstance, error) {
	q := r.db.rebind(`SELECT ` + hostingCols + ` FROM hosting_instances WHERE id = ?`)
	return r.getOne(r.db.sql.QueryRowContext(ctx, q, id).Scan)
}

// GetOnTx returns one instance by id within a transaction.
func (r *HostingRepo) GetOnTx(ctx context.Context, tx *sql.Tx, id string) (HostingInstance, error) {
	q := r.db.rebind(`SELECT ` + hostingCols + ` FROM hosting_instances WHERE id = ?`)
	return r.getOne(tx.QueryRowContext(ctx, q, id).Scan)
}

func (r *HostingRepo) getOne(scan func(dest ...any) error) (HostingInstance, error) {
	h, err := scanHosting(scan)
	if errors.Is(err, sql.ErrNoRows) {
		return HostingInstance{}, ErrNotFound
	}
	return h, err
}

// ListByKey returns a key's instances, newest first. Deleted ones are hidden.
func (r *HostingRepo) ListByKey(ctx context.Context, keyID string) ([]HostingInstance, error) {
	q := r.db.rebind(`SELECT ` + hostingCols + ` FROM hosting_instances
		WHERE key_id = ? AND status <> ? ORDER BY created_at DESC, id DESC`)
	return r.queryList(ctx, q, keyID, string(HostingDeleted))
}

// ListAll returns every instance, newest first. An empty status lists all.
func (r *HostingRepo) ListAll(ctx context.Context, status HostingStatus) ([]HostingInstance, error) {
	if status == "" {
		q := r.db.rebind(`SELECT ` + hostingCols + ` FROM hosting_instances ORDER BY created_at DESC, id DESC`)
		return r.queryList(ctx, q)
	}
	q := r.db.rebind(`SELECT ` + hostingCols + ` FROM hosting_instances WHERE status = ? ORDER BY created_at DESC, id DESC`)
	return r.queryList(ctx, q, string(status))
}

// Transition conditionally moves an instance between statuses and reports
// whether it applied. lastError replaces the stored error.
func (r *HostingRepo) Transition(ctx context.Context, id string, from []HostingStatus, to HostingStatus, lastError string) (bool, error) {
	return r.transition(ctx, r.db.sql, id, from, to, lastError)
}

// TransitionOnTx is Transition within a transaction.
func (r *HostingRepo) TransitionOnTx(ctx context.Context, tx *sql.Tx, id string, from []HostingStatus, to HostingStatus, lastError string) (bool, error) {
	return r.transition(ctx, tx, id, from, to, lastError)
}

func (r *HostingRepo) transition(ctx context.Context, ex sqlExec, id string, from []HostingStatus, to HostingStatus, lastError string) (bool, error) {
	if len(from) == 0 {
		return false, fmt.Errorf("store: hosting transition needs a from status")
	}
	args := []any{string(to), lastError, formatTime(time.Now()), id}
	in := ""
	for i, f := range from {
		if i > 0 {
			in += ", "
		}
		in += "?"
		args = append(args, string(f))
	}
	q := r.db.rebind(`UPDATE hosting_instances SET status = ?, last_error = ?, updated_at = ?
		WHERE id = ? AND status IN (` + in + `)`)
	res, err := ex.ExecContext(ctx, q, args...)
	if err != nil {
		return false, fmt.Errorf("store: transition hosting instance: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: transition hosting rows affected: %w", err)
	}
	return n == 1, nil
}

// SetDokploy stores the Dokploy-side identifiers and image as provisioning
// progresses, so a retry resumes from the first missing step.
func (r *HostingRepo) SetDokploy(ctx context.Context, h HostingInstance) error {
	q := r.db.rebind(`UPDATE hosting_instances SET image = ?, subdomain = ?, fqdn = ?,
		dokploy_app_id = ?, dokploy_app_name = ?, dokploy_domain_id = ?, data_volume = ?,
		password_wrapped_dek = ?, password_ciphertext = ?, node_id = ?, updated_at = ?
		WHERE id = ?`)
	_, err := r.db.sql.ExecContext(ctx, q, h.Image, h.Subdomain, h.FQDN,
		h.DokployAppID, h.DokployAppName, h.DokployDomainID, h.DataVolume,
		h.Password.WrappedDEK, h.Password.Ciphertext, h.NodeID, formatTime(time.Now()), h.ID)
	if err != nil {
		return fmt.Errorf("store: set hosting dokploy ids: %w", err)
	}
	return nil
}

// MarkProvisioned moves a provisioning instance to active. On the first
// provisioning the prepaid days start now and move into paid_until.
func (r *HostingRepo) MarkProvisioned(ctx context.Context, h HostingInstance, at time.Time) (bool, error) {
	until := h.PaidUntil
	if h.PrepaidDays > 0 {
		base := at
		if until.After(base) {
			base = until
		}
		until = base.AddDate(0, 0, h.PrepaidDays)
	}
	provisioned := h.ProvisionedAt
	if provisioned.IsZero() {
		provisioned = at
	}
	q := r.db.rebind(`UPDATE hosting_instances SET status = ?, last_error = '', provisioned_at = ?,
		paid_until = ?, prepaid_days = prepaid_days - ?, updated_at = ?
		WHERE id = ? AND status = ? AND prepaid_days = ?`)
	res, err := r.db.sql.ExecContext(ctx, q, string(HostingActive), formatTime(provisioned),
		formatTime(until), h.PrepaidDays, formatTime(time.Now()), h.ID, string(HostingProvisioning), h.PrepaidDays)
	if err != nil {
		return false, fmt.Errorf("store: mark hosting provisioned: %w", err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ApplyPaymentOnTx records one paid period. A provisioned instance extends
// paid_until to newUntil; an unprovisioned one adds days to prepaid_days. The
// update only applies if the row still holds prevUntil, prevPrepaid and the
// status that was read, so a concurrent payment or admin action (such as a
// delete) fails the update instead of being lost; the caller retries.
func (r *HostingRepo) ApplyPaymentOnTx(ctx context.Context, tx *sql.Tx, h HostingInstance, newUntil time.Time, addPrepaid int) error {
	q := r.db.rebind(`UPDATE hosting_instances SET paid_until = ?, prepaid_days = prepaid_days + ?, updated_at = ?
		WHERE id = ? AND paid_until = ? AND prepaid_days = ? AND status = ?`)
	until := h.PaidUntil
	if !newUntil.IsZero() {
		until = newUntil
	}
	res, err := tx.ExecContext(ctx, q, formatTime(until), addPrepaid, formatTime(time.Now()),
		h.ID, formatTime(h.PaidUntil), h.PrepaidDays, string(h.Status))
	if err != nil {
		return fmt.Errorf("store: apply hosting payment: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrHostingConflict
	}
	return nil
}

// CountByNode returns how many instances occupy each node: every status that
// holds (or may hold) a container there.
func (r *HostingRepo) CountByNode(ctx context.Context) (map[string]int, error) {
	q := r.db.rebind(`SELECT node_id, COUNT(*) FROM hosting_instances
		WHERE node_id <> '' AND status IN (?, ?, ?, ?, ?) GROUP BY node_id`)
	rows, err := r.db.sql.QueryContext(ctx, q, string(HostingProvisioning), string(HostingActive),
		string(HostingExpired), string(HostingStopped), string(HostingFailed))
	if err != nil {
		return nil, fmt.Errorf("store: count hosting by node: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// CountWaitingByRegion counts paid instances not placed on a node yet, per
// region: they will take a slot in that region once created.
func (r *HostingRepo) CountWaitingByRegion(ctx context.Context) (map[string]int, error) {
	q := r.db.rebind(`SELECT region, COUNT(*) FROM hosting_instances
		WHERE node_id = '' AND region <> '' AND status IN (?, ?, ?) GROUP BY region`)
	rows, err := r.db.sql.QueryContext(ctx, q, string(HostingPaid), string(HostingProvisioning), string(HostingFailed))
	if err != nil {
		return nil, fmt.Errorf("store: count waiting hosting by region: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var region string
		var n int
		if err := rows.Scan(&region, &n); err != nil {
			return nil, err
		}
		out[region] = n
	}
	return out, rows.Err()
}

// SubdomainExists reports whether any instance already holds sub.
func (r *HostingRepo) SubdomainExists(ctx context.Context, sub string) (bool, error) {
	q := r.db.rebind(`SELECT COUNT(*) FROM hosting_instances WHERE subdomain = ?`)
	var n int
	if err := r.db.sql.QueryRowContext(ctx, q, sub).Scan(&n); err != nil {
		return false, fmt.Errorf("store: hosting subdomain exists: %w", err)
	}
	return n > 0, nil
}

// ExpireDue marks active instances whose paid_until is before now as expired
// and returns how many changed.
func (r *HostingRepo) ExpireDue(ctx context.Context, now time.Time) (int64, error) {
	q := r.db.rebind(`UPDATE hosting_instances SET status = ?, updated_at = ?
		WHERE status = ? AND paid_until <> '' AND paid_until < ?`)
	res, err := r.db.sql.ExecContext(ctx, q, string(HostingExpired), formatTime(now), string(HostingActive), formatTime(now))
	if err != nil {
		return 0, fmt.Errorf("store: expire hosting instances: %w", err)
	}
	return res.RowsAffected()
}

// FailStaleProvisioning moves instances stuck in provisioning since before
// cutoff (a crash mid-provision) to failed so an admin can retry them.
func (r *HostingRepo) FailStaleProvisioning(ctx context.Context, cutoff time.Time) (int64, error) {
	q := r.db.rebind(`UPDATE hosting_instances SET status = ?, last_error = ?, updated_at = ?
		WHERE status = ? AND updated_at < ?`)
	res, err := r.db.sql.ExecContext(ctx, q, string(HostingFailed), "provisioning was interrupted; retry",
		formatTime(time.Now()), string(HostingProvisioning), formatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("store: fail stale hosting provisioning: %w", err)
	}
	return res.RowsAffected()
}

// ListExpiredBefore returns expired instances whose paid_until is before
// cutoff (past the grace period).
func (r *HostingRepo) ListExpiredBefore(ctx context.Context, cutoff time.Time) ([]HostingInstance, error) {
	q := r.db.rebind(`SELECT ` + hostingCols + ` FROM hosting_instances
		WHERE status = ? AND paid_until <> '' AND paid_until < ? ORDER BY paid_until`)
	return r.queryList(ctx, q, string(HostingExpired), formatTime(cutoff))
}

func (r *HostingRepo) queryList(ctx context.Context, q string, args ...any) ([]HostingInstance, error) {
	rows, err := r.db.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list hosting instances: %w", err)
	}
	defer rows.Close()
	var out []HostingInstance
	for rows.Next() {
		h, err := scanHosting(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func scanHosting(scan func(dest ...any) error) (HostingInstance, error) {
	var (
		h                                        HostingInstance
		status                                   string
		paidUntil, created, updated, provisioned string
	)
	err := scan(&h.ID, &h.TenantID, &h.KeyID, &status, &h.Image, &h.Subdomain, &h.FQDN,
		&h.DokployAppID, &h.DokployAppName, &h.DokployDomainID, &h.DataVolume, &h.LastError,
		&paidUntil, &h.PrepaidDays, &h.Password.WrappedDEK, &h.Password.Ciphertext, &created, &updated, &provisioned, &h.NodeID, &h.Region)
	if err != nil {
		return HostingInstance{}, err
	}
	h.Status = HostingStatus(status)
	h.PaidUntil = parseTime(paidUntil)
	h.CreatedAt = parseTime(created)
	h.UpdatedAt = parseTime(updated)
	h.ProvisionedAt = parseTime(provisioned)
	return h, nil
}
