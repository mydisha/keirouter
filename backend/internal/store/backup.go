package store

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BackupFormat identifies a KeiRouter full logical backup file.
const (
	BackupFormat  = "keirouter-backup"
	BackupVersion = 1
)

// BackupTable is one table's rows. Values are plain JSON scalars (string,
// number, null), so a backup taken on SQLite restores into Postgres and back.
type BackupTable struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// Backup is a full logical dump of every application table. Encryption holds
// caller-defined metadata (the passphrase box salt and check value).
type Backup struct {
	Format        string          `json:"format"`
	Version       int             `json:"version"`
	CreatedAt     string          `json:"created_at"`
	SourceDialect string          `json:"source_dialect"`
	SchemaVersion string          `json:"schema_version"`
	Encryption    json.RawMessage `json:"encryption,omitempty"`
	Tables        []BackupTable   `json:"tables"`
}

// RestoreStats summarises a restore.
type RestoreStats struct {
	Tables int `json:"tables"`
	Rows   int `json:"rows"`
}

// DecodeBackup parses a backup file, keeping numbers exact (int64 stays
// int64, floats keep their shortest round-trip form).
func DecodeBackup(r io.Reader) (Backup, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	var b Backup
	if err := dec.Decode(&b); err != nil {
		return Backup{}, fmt.Errorf("store: decode backup: %w", err)
	}
	if b.Format != BackupFormat {
		return Backup{}, fmt.Errorf("store: not a KeiRouter backup file")
	}
	if b.Version != BackupVersion {
		return Backup{}, fmt.Errorf("store: unsupported backup version %d", b.Version)
	}
	return b, nil
}

// ExportBackup dumps every application table (all but schema_migrations) in
// foreign-key order. It reads inside one transaction so the dump is a
// consistent snapshot even while requests keep writing usage.
func (db *DB) ExportBackup(ctx context.Context) (Backup, error) {
	tables, err := db.backupTables(ctx)
	if err != nil {
		return Backup{}, err
	}
	version, err := db.latestMigration(ctx)
	if err != nil {
		return Backup{}, err
	}
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return Backup{}, err
	}
	defer func() { _ = tx.Rollback() }()

	out := Backup{
		Format: BackupFormat, Version: BackupVersion,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		SourceDialect: string(db.dialect), SchemaVersion: version,
	}
	for _, name := range tables {
		rows, err := tx.QueryContext(ctx, `SELECT * FROM `+quoteIdent(name))
		if err != nil {
			return Backup{}, fmt.Errorf("store: backup %s: %w", name, err)
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return Backup{}, err
		}
		t := BackupTable{Name: name, Columns: cols, Rows: [][]any{}}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return Backup{}, fmt.Errorf("store: backup %s: %w", name, err)
			}
			for i, v := range vals {
				if b, ok := v.([]byte); ok {
					vals[i] = string(b)
				}
				if tm, ok := v.(time.Time); ok {
					vals[i] = tm.UTC().Format(time.RFC3339Nano)
				}
			}
			t.Rows = append(t.Rows, vals)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return Backup{}, fmt.Errorf("store: backup %s: %w", name, err)
		}
		out.Tables = append(out.Tables, t)
	}
	return out, nil
}

// RestoreBackup replaces the contents of every application table with the
// backup, in one transaction: on any error nothing changes. The target must
// already be migrated to at least the backup's schema version; columns the
// backup predates keep their defaults.
func (db *DB) RestoreBackup(ctx context.Context, b Backup) (RestoreStats, error) {
	if b.Format != BackupFormat || b.Version != BackupVersion {
		return RestoreStats{}, fmt.Errorf("store: not a KeiRouter v%d backup", BackupVersion)
	}
	version, err := db.latestMigration(ctx)
	if err != nil {
		return RestoreStats{}, err
	}
	if migrationNumber(b.SchemaVersion) > migrationNumber(version) {
		return RestoreStats{}, fmt.Errorf("store: backup schema %s is newer than this server (%s); upgrade KeiRouter first", b.SchemaVersion, version)
	}
	tables, err := db.backupTables(ctx)
	if err != nil {
		return RestoreStats{}, err
	}
	colTypes := map[string]map[string]string{}
	for _, name := range tables {
		types, err := db.columnTypes(ctx, name)
		if err != nil {
			return RestoreStats{}, err
		}
		colTypes[name] = types
	}
	byName := map[string]BackupTable{}
	for _, t := range b.Tables {
		types, ok := colTypes[t.Name]
		if !ok {
			return RestoreStats{}, fmt.Errorf("store: backup table %q does not exist on this server", t.Name)
		}
		for _, c := range t.Columns {
			if _, ok := types[c]; !ok {
				return RestoreStats{}, fmt.Errorf("store: backup column %s.%s does not exist on this server", t.Name, c)
			}
		}
		byName[t.Name] = t
	}

	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return RestoreStats{}, err
	}
	defer func() { _ = tx.Rollback() }()

	for i := len(tables) - 1; i >= 0; i-- {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+quoteIdent(tables[i])); err != nil {
			return RestoreStats{}, fmt.Errorf("store: clear %s: %w", tables[i], err)
		}
	}
	stats := RestoreStats{}
	for _, name := range tables {
		t, ok := byName[name]
		if !ok {
			continue
		}
		stats.Tables++
		if len(t.Rows) == 0 {
			continue
		}
		quoted := make([]string, len(t.Columns))
		marks := make([]string, len(t.Columns))
		for i, c := range t.Columns {
			quoted[i] = quoteIdent(c)
			marks[i] = "?"
		}
		stmt, err := tx.PrepareContext(ctx, db.rebind(`INSERT INTO `+quoteIdent(name)+` (`+strings.Join(quoted, ", ")+`) VALUES (`+strings.Join(marks, ", ")+`)`))
		if err != nil {
			return RestoreStats{}, fmt.Errorf("store: restore %s: %w", name, err)
		}
		for _, row := range t.Rows {
			if len(row) != len(t.Columns) {
				stmt.Close()
				return RestoreStats{}, fmt.Errorf("store: restore %s: row has %d values for %d columns", name, len(row), len(t.Columns))
			}
			args := make([]any, len(row))
			for i, v := range row {
				a, err := restoreValue(v, colTypes[name][t.Columns[i]])
				if err != nil {
					stmt.Close()
					return RestoreStats{}, fmt.Errorf("store: restore %s.%s: %w", name, t.Columns[i], err)
				}
				args[i] = a
			}
			if _, err := stmt.ExecContext(ctx, args...); err != nil {
				stmt.Close()
				return RestoreStats{}, fmt.Errorf("store: restore %s: %w", name, err)
			}
			stats.Rows++
		}
		stmt.Close()
	}
	if err := tx.Commit(); err != nil {
		return RestoreStats{}, err
	}
	return stats, nil
}

// restoreValue converts a decoded JSON value to the Go type the target column
// expects, so numbers land exactly in integer and floating columns alike.
func restoreValue(v any, colType string) (any, error) {
	if v == nil {
		return nil, nil
	}
	t := strings.ToLower(colType)
	isInt := strings.Contains(t, "int")
	isFloat := strings.Contains(t, "double") || strings.Contains(t, "real") || strings.Contains(t, "float") || strings.Contains(t, "numeric")
	switch x := v.(type) {
	case json.Number:
		switch {
		case isInt:
			if n, err := strconv.ParseInt(string(x), 10, 64); err == nil {
				return n, nil
			}
			f, err := x.Float64()
			if err != nil {
				return nil, err
			}
			return int64(f), nil
		case isFloat:
			return x.Float64()
		default:
			return string(x), nil
		}
	case float64:
		if isInt {
			return int64(x), nil
		}
		if isFloat {
			return x, nil
		}
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case int64:
		if isFloat {
			return float64(x), nil
		}
		return x, nil
	case bool:
		if x {
			return int64(1), nil
		}
		return int64(0), nil
	case string:
		if isInt {
			if n, err := strconv.ParseInt(x, 10, 64); err == nil {
				return n, nil
			}
		}
		if isFloat {
			if f, err := strconv.ParseFloat(x, 64); err == nil {
				return f, nil
			}
		}
		return x, nil
	default:
		return nil, fmt.Errorf("unsupported value type %T", v)
	}
}

// backupTables lists application tables parents-first by foreign key.
func (db *DB) backupTables(ctx context.Context) ([]string, error) {
	var listQ string
	if db.dialect == DialectPostgres {
		listQ = `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'`
	} else {
		listQ = `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`
	}
	rows, err := db.sql.QueryContext(ctx, listQ)
	if err != nil {
		return nil, fmt.Errorf("store: list tables: %w", err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		if n != "schema_migrations" {
			names = append(names, n)
		}
	}
	rows.Close()
	sort.Strings(names)

	deps := map[string][]string{}
	for _, n := range names {
		parents, err := db.foreignKeyParents(ctx, n)
		if err != nil {
			return nil, err
		}
		deps[n] = parents
	}
	var order []string
	state := map[string]int{} // 1 visiting, 2 done
	var visit func(string)
	visit = func(n string) {
		if state[n] != 0 {
			return // done, or a cycle: keep the first-seen order
		}
		state[n] = 1
		for _, p := range deps[n] {
			if p != n {
				if _, known := deps[p]; known {
					visit(p)
				}
			}
		}
		state[n] = 2
		order = append(order, n)
	}
	for _, n := range names {
		visit(n)
	}
	return order, nil
}

func (db *DB) foreignKeyParents(ctx context.Context, table string) ([]string, error) {
	var q string
	if db.dialect == DialectPostgres {
		q = `SELECT DISTINCT ccu.table_name
			FROM information_schema.table_constraints tc
			JOIN information_schema.constraint_column_usage ccu
			  ON tc.constraint_name = ccu.constraint_name AND tc.table_schema = ccu.table_schema
			WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_schema = current_schema() AND tc.table_name = $1`
	} else {
		q = `SELECT DISTINCT "table" FROM pragma_foreign_key_list(?)`
	}
	rows, err := db.sql.QueryContext(ctx, q, table)
	if err != nil {
		return nil, fmt.Errorf("store: foreign keys of %s: %w", table, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (db *DB) columnTypes(ctx context.Context, table string) (map[string]string, error) {
	var q string
	if db.dialect == DialectPostgres {
		q = `SELECT column_name, data_type FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1`
	} else {
		q = `SELECT name, type FROM pragma_table_info(?)`
	}
	rows, err := db.sql.QueryContext(ctx, q, table)
	if err != nil {
		return nil, fmt.Errorf("store: columns of %s: %w", table, err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, err
		}
		out[name] = typ
	}
	return out, rows.Err()
}

// latestMigration returns the highest applied migration version.
func (db *DB) latestMigration(ctx context.Context) (string, error) {
	applied, err := db.appliedVersions(ctx)
	if err != nil {
		return "", err
	}
	latest := ""
	for v := range applied {
		if migrationNumber(v) > migrationNumber(latest) || (migrationNumber(v) == migrationNumber(latest) && v > latest) {
			latest = v
		}
	}
	return latest, nil
}

// migrationNumber parses the numeric prefix of a migration version ("0050_x").
func migrationNumber(v string) int {
	end := 0
	for end < len(v) && v[end] >= '0' && v[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(v[:end])
	return n
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
