package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rukins/gore/internal/db/migrate"
)

func TestMigrateFromEmpty(t *testing.T) {
	ctx := context.Background()
	uri := "sqlite://" + filepath.Join(t.TempDir(), "gore.db")

	s, err := Open(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	db := openFile(t, uri)
	defer db.Close()

	for _, table := range []string{"links", "visits", "schema_migrations"} {
		var n int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
			table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("missing table %s", table)
		}
	}

	// Same shapes the PostgreSQL empty-file test checks. pragma reports
	// the declared type. visitor_id stays nullable: historical rows have
	// no identifier.
	for _, col := range []struct {
		table, name, typ string
		notnull          int
	}{
		{"links", "id", "BIGINT", 0},
		{"visits", "id", "BIGINT", 0},
		{"visits", "time", "BIGINT", 1},
		{"links", "valid_from", "BIGINT", 1},
		{"visits", "visitor_id", "TEXT", 0},
	} {
		var typ string
		var notnull int
		q := `SELECT type, "notnull" FROM pragma_table_info('` + col.table + `') WHERE name = ?`
		if err := db.QueryRowContext(ctx, q, col.name).Scan(&typ, &notnull); err != nil {
			t.Fatal(err)
		}
		if typ != col.typ {
			t.Errorf("%s.%s type = %q, want %s", col.table, col.name, typ, col.typ)
		}
		if notnull != col.notnull {
			t.Errorf("%s.%s notnull = %d, want %d", col.table, col.name, notnull, col.notnull)
		}
	}

	var applied int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	files, err := migrate.Load()
	if err != nil {
		t.Fatal(err)
	}
	if applied != len(files) {
		t.Fatalf("%d migrations recorded, want %d", applied, len(files))
	}

	again, err := Open(ctx, uri)
	if err != nil {
		t.Fatalf("second open failed: %v", err)
	}
	again.Close()
	var after int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != applied {
		t.Fatalf("second open recorded %d migrations, want %d", after, applied)
	}
}

func TestApplyScriptRollsBack(t *testing.T) {
	ctx := context.Background()
	uri := "sqlite://" + filepath.Join(t.TempDir(), "gore.db")
	s, err := Open(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	db := openFile(t, uri)
	defer db.Close()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	err = migrate.ApplyScript(ctx, liteConn{c}, migrate.Options{
		Bookkeeping: migrate.BookkeepingSQL,
		Split:       true,
		Record: func(ctx context.Context, tx migrate.Tx, version int64) error {
			return tx.Exec(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, version, 0)
		},
	}, 999999, "999999_bad.sql",
		"CREATE TABLE half_applied (id INT);\nSELECT * FROM a_table_that_does_not_exist;")
	if err == nil {
		t.Fatal("a failing migration reported success")
	}

	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE name = 'half_applied'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("the failing migration left its table behind")
	}
	var recorded int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = 999999`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 0 {
		t.Error("the failing migration was recorded as applied")
	}
}

func openFile(t *testing.T, uri string) *sql.DB {
	t.Helper()
	path := strings.TrimPrefix(uri, "sqlite://")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	return db
}
