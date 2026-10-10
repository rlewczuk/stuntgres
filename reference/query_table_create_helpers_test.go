package reference_test

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
)

func queryTableCreateSession(t *testing.T, p *referencePair, b referenceBackend) (*sql.Conn, func(referenceStep)) {
	t.Helper()
	conn, err := b.db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	return conn, func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
}

func queryTableCreateAbsent(t *testing.T, run func(referenceStep), postgres bool, name string) {
	t.Helper()
	run(referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='"+name+"'", []string{"relname"}, []string{"NAME"}, nil))
	// Native has no pg_type SQL provider. Private publication tests cover its
	// actual type registry; this companion checks PostgreSQL's associated row type.
	if postgres {
		run(referenceSelect("SELECT typname FROM pg_catalog.pg_type WHERE typname='"+name+"'", []string{"typname"}, []string{"NAME"}, nil))
	}
}

func queryTableCreateSource(run func(referenceStep)) {
	run(referenceExec("CREATE TABLE qc_source(i INTEGER,s TEXT)", 0))
	run(referenceExec("INSERT INTO qc_source VALUES(1,'a'),(1,'a'),(2,NULL)", 3))
}

func queryTableCreateBag(name string) referenceStep {
	s := referenceSelect("SELECT i,s FROM "+name, []string{"i", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}})
	s.bag = true
	return s
}

// A genuine PG tuple identity is read from the locked row, never pinned to an
// allocator-dependent block/offset constant. Native exposes no physical ctid.
func queryTableCreateTuple(t *testing.T, ctx context.Context, conn *sql.Conn, postgres bool, relation string) string {
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

func queryTableCreateLockDiagnostic(t *testing.T, err error, postgres bool, code, message, relation, tuple string) {
	t.Helper()
	want := controlsReferenceDiagnostic(code, message)
	if postgres {
		if tuple == "" {
			t.Fatal("locked-row tuple identity is required")
		}
		want.Context = `while locking tuple ` + tuple + ` in relation "` + relation + `"`
	}
	referenceAssertDiagnostic(t, err, &want)
}

func queryTableCreateLockExec(t *testing.T, p *referencePair, b referenceBackend, conn *sql.Conn, q, code, message, relation, tuple string) {
	t.Helper()
	ctx := context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder)
	result, err := conn.ExecContext(ctx, q)
	if result != nil {
		t.Fatal("failed lock execution exposed result")
	}
	queryTableCreateLockDiagnostic(t, err, b.pg, code, message, relation, tuple)
}
