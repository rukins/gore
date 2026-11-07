package postgres

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rukins/gore/internal/db/migrate"
)

// Open connects to uri, applies migrations, and returns the store.
func Open(ctx context.Context, uri string) (*store, error) {
	cfg, err := pgxpool.ParseConfig(uri)
	if err != nil {
		return nil, fmt.Errorf("cannot parse store uri: %w", err)
	}

	// Pin the session time zone.
	//
	// Hourly buckets are date_trunc('hour', to_timestamp(v.time / 1000000.0)).
	// to_timestamp is UTC, but date_trunc still truncates in the session's
	// time zone, so the buckets would shift with a server setting nothing
	// in this repository controls. The instance is shared with another
	// service, so the zone is set here rather than assumed.
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("cannot connect to database: %w", err)
	}

	// A store without its schema must not serve traffic, so a failure
	// here is returned rather than logged and stepped over.
	if err := migratePool(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}

	log.Printf("connected to %v", migrate.Redact(uri))
	return &store{pool: pool}, nil
}

// migrateLockKey is an arbitrary but fixed key for the advisory lock that
// serialises migration across instances. Two gore processes starting at
// the same time must not both apply the same file.
const migrateLockKey int64 = 0x7265646972 // historical lock id; do not change

func migratePool(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("cannot acquire connection for migration: %w", err)
	}
	defer conn.Release()

	pg := conn.Conn()
	return migrate.Apply(ctx, pgConn{pg}, migrate.Options{
		Lock: func(ctx context.Context) error {
			_, err := pg.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrateLockKey)
			return err
		},
		Unlock: func(ctx context.Context) error {
			_, err := pg.Exec(ctx, `SELECT pg_advisory_unlock($1)`, migrateLockKey)
			return err
		},
		Bookkeeping: migrate.BookkeepingSQL,
		Record:      recordVersion,
	})
}

func recordVersion(ctx context.Context, tx migrate.Tx, version int64) error {
	return tx.Exec(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES ($1, $2)`,
		version, time.Now().UTC().UnixMicro())
}

// pgConn adapts one session to the migration runner.
type pgConn struct{ c *pgx.Conn }

func (p pgConn) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := p.c.Exec(ctx, sql, args...)
	return err
}

func (p pgConn) Query(ctx context.Context, sql string, args ...any) (migrate.Rows, error) {
	rows, err := p.c.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgRows{rows}, nil
}

func (p pgConn) Begin(ctx context.Context) (migrate.Tx, error) {
	tx, err := p.c.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return pgTx{tx}, nil
}

type pgRows struct{ r pgx.Rows }

func (p pgRows) Next() bool             { return p.r.Next() }
func (p pgRows) Scan(dest ...any) error { return p.r.Scan(dest...) }
func (p pgRows) Err() error             { return p.r.Err() }
func (p pgRows) Close()                 { p.r.Close() }

type pgTx struct{ tx pgx.Tx }

func (p pgTx) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := p.tx.Exec(ctx, sql, args...)
	return err
}

func (p pgTx) Commit(ctx context.Context) error   { return p.tx.Commit(ctx) }
func (p pgTx) Rollback(ctx context.Context) error { return p.tx.Rollback(ctx) }
