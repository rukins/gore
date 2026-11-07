// Package migrate applies the embedded SQL migrations.
//
// There is one sequence of files, and both engines run them as written.
// A version recorded in schema_migrations is not applied again, and the
// script shares a transaction with the row that records it.
package migrate

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"slices"
	"strconv"
	"strings"
)

//go:embed all:migrations/*.sql
var migrationFS embed.FS

// ErrAliasExists reports that (host, alias) is taken. Callers compare it
// by identity, so both engines return this value rather than a wrapped one.
var ErrAliasExists = fmt.Errorf("alias already existed")

// BookkeepingSQL creates schema_migrations. applied_at is Unix
// microseconds, written by the record hook. Both engines run this
// statement as it is. An existing table is left as CREATE TABLE IF NOT
// EXISTS found it.
const BookkeepingSQL = `
	CREATE TABLE IF NOT EXISTS schema_migrations (
		version    BIGINT PRIMARY KEY,
		applied_at BIGINT NOT NULL
	)`

// File is one numbered migration.
type File struct {
	Version int64
	Name    string
	SQL     string
}

// Rows is the subset of a query result the runner reads.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}

// Tx is one migration transaction.
type Tx interface {
	Exec(ctx context.Context, sql string, args ...any) error
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// Conn is the connection migrations run on. It is one session, not a pool:
// an advisory lock held here would not cover work done on another connection.
type Conn interface {
	Exec(ctx context.Context, sql string, args ...any) error
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	Begin(ctx context.Context) (Tx, error)
}

// Options is what differs between engines. Everything else is shared.
type Options struct {
	// Lock and Unlock bracket the whole run. PostgreSQL takes an advisory
	// lock. SQLite leaves them nil and uses an immediate transaction per file.
	Lock   func(ctx context.Context) error
	Unlock func(ctx context.Context) error
	// Bookkeeping creates schema_migrations if it is missing. Both engines
	// pass BookkeepingSQL.
	Bookkeeping string
	// Record inserts the version row inside the migration transaction.
	Record func(ctx context.Context, tx Tx, version int64) error
	// Split runs the script one statement at a time. SQLite's driver
	// executes only the first statement of an Exec.
	Split bool
}

// Load reads the embedded files in version order.
func Load() ([]File, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("cannot read migrations: %w", err)
	}

	ms := make([]File, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		v, _, ok := parseName(e.Name())
		if !ok {
			return nil, fmt.Errorf(
				"migration %v: want <version>_<name>.sql with a numeric version",
				e.Name())
		}
		b, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("migration %v: %w", e.Name(), err)
		}
		ms = append(ms, File{Version: v, Name: e.Name(), SQL: string(b)})
	}
	slices.SortFunc(ms, func(a, b File) int {
		return int(a.Version - b.Version)
	})

	for i := 1; i < len(ms); i++ {
		if ms[i].Version == ms[i-1].Version {
			return nil, fmt.Errorf("migrations %v and %v share version %d",
				ms[i-1].Name, ms[i].Name, ms[i].Version)
		}
	}
	return ms, nil
}

// Apply runs every migration this database has not recorded.
func Apply(ctx context.Context, conn Conn, opt Options) error {
	if opt.Bookkeeping == "" || opt.Record == nil {
		return fmt.Errorf("migrate: missing bookkeeping or record hook")
	}
	ms, err := Load()
	if err != nil {
		return err
	}

	if opt.Lock != nil {
		if err := opt.Lock(ctx); err != nil {
			return fmt.Errorf("cannot take migration lock: %w", err)
		}
		defer func() {
			if opt.Unlock == nil {
				return
			}
			if err := opt.Unlock(ctx); err != nil {
				log.Printf("cannot release migration lock: %v", err)
			}
		}()
	}

	if err := conn.Exec(ctx, opt.Bookkeeping); err != nil {
		return fmt.Errorf("cannot create schema_migrations: %w", err)
	}

	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("cannot read schema_migrations: %w", err)
	}
	applied := map[int64]bool{}
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return fmt.Errorf("cannot scan schema_migrations: %w", err)
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("cannot read schema_migrations: %w", err)
	}

	for _, m := range ms {
		if applied[m.Version] {
			continue
		}
		if err := applyOne(ctx, conn, m, opt); err != nil {
			return err
		}
		log.Printf("applied migration %v", m.Name)
	}
	return nil
}

// ApplyScript applies one script the same way Apply applies a file. Tests
// use it for a statement that must roll back.
func ApplyScript(ctx context.Context, conn Conn, opt Options, version int64, name, script string) error {
	return applyOne(ctx, conn, File{Version: version, Name: name, SQL: script}, opt)
}

func applyOne(ctx context.Context, conn Conn, m File, opt Options) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migration %v: cannot begin: %w", m.Name, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	script := m.SQL
	parts := []string{script}
	if opt.Split {
		parts = splitSQL(script)
	}
	for _, stmt := range parts {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("migration %v: %w", m.Name, err)
		}
	}
	if err := opt.Record(ctx, tx, m.Version); err != nil {
		return fmt.Errorf("migration %v: cannot record: %w", m.Name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migration %v: cannot commit: %w", m.Name, err)
	}
	return nil
}

// parseName splits a file name into its version and label. A name that
// does not parse is an error rather than a file to skip: a skipped
// migration leaves the schema incomplete and says nothing.
func parseName(name string) (version int64, label string, ok bool) {
	prefix, rest, found := strings.Cut(name, "_")
	if !found {
		return 0, "", false
	}
	v, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil {
		return 0, "", false
	}
	return v, rest, true
}
