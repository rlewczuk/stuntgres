//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"
)

// Oracle-only cases assert independently specified release behavior and remain
// runnable while the native implementation is red.
func sequenceProbeConn(t *testing.T, p *referencePair) *sql.Conn {
	t.Helper()
	c, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func sequenceProbeExec(t *testing.T, ctx context.Context, c *sql.Conn, q string) {
	t.Helper()
	r, err := c.ExecContext(ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	n, err := r.RowsAffected()
	if err != nil || n != 0 {
		t.Fatal(q, n, err)
	}
}
func sequenceProbeValue(t *testing.T, ctx context.Context, c *sql.Conn, q string, want int64) {
	t.Helper()
	var got int64
	if err := c.QueryRowContext(ctx, q).Scan(&got); err != nil || got != want {
		t.Fatalf("%s got=%d want=%d err=%v", q, got, want, err)
	}
}
func sequenceProbeError(t *testing.T, ctx context.Context, c *sql.Conn, q string, want *referenceDiagnostic) {
	t.Helper()
	_, err := c.ExecContext(ctx, q)
	if err == nil {
		t.Fatal("expected error", q)
	}
	if want == nil {
		expected, ok := sequenceExpectedDiagnostic(q)
		if !ok {
			t.Fatal("missing independent diagnostic", q)
		}
		want = &expected
	}
	referenceAssertDiagnostic(t, err, want)
}

func TestReferenceSequenceProbeDefaultsSessionsSetval(t *testing.T) {
	p := newReferencePair(t)
	a, b := sequenceProbeConn(t, p), sequenceProbeConn(t, p)
	sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE s")
	sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE t")
	for _, tc := range []struct{ q, msg string }{{"SELECT currval('s')", `currval of sequence "s" is not yet defined in this session`}, {"SELECT lastval()", "lastval is not yet defined in this session"}} {
		want := controlsReferenceDiagnostic("55000", tc.msg)
		sequenceProbeError(t, p.ctx, a, tc.q, &want)
	}
	stmt, err := a.PrepareContext(p.ctx, "SELECT nextval('s')")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, want := range []int64{1, 2} {
		rows, err := stmt.QueryContext(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		names, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(names, []string{"nextval"}) {
			t.Fatal(names, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT8" {
			t.Fatal(types, err)
		}
		var got any
		if !rows.Next() {
			t.Fatal("missing row")
		}
		if err = rows.Scan(&got); err != nil || got != want {
			t.Fatal(got, want, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("extra/error rows", rows.Err())
		}
		rows.Close()
	}
	sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 3)
	sequenceProbeValue(t, p.ctx, a, "SELECT currval('s')", 2)
	sequenceProbeValue(t, p.ctx, a, "SELECT setval('s',42)", 42)
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 43)
	sequenceProbeValue(t, p.ctx, a, "SELECT setval('s',42,false)", 42)
	sequenceProbeValue(t, p.ctx, a, "SELECT currval('s')", 43)
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 42)
	sequenceProbeValue(t, p.ctx, a, "SELECT setval('t',80,true)", 80)
	sequenceProbeValue(t, p.ctx, a, "SELECT lastval()", 42)
	sequenceProbeValue(t, p.ctx, a, "SELECT setval('s',90,true)", 90)
	sequenceProbeValue(t, p.ctx, a, "SELECT lastval()", 90)
	sequenceProbeExec(t, p.ctx, a, "BEGIN")
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 91)
	sequenceProbeExec(t, p.ctx, a, "ROLLBACK")
	sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 92)
	sequenceProbeExec(t, p.ctx, a, "BEGIN")
	sequenceProbeValue(t, p.ctx, a, "SELECT setval('s',100,false)", 100)
	sequenceProbeExec(t, p.ctx, a, "ROLLBACK")
	sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 100)
	for _, q := range []string{"SELECT nextval(NULL)", "SELECT currval(NULL)", "SELECT setval(NULL,42)", "SELECT setval('s',NULL)", "SELECT setval('s',42,NULL)"} {
		var got any
		if err := a.QueryRowContext(p.ctx, q).Scan(&got); err != nil || got != nil {
			t.Fatal(q, got, err)
		}
	}
}

func TestReferenceSequenceProbeCacheOptionsRow(t *testing.T) {
	p := newReferencePair(t)
	a, b := sequenceProbeConn(t, p), sequenceProbeConn(t, p)
	sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE s CACHE 10")
	var last, log int64
	var called bool
	if err := a.QueryRowContext(p.ctx, "SELECT * FROM s").Scan(&last, &log, &called); err != nil || last != 1 || log != 0 || called {
		t.Fatal(last, log, called, err)
	}
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 1)
	sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 11)
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 2)
	if err := a.QueryRowContext(p.ctx, "SELECT * FROM s").Scan(&last, &log, &called); err != nil || last != 20 || log != 22 || !called {
		t.Fatal(last, log, called, err)
	}
	sequenceProbeValue(t, p.ctx, b, "SELECT setval('s',50)", 50)
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 3)
	sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 51)
	sequenceProbeExec(t, p.ctx, b, "ALTER SEQUENCE s INCREMENT BY 2")
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 62)
	sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE bounded AS SMALLINT INCREMENT BY 2 MINVALUE 2 MAXVALUE 5 START WITH 2 CACHE 10 CYCLE")
	for _, want := range []int64{2, 4, 2, 4, 2} {
		sequenceProbeValue(t, p.ctx, a, "SELECT nextval('bounded')", want)
	}
	sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE typed AS SMALLINT")
	sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE typed AS INTEGER")
	sequenceProbeValue(t, p.ctx, a, "SELECT seqmax FROM pg_sequence WHERE seqrelid='typed'::regclass", 2147483647)
	sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE explicit_bound AS SMALLINT MAXVALUE 10")
	sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE explicit_bound AS INTEGER")
	sequenceProbeValue(t, p.ctx, a, "SELECT seqmax FROM pg_sequence WHERE seqrelid='explicit_bound'::regclass", 10)
	sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE huge CACHE 9223372036854775807")
	// PG reserves cache values iteratively; witness its maximal metadata without
	// asking the release oracle to perform an INT64_MAX-length allocation loop.
	sequenceProbeValue(t, p.ctx, a, "SELECT seqcache FROM pg_sequence WHERE seqrelid='huge'::regclass", 9223372036854775807)
	sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE bounded_cache CACHE 1000")
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('bounded_cache')", 1)
	sequenceProbeValue(t, p.ctx, a, "SELECT last_value FROM bounded_cache", 1000)
}

func TestReferenceSequenceProbeTransactionalRestart(t *testing.T) {
	p := newReferencePair(t)
	a, b := sequenceProbeConn(t, p), sequenceProbeConn(t, p)
	sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE s")
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 1)
	var pid int
	if err := b.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	// Each operation must queue behind SRX, not merely nextval cache misses.
	for _, q := range []string{"SELECT nextval('s')", "SELECT currval('s')", "SELECT lastval()", "SELECT setval('s',50)"} {
		sequenceProbeExec(t, p.ctx, a, "BEGIN")
		sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE s RESTART WITH 100")
		done := make(chan error, 1)
		go func() { var v int64; done <- b.QueryRowContext(p.ctx, q).Scan(&v) }()
		referenceLockWait(t, p.ctx, pid)
		// Use an independent connection: B is occupied by the queued command.
		var visible int64
		if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT last_value FROM public.s").Scan(&visible); err != nil {
			t.Fatal("SRX incorrectly blocks row reader", err)
		}
		sequenceProbeExec(t, p.ctx, a, "ROLLBACK")
		select {
		case err := <-done:
			if err != nil {
				got := referenceExtractDiagnostic(t, err)
				if got.Code != "55000" {
					t.Fatal(err)
				}
			}
		case <-time.After(5 * time.Second):
			t.Fatal("restart waiter did not complete")
		}
	}
	sequenceProbeExec(t, p.ctx, a, "BEGIN")
	sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE s RESTART 100")
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 100)
	sequenceProbeExec(t, p.ctx, a, "ROLLBACK")
	sequenceProbeValue(t, p.ctx, a, "SELECT currval('s')", 100)
	sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 51)
	sequenceProbeExec(t, p.ctx, a, "BEGIN")
	sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE s RESTART 100")
	sequenceProbeExec(t, p.ctx, a, "COMMIT")
	sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 100)
	sequenceProbeExec(t, p.ctx, a, "BEGIN")
	sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE transient")
	sequenceProbeExec(t, p.ctx, a, "ROLLBACK")
	sequenceProbeError(t, p.ctx, b, "SELECT nextval('transient')", nil)
	sequenceProbeExec(t, p.ctx, a, "BEGIN")
	sequenceProbeExec(t, p.ctx, a, "DROP SEQUENCE s")
	sequenceProbeExec(t, p.ctx, a, "ROLLBACK")
	sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 101)
}

func TestReferenceSequenceProbeTemporaryOwnershipForms(t *testing.T) {
	p := newReferencePair(t)
	a, b := sequenceProbeConn(t, p), sequenceProbeConn(t, p)
	sequenceProbeExec(t, p.ctx, a, "CREATE TABLE t(a INT,b INT,c INT)")
	sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE owned OWNED BY t.c")
	sequenceProbeExec(t, p.ctx, a, "ALTER TABLE t DROP COLUMN b")
	sequenceProbeExec(t, p.ctx, a, "ALTER TABLE t DROP COLUMN c RESTRICT")
	sequenceProbeError(t, p.ctx, a, "SELECT nextval('owned')", nil)
	sequenceProbeExec(t, p.ctx, a, "CREATE UNLOGGED SEQUENCE s START 40")
	sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE s SET LOGGED")
	sequenceProbeExec(t, p.ctx, a, "CREATE TEMPORARY SEQUENCE s START 7")
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 7)
	sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 40)
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('pg_temp.s')", 8)
	sequenceProbeExec(t, p.ctx, a, "BEGIN")
	sequenceProbeExec(t, p.ctx, a, "DROP SEQUENCE pg_temp.s")
	sequenceProbeExec(t, p.ctx, a, "ROLLBACK")
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 9)
	sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE pg_temp.s RENAME TO local_s")
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('local_s')", 10)
	sequenceProbeExec(t, p.ctx, a, "CREATE SCHEMA moved")
	defer sequenceProbeExec(t, p.ctx, a, "DROP SCHEMA moved CASCADE")
	sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE detached OWNED BY t.a")
	sequenceProbeError(t, p.ctx, a, "ALTER SEQUENCE detached SET SCHEMA moved", nil)
	sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE detached OWNED BY NONE")
	sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE detached SET SCHEMA moved")
	sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE moved.detached RENAME TO renamed")
	sequenceProbeExec(t, p.ctx, a, "DROP SEQUENCE moved.renamed,public.s RESTRICT")
	sequenceProbeError(t, p.ctx, a, "DROP SEQUENCE t", nil)
}

func TestReferenceSequenceProbeDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	a := sequenceProbeConn(t, p)
	sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE s")
	sequenceProbeExec(t, p.ctx, a, "CREATE TABLE t(v INT)")
	cases := []struct{ q, code, msg string }{
		{"CREATE SEQUENCE bad INCREMENT 0", "22023", "INCREMENT must not be zero"},
		{"CREATE SEQUENCE bad CACHE 0", "22023", "CACHE (0) must be greater than zero"},
		{"CREATE SEQUENCE bad MINVALUE 5 MAXVALUE 5", "22023", "MINVALUE (5) must be less than MAXVALUE (5)"},
		{"CREATE SEQUENCE bad AS TEXT", "22023", "sequence type must be smallint, integer, or bigint"},
		{"CREATE SEQUENCE bad AS SMALLINT MAXVALUE 32768", "22023", "MAXVALUE (32768) is out of range for sequence data type smallint"},
		{"CREATE SEQUENCE bad START 0", "22023", "START value (0) cannot be less than MINVALUE (1)"},
		{"ALTER SEQUENCE s RESTART 0", "22023", "RESTART value (0) cannot be less than MINVALUE (1)"},
		{"SELECT setval('s',0)", "22003", `setval: value 0 is out of bounds for sequence "s" (1..9223372036854775807)`},
	}
	for _, tc := range cases {
		t.Run(tc.q, func(t *testing.T) {
			want := controlsReferenceDiagnostic(tc.code, tc.msg)
			sequenceProbeError(t, p.ctx, a, tc.q, &want)
		})
	}
	for _, q := range []string{
		"SELECT nextval('t')", "SELECT currval('t')", "SELECT setval('t',1)", "SELECT nextval('0')", "SELECT nextval('-')", "SELECT nextval('4294967295')", "SELECT nextval('a.b.c')", "SELECT nextval('bad..name')",
		"SELECT /*é*/ nextval('missing')", "CREATE SEQUENCE s", "CREATE SEQUENCE bad CACHE 1 CACHE 2", "CREATE SEQUENCE bad OWNED BY t", "CREATE SEQUENCE bad OWNED BY t.missing", "ALTER SEQUENCE t RESTART", "DROP SEQUENCE missing", "DROP SEQUENCE t", "ALTER SEQUENCE s AS SMALLINT MINVALUE -32769", "ALTER SEQUENCE s MAXVALUE 1",
	} {
		t.Run(q, func(t *testing.T) { sequenceProbeError(t, p.ctx, a, q, nil) })
	}
	// Prepared input diagnostics have a portal Context that a literal input lacks.
	stmt, err := a.PrepareContext(p.ctx, "SELECT nextval($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	var v int64
	err = stmt.QueryRowContext(p.ctx, "missing").Scan(&v)
	if err == nil {
		t.Fatal("missing parameter accepted")
	}
	parameterWant := controlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
	parameterWant.Context = "unnamed portal parameter $1 = '...'"
	referenceAssertDiagnostic(t, err, &parameterWant)
	for _, q := range []string{"SELECT nextval('missing')", "SELECT /*é*/ nextval('missing')"} {
		stmt, err := a.PrepareContext(p.ctx, q)
		if stmt != nil {
			stmt.Close()
		}
		if err == nil {
			t.Fatal("missing literal Prepare accepted")
		}
		want := controlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
		want.Position = "16"
		if q == "SELECT /*é*/ nextval('missing')" {
			want.Position = "22"
		}
		referenceAssertDiagnostic(t, err, &want)
	}
}
