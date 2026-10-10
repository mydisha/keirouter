// Package store is KeiRouter's persistence layer. It opens a database
// connection (SQLite by default, Postgres optionally), applies embedded
// migrations on startup, and exposes typed repositories for each domain.
//
// The same SQL runs against both engines wherever possible; dialect-specific
// statements are selected via the Dialect value. This keeps the local
// single-binary experience zero-config while letting team/VPS deployments point
// at Postgres without code changes.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"runtime"
	"strconv"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver registered as "pgx"
	_ "modernc.org/sqlite"             // pure-Go SQLite driver, no CGO

	"github.com/mydisha/keirouter/backend/internal/config"
)

// Dialect identifies the active SQL engine.
type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectPostgres Dialect = "postgres"
)

// DB wraps the sql.DB handle together with its dialect so repositories can
// adapt placeholder syntax and engine-specific statements.
type DB struct {
	sql        *sql.DB
	dialect    Dialect
	sqlitePath string
}

// Open connects using the given database configuration and verifies the
// connection. The caller is responsible for calling Close.
func Open(ctx context.Context, cfg config.DatabaseConfig, dataDir string) (*DB, error) {
	dialect := Dialect(cfg.Driver)

	var (
		driverName     string
		dsn            string
		sqliteFilePath string
	)
	switch dialect {
	case DialectSQLite:
		driverName = "sqlite"
		sqliteFilePath = sqlitePath(cfg, dataDir)
		dsn = sqliteDSNFromPath(sqliteFilePath)
	case DialectPostgres:
		driverName = "pgx"
		dsn = cfg.DSN
	default:
		return nil, fmt.Errorf("store: unsupported driver %q", cfg.Driver)
	}

	sqlDB, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", dialect, err)
	}

	// SQLite is single-writer; cap connections to avoid SQLITE_BUSY. Postgres
	// uses the configured pool.
	if dialect == DialectSQLite {
		maxOpen := cfg.MaxOpenConns
		if maxOpen <= 0 {
			maxOpen = max(runtime.GOMAXPROCS(0), 8)
		}
		sqlDB.SetMaxOpenConns(maxOpen)
		if cfg.MaxIdleConns > 0 {
			sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
		} else {
			sqlDB.SetMaxIdleConns(maxOpen)
		}
	} else {
		maxOpen := cfg.MaxOpenConns
		if maxOpen <= 0 {
			maxOpen = 25
		}
		// Migration locking holds one dedicated session while DDL runs on the
		// pool, so PostgreSQL needs at least one additional connection.
		if maxOpen < 2 {
			maxOpen = 2
		}
		maxIdle := cfg.MaxIdleConns
		if maxIdle <= 0 {
			maxIdle = min(10, maxOpen)
		}
		if maxIdle > maxOpen {
			maxIdle = maxOpen
		}
		sqlDB.SetMaxOpenConns(maxOpen)
		sqlDB.SetMaxIdleConns(maxIdle)
		if cfg.ConnMaxLifetime > 0 {
			sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
		}
		if cfg.ConnMaxIdleTime > 0 {
			sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
		}
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("store: ping %s: %w", dialect, err)
	}

	db := &DB{sql: sqlDB, dialect: dialect, sqlitePath: sqliteFilePath}
	if dialect == DialectSQLite {
		if err := db.applySQLitePragmas(ctx); err != nil {
			_ = sqlDB.Close()
			return nil, err
		}
	}
	return db, nil
}

// sqliteDSN builds the SQLite connection string. An empty DSN defaults to a
// file under the data directory; ":memory:" is honored for tests.
func sqlitePath(cfg config.DatabaseConfig, dataDir string) string {
	path := cfg.DSN
	if path == "" {
		path = dataDir + "/keirouter.db"
	}
	return path
}

func sqliteDSN(cfg config.DatabaseConfig, dataDir string) string {
	return sqliteDSNFromPath(sqlitePath(cfg, dataDir))
}

func sqliteDSNFromPath(path string) string {
	if path == ":memory:" {
		// Unique name + mode=memory + cache=shared: each Open() call gets
		// its own isolated in-memory database, while connections within
		// the same pool still share it. Without a unique name every
		// :memory: caller would share the same database, causing
		// cross-test UNIQUE-constraint violations.
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		return fmt.Sprintf("file:mem_%x?mode=memory&cache=shared", b)
	}
	// WAL + busy timeout improve concurrent read/write behavior.
	return "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
}

func (db *DB) applySQLitePragmas(ctx context.Context) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON;",
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA temp_store = MEMORY;",
		"PRAGMA cache_size = -32768;",
	}
	if db.sqlitePath != ":memory:" {
		pragmas = append(pragmas, "PRAGMA mmap_size = 268435456;")
	}
	for _, p := range pragmas {
		if _, err := db.sql.ExecContext(ctx, p); err != nil {
			return fmt.Errorf("store: pragma %q: %w", p, err)
		}
	}
	return nil
}

// SQL exposes the underlying handle for repositories within this package.
func (db *DB) SQL() *sql.DB { return db.sql }

// Dialect reports the active engine.
func (db *DB) Dialect() Dialect { return db.dialect }

// SQLitePath reports the SQLite database file path when the active engine is SQLite.
func (db *DB) SQLitePath() string { return db.sqlitePath }

// BeginTx starts a database transaction. Callers use this for multi-table
// writes that must succeed or fail atomically (e.g. key + budget creation).
func (db *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return db.sql.BeginTx(ctx, opts)
}

// Close releases the connection pool.
func (db *DB) Close() error { return db.sql.Close() }

// epochExpr returns a dialect-specific SQL expression that converts a TEXT
// RFC3339 timestamp (column or placeholder) to unix-epoch seconds. SQLite has
// strftime; Postgres needs an explicit cast to timestamptz before EXTRACT.
// The Postgres result is cast to BIGINT because EXTRACT yields NUMERIC: bucket
// math like CAST((a-b)/? AS INTEGER) would then round instead of truncating
// the way SQLite's integer division does, shifting rows into the next bucket.
func (db *DB) epochExpr(operand string) string {
	if db.dialect == DialectPostgres {
		return "CAST(EXTRACT(EPOCH FROM " + operand + "::timestamptz) AS BIGINT)"
	}
	return "strftime('%s', " + operand + ")"
}

// dateExpr returns a dialect-specific SQL expression that yields the YYYY-MM-DD
// calendar date (as text) of a TEXT RFC3339 timestamp. SQLite's DATE() accepts
// text directly and returns text; Postgres needs a cast through timestamptz
// and an explicit format so the scanned value is always a string.
func (db *DB) dateExpr(operand string) string {
	if db.dialect == DialectPostgres {
		return "TO_CHAR((" + operand + "::timestamptz AT TIME ZONE 'UTC'), 'YYYY-MM-DD')"
	}
	return "DATE(" + operand + ")"
}

// rebind converts '?' placeholders to the engine's native form. SQLite accepts
// '?' directly; Postgres needs $1, $2, ... positional placeholders.
func (db *DB) rebind(query string) string {
	if db.dialect != DialectPostgres {
		return query
	}
	var b []byte
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
			b = append(b, '$')
			b = strconv.AppendInt(b, int64(n), 10)
			continue
		}
		b = append(b, query[i])
	}
	return string(b)
}
