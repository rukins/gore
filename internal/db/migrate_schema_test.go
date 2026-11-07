package db_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/rukins/gore/internal/db"
	"github.com/rukins/gore/internal/db/migrate"
)

// TestMigrateFromEmpty runs the schema against a database with nothing in
// it, which is what a new PostgreSQL database hits. The test database may
// already have recorded the migration; it is dropped first so the file is
// applied again.
func TestMigrateFromEmpty(t *testing.T) {
	ctx := context.Background()
	uri := postgresURI()

	conn, err := pgx.Connect(ctx, uri)
	if err != nil {
		t.Skipf("cannot connect to postgres: %v", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx,
		`DROP TABLE IF EXISTS visits, links, schema_migrations, half_applied`); err != nil {
		t.Fatal(err)
	}

	s, err := db.NewStore(ctx, uri)
	if err != nil {
		t.Fatalf("migrate from empty failed: %v", err)
	}
	s.Close()

	files, err := migrate.Load()
	if err != nil {
		t.Fatal(err)
	}
	var applied int
	if err := conn.QueryRow(ctx,
		`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != len(files) {
		t.Fatalf("%d migrations recorded, want %d", applied, len(files))
	}

	for _, col := range []string{
		"host", "alias", "visitor_id", "ip", "ua", "referer",
		"referer_host", "browser", "os", "device", "is_bot", "time",
	} {
		var exists bool
		if err := conn.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_name = 'visits' AND column_name = $1)`,
			col).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("visits is missing column %v", col)
		}
	}

	for _, col := range []struct{ table, name, typ string }{
		{"links", "id", "bigint"},
		{"visits", "id", "bigint"},
		{"visits", "time", "bigint"},
		{"links", "valid_from", "bigint"},
	} {
		var got string
		if err := conn.QueryRow(ctx, `
			SELECT data_type FROM information_schema.columns
			WHERE table_schema = 'public'
			  AND table_name = $1 AND column_name = $2`,
			col.table, col.name).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != col.typ {
			t.Errorf("%s.%s is %s, want %s", col.table, col.name, got, col.typ)
		}
	}

	var visitorType, nullable string
	if err := conn.QueryRow(ctx, `
		SELECT data_type, is_nullable FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name = 'visits' AND column_name = 'visitor_id'`,
	).Scan(&visitorType, &nullable); err != nil {
		t.Fatal(err)
	}
	if visitorType != "text" {
		t.Errorf("visits.visitor_id is %s, want text", visitorType)
	}
	if nullable != "YES" {
		t.Error("visits.visitor_id is NOT NULL, but historical rows have no identifier")
	}

	var ipType string
	if err := conn.QueryRow(ctx, `
		SELECT data_type FROM information_schema.columns
		WHERE table_name = 'visits' AND column_name = 'ip'`,
	).Scan(&ipType); err != nil {
		t.Fatal(err)
	}
	if ipType != "text" {
		t.Errorf("visits.ip is %v, want text: hide_ip stores a digest", ipType)
	}
}
