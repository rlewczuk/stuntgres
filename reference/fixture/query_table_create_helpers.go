package fixture

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
)

func QueryTableCreateSession(t *testing.T, p *Pair, b Backend) (*sql.Conn, func(Step)) {
	t.Helper()
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	return conn, func(s Step) { RunStep(t, p.Ctx, conn, b.Recorder, s) }
}

func QueryTableCreateAbsent(t *testing.T, run func(Step), name string) {
	t.Helper()
	run(Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='"+name+"'", []string{"relname"}, []string{"NAME"}, nil))
	run(Select("SELECT typname FROM pg_catalog.pg_type WHERE typname='"+name+"'", []string{"typname"}, []string{"NAME"}, nil))
}

func QueryTableCreateSource(run func(Step)) {
	run(Exec("CREATE TABLE qc_source(i INTEGER,s TEXT)", 0))
	run(Exec("INSERT INTO qc_source VALUES(1,'a'),(1,'a'),(2,NULL)", 3))
}

func QueryTableCreateBag(name string) Step {
	s := Select("SELECT i,s FROM "+name, []string{"i", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}})
	s.Bag = true
	return s
}

// A genuine PG tuple identity is read from the locked row, never pinned to an
// allocator-dependent block/offset constant. Native exposes no physical ctid.
func QueryTableCreateTuple(t *testing.T, ctx context.Context, conn *sql.Conn, postgres bool, relation string) string {
	t.Helper()
	if !postgres {
		return ""
	}
	var tuple string
	if err := conn.QueryRowContext(ctx, "SELECT ctid::text FROM "+relation+" WHERE i=7").Scan(&tuple); err != nil {
		t.Fatal(err)
	}
	matched, err := regexp.MatchString(`^\([0-9]+,[1-9][0-9]*\)$`, tuple)
	if err != nil || !matched {
		t.Fatalf("physical tuple identity %q error=%v", tuple, err)
	}
	return tuple
}

func QueryTableCreateLockDiagnostic(t *testing.T, err error, postgres bool, code, message, relation, tuple string) {
	t.Helper()
	want := ControlsReferenceDiagnostic(code, message)
	if postgres {
		if tuple == "" {
			t.Fatal("locked-row tuple identity is required")
		}
		want.Context = `while locking tuple ` + tuple + ` in relation "` + relation + `"`
	}
	AssertDiagnostic(t, err, &want)
}

func QueryTableCreateLockExec(t *testing.T, p *Pair, b Backend, conn *sql.Conn, q, code, message, relation, tuple string) {
	t.Helper()
	ctx := context.WithValue(p.Ctx, RecorderKey{}, b.Recorder)
	result, err := conn.ExecContext(ctx, q)
	if result != nil {
		t.Fatal("failed lock execution exposed result")
	}
	QueryTableCreateLockDiagnostic(t, err, b.PG, code, message, relation, tuple)
}
