package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rukins/gore/internal/db/migrate"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Open connects to the file named by a sqlite:// URI, applies migrations,
// and returns the store.
func Open(ctx context.Context, uri string) (*store, error) {
	db, path, err := openDB(ctx, uri)
	if err != nil {
		return nil, err
	}
	if err := migrateDB(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	tighten(path)
	log.Printf("connected to %v", migrate.Redact(uri))
	return &store{db: db}, nil
}

func openDB(ctx context.Context, uri string) (*sql.DB, string, error) {
	path, err := pathFromURI(uri)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, "", fmt.Errorf("cannot create store directory: %w", err)
	}

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, "", fmt.Errorf("cannot connect to database: %w", err)
	}
	// One connection per process. A pool of writers deadlocks itself, and
	// a second process waits on busy_timeout instead.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, "", fmt.Errorf("cannot connect to database: %w", err)
	}
	return db, path, nil
}

// pathFromURI is the text after "sqlite://". url.Parse treats
// sqlite://data/gore.db as host "data", so the path is not parsed that way.
func pathFromURI(uri string) (string, error) {
	_, rest, ok := strings.Cut(uri, "://")
	if !ok || strings.TrimSpace(rest) == "" {
		return "", fmt.Errorf("store %q has an empty path", migrate.Redact(uri))
	}
	if strings.ContainsAny(rest, "?#") {
		return "", fmt.Errorf("store path %q contains ? or #", rest)
	}
	return rest, nil
}

func dsn(path string) string {
	// _txlock makes BEGIN an immediate transaction, which is the write lock
	// two processes starting together need. The driver enables extended
	// result codes, so a unique violation is distinguishable from other
	// constraints.
	return "file:" + path +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_txlock=immediate"
}

func tighten(path string) {
	_ = os.Chmod(path, 0o600)
	_ = os.Chmod(path+"-wal", 0o600)
	_ = os.Chmod(path+"-shm", 0o600)
}

func migrateDB(ctx context.Context, db *sql.DB) error {
	c, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("cannot acquire connection for migration: %w", err)
	}
	defer c.Close()

	return migrate.Apply(ctx, liteConn{c}, migrate.Options{
		Bookkeeping: migrate.BookkeepingSQL,
		Split:       true,
		Record: func(ctx context.Context, tx migrate.Tx, version int64) error {
			return tx.Exec(ctx,
				`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
				version, time.Now().UTC().UnixMicro())
		},
	})
}

// Timestamps are Unix microseconds, UTC. The driver is not asked to encode
// time.Time: its default text form is not fixed-width, so a text column
// would not compare in chronological order.
func micros(t time.Time) int64 { return t.UTC().UnixMicro() }

func fromMicros(n int64) time.Time { return time.UnixMicro(n).UTC() }

type liteConn struct{ c *sql.Conn }

func (l liteConn) Exec(ctx context.Context, query string, args ...any) error {
	_, err := l.c.ExecContext(ctx, query, args...)
	return err
}

func (l liteConn) Query(ctx context.Context, query string, args ...any) (migrate.Rows, error) {
	rows, err := l.c.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return liteRows{rows}, nil
}

func (l liteConn) Begin(ctx context.Context) (migrate.Tx, error) {
	tx, err := l.c.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return liteTx{tx}, nil
}

type liteRows struct{ r *sql.Rows }

func (l liteRows) Next() bool             { return l.r.Next() }
func (l liteRows) Scan(dest ...any) error { return l.r.Scan(dest...) }
func (l liteRows) Err() error             { return l.r.Err() }
func (l liteRows) Close()                 { l.r.Close() }

type liteTx struct{ tx *sql.Tx }

func (l liteTx) Exec(ctx context.Context, query string, args ...any) error {
	_, err := l.tx.ExecContext(ctx, query, args...)
	return err
}

func (l liteTx) Commit(ctx context.Context) error   { return l.tx.Commit() }
func (l liteTx) Rollback(ctx context.Context) error { return l.tx.Rollback() }
