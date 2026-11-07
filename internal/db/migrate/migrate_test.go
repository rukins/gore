package migrate

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNextIDMonotonic(t *testing.T) {
	a := NextID()
	b := NextID()
	if b <= a {
		t.Fatalf("NextID went from %d to %d", a, b)
	}
}

func TestLoad(t *testing.T) {
	ms, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 {
		t.Fatal("no migrations were embedded")
	}
	for i, m := range ms {
		if m.SQL == "" {
			t.Errorf("migration %v is empty", m.Name)
		}
		if i > 0 && ms[i-1].Version >= m.Version {
			t.Errorf("migration %v is not after %v", m.Name, ms[i-1].Name)
		}
	}
	if ms[0].Version != 1 {
		t.Errorf("first migration is version %d, want 1", ms[0].Version)
	}
}

func TestParseName(t *testing.T) {
	for _, name := range []string{"initial.sql", "abc_initial.sql"} {
		if _, _, ok := parseName(name); ok {
			t.Fatalf("parseName(%q) accepted a bad name", name)
		}
	}
	if v, _, ok := parseName("001_initial.sql"); !ok || v != 1 {
		t.Fatalf("parseName rejected a good name: %v", v)
	}
}

func TestSplitKeepsSemicolonsInsideStringsAndComments(t *testing.T) {
	parts := splitSQL("CREATE TABLE t (note TEXT);\nINSERT INTO t VALUES ('a;b');\n-- tail ; not a split\n")
	var kept []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			kept = append(kept, p)
		}
	}
	// The trailing comment is its own piece. Its semicolon is not a split,
	// and the semicolon inside the string is not one either.
	if len(kept) != 3 {
		t.Fatalf("got %d statements, want 3:\n%q", len(kept), kept)
	}
	if !strings.Contains(kept[1], "'a;b'") {
		t.Fatalf("string semicolon was split: %q", kept[1])
	}
	if !strings.Contains(kept[2], "-- tail ; not a split") {
		t.Fatalf("comment semicolon was split: %q", kept[2])
	}
}

// fakeConn records what the runner did so a failing statement can be shown
// to roll the version row back without a database.
type fakeConn struct {
	failOn    string
	committed bool
	rolled    bool
	recorded  bool
	applied   map[int64]bool
}

func (f *fakeConn) Exec(ctx context.Context, query string, args ...any) error {
	if f.failOn != "" && strings.Contains(query, f.failOn) {
		return errors.New("boom")
	}
	return nil
}

func (f *fakeConn) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	var vs []int64
	for v := range f.applied {
		vs = append(vs, v)
	}
	return &fakeRows{vs: vs}, nil
}

func (f *fakeConn) Begin(ctx context.Context) (Tx, error) {
	return &fakeTx{c: f}, nil
}

type fakeTx struct {
	c          *fakeConn
	failed     bool
	statements []string
}

func (f *fakeTx) Exec(ctx context.Context, query string, args ...any) error {
	if f.c.failOn != "" && strings.Contains(query, f.c.failOn) {
		f.failed = true
		return errors.New("boom")
	}
	f.statements = append(f.statements, query)
	if strings.Contains(query, "INSERT INTO schema_migrations") {
		f.c.recorded = true
	}
	return nil
}

func (f *fakeTx) Commit(ctx context.Context) error {
	if f.failed {
		return errors.New("commit after failure")
	}
	f.c.committed = true
	return nil
}

func (f *fakeTx) Rollback(ctx context.Context) error {
	f.c.rolled = true
	if !f.c.committed {
		f.c.recorded = false
	}
	return nil
}

type fakeRows struct {
	vs []int64
	i  int
}

func (f *fakeRows) Next() bool {
	if f.i >= len(f.vs) {
		return false
	}
	f.i++
	return true
}

func (f *fakeRows) Scan(dest ...any) error {
	*(dest[0].(*int64)) = f.vs[f.i-1]
	return nil
}

func (f *fakeRows) Err() error { return nil }
func (f *fakeRows) Close()     {}

func testOpt() Options {
	return Options{
		Bookkeeping: `CREATE TABLE schema_migrations (version BIGINT PRIMARY KEY)`,
		Record: func(ctx context.Context, tx Tx, version int64) error {
			return tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version)
		},
	}
}

func TestApplyRollsBackAFailingScript(t *testing.T) {
	ctx := context.Background()
	f := &fakeConn{failOn: "does_not_exist"}
	err := ApplyScript(ctx, f, testOpt(), 999999, "999999_bad.sql",
		"CREATE TABLE half_applied (id INT);\nSELECT * FROM does_not_exist;")
	if err == nil {
		t.Fatal("a failing migration reported success")
	}
	if f.committed {
		t.Fatal("the failing migration was committed")
	}
	if !f.rolled {
		t.Fatal("the failing migration was not rolled back")
	}
	if f.recorded {
		t.Fatal("the failing migration was recorded as applied")
	}
}
