package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Hosting cost periods.
const (
	CostMonthly = "monthly"
	CostOneTime = "one_time"
)

// HostingCost is one operating cost of the hosting product. StartDate and
// EndDate are YYYY-MM-DD; EndDate is empty for an open-ended monthly cost.
type HostingCost struct {
	ID        string
	Name      string
	Category  string
	AmountIDR int64
	Period    string
	StartDate string
	EndDate   string
	Note      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// HostingCostRepo persists hosting costs.
type HostingCostRepo struct{ db *DB }

// HostingCosts returns the hosting cost repository.
func (db *DB) HostingCosts() *HostingCostRepo { return &HostingCostRepo{db: db} }

const hostingCostCols = `id, name, category, amount_idr, period, start_date, end_date, note, created_at, updated_at`

// Create inserts a cost.
func (r *HostingCostRepo) Create(ctx context.Context, c HostingCost) error {
	q := r.db.rebind(`INSERT INTO hosting_costs (` + hostingCostCols + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := r.db.sql.ExecContext(ctx, q, c.ID, c.Name, c.Category, c.AmountIDR, c.Period,
		c.StartDate, c.EndDate, c.Note, formatTime(c.CreatedAt), formatTime(c.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store: create hosting cost: %w", err)
	}
	return nil
}

// Update replaces a cost's editable fields.
func (r *HostingCostRepo) Update(ctx context.Context, c HostingCost) error {
	q := r.db.rebind(`UPDATE hosting_costs SET name = ?, category = ?, amount_idr = ?, period = ?,
		start_date = ?, end_date = ?, note = ?, updated_at = ? WHERE id = ?`)
	res, err := r.db.sql.ExecContext(ctx, q, c.Name, c.Category, c.AmountIDR, c.Period,
		c.StartDate, c.EndDate, c.Note, formatTime(time.Now()), c.ID)
	if err != nil {
		return fmt.Errorf("store: update hosting cost: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a cost.
func (r *HostingCostRepo) Delete(ctx context.Context, id string) error {
	q := r.db.rebind(`DELETE FROM hosting_costs WHERE id = ?`)
	res, err := r.db.sql.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("store: delete hosting cost: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Get returns one cost.
func (r *HostingCostRepo) Get(ctx context.Context, id string) (HostingCost, error) {
	q := r.db.rebind(`SELECT ` + hostingCostCols + ` FROM hosting_costs WHERE id = ?`)
	c, err := scanHostingCost(r.db.sql.QueryRowContext(ctx, q, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return HostingCost{}, ErrNotFound
	}
	return c, err
}

// List returns every cost, newest start first.
func (r *HostingCostRepo) List(ctx context.Context) ([]HostingCost, error) {
	rows, err := r.db.sql.QueryContext(ctx, `SELECT `+hostingCostCols+` FROM hosting_costs ORDER BY start_date DESC, name`)
	if err != nil {
		return nil, fmt.Errorf("store: list hosting costs: %w", err)
	}
	defer rows.Close()
	var out []HostingCost
	for rows.Next() {
		c, err := scanHostingCost(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanHostingCost(scan func(dest ...any) error) (HostingCost, error) {
	var c HostingCost
	var created, updated string
	if err := scan(&c.ID, &c.Name, &c.Category, &c.AmountIDR, &c.Period, &c.StartDate, &c.EndDate,
		&c.Note, &created, &updated); err != nil {
		return HostingCost{}, err
	}
	c.CreatedAt, c.UpdatedAt = parseTime(created), parseTime(updated)
	return c, nil
}
