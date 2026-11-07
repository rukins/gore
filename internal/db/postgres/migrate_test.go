package postgres

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rukins/gore/internal/db/migrate"
)

func testPostgresURI() string {
	if uri := os.Getenv("GORE_TEST_POSTGRES"); uri != "" {
		return uri
	}
	return "postgres://gore:gore@127.0.0.1:5432/gore_test?sslmode=disable"
}

// TestApplyScriptRollsBack checks that a failing file leaves nothing
// behind, neither its tables nor its version row. A half-applied schema
// is worse than none, because the next start would skip it.
//
// This does not take the advisory lock and does not touch links or visits,
// so it can run beside the store tests.
func TestApplyScriptRollsBack(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testPostgresURI())
	if err != nil {
		t.Skipf("cannot connect to postgres: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("cannot connect to postgres: %v", err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()

	err = migrate.ApplyScript(ctx, pgConn{conn.Conn()}, migrate.Options{
		Bookkeeping: migrate.BookkeepingSQL,
		Record:      recordVersion,
	}, 999999, "999999_bad.sql",
		"CREATE TABLE half_applied (id INT);\nSELECT * FROM a_table_that_does_not_exist;")
	if err == nil {
		t.Fatal("a failing migration reported success")
	}

	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM information_schema.tables
		               WHERE table_name = 'half_applied')`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("the failing migration left its table behind")
	}

	var recorded bool
	err = pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`,
		999999).Scan(&recorded)
	if err != nil && !strings.Contains(err.Error(), "does not exist") {
		t.Fatal(err)
	}
	if recorded {
		t.Error("the failing migration was recorded as applied")
	}
}

// TestMigrateFailsOnClosedPool checks that a failure to reach the database
// is returned rather than logged and stepped over.
func TestMigrateFailsOnClosedPool(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testPostgresURI())
	if err != nil {
		t.Skipf("cannot connect to postgres: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("cannot connect to postgres: %v", err)
	}
	pool.Close()

	if err := migratePool(ctx, pool); err == nil {
		t.Fatal("migrate on a closed pool reported success")
	}
}
