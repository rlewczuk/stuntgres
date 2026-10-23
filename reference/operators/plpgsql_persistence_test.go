package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCorePersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pl_saved")
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA pl_saved",
			"CREATE FUNCTION pl_saved.pick(x bigint) RETURNS bigint LANGUAGE plpgsql AS $$BEGIN RETURN x+1000; END$$",
			"CREATE VIEW pl_saved.captured AS SELECT pl_saved.pick(1) AS v",
			"CREATE FUNCTION pl_saved.sql_frozen() RETURNS bigint RETURN pl_saved.pick(1)",
			"CREATE FUNCTION pl_saved.sql_quoted() RETURNS bigint LANGUAGE SQL AS 'SELECT pl_saved.pick(1::bigint)'",
			"CREATE FUNCTION pl_saved.pick(x integer) RETURNS bigint LANGUAGE plpgsql AS $$BEGIN RETURN x+2000; END$$",
			"CREATE FUNCTION pl_saved.add1(x integer DEFAULT 4) RETURNS integer LANGUAGE plpgsql AS $$DECLARE y x%TYPE := x; BEGIN y := y+1; RETURN y; END$$",
			"CREATE FUNCTION pl_saved.sql_leaf(x integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1+1'",
			"CREATE FUNCTION pl_saved.pl_outer(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN pl_saved.sql_leaf(x); END$$",
			"CREATE TABLE pl_saved.type_source(x varchar(3))",
			"CREATE FUNCTION pl_saved.column_type() RETURNS boolean LANGUAGE plpgsql AS $$DECLARE x pl_saved.type_source.x%TYPE; BEGIN RETURN x IS NULL; END$$",
			"CREATE FUNCTION pl_saved.row_type() RETURNS boolean LANGUAGE plpgsql AS $$DECLARE r pl_saved.type_source%ROWTYPE; BEGIN RETURN r.x IS NULL; END$$",
			"CREATE TABLE pl_saved.data(id integer,v integer DEFAULT pl_saved.add1() CHECK(pl_saved.add1(v)>0))")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO pl_saved.data(id) VALUES(1)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Error("INSERT INTO pl_saved.data VALUES(2,-2)", "23514"))
		check := func(conn *sql.Conn, backend fixture.Backend) {
			for _, q := range []string{"SELECT v FROM pl_saved.captured", "SELECT pl_saved.sql_frozen() AS v", "SELECT pl_saved.sql_quoted() AS v"} {
				plpgsqlValue(t, p, backend, conn, q, "INT8", int64(1001))
			}
			plpgsqlValue(t, p, backend, conn, "SELECT pl_saved.pick(1) AS v", "INT8", int64(2001))
			plpgsqlValue(t, p, backend, conn, "SELECT pl_saved.pl_outer(4) AS v", "INT4", int64(5))
			plpgsqlValue(t, p, backend, conn, "SELECT v FROM pl_saved.data WHERE id=1", "INT4", int64(5))
			plpgsqlValue(t, p, backend, conn, "SELECT pl_saved.column_type() AS v", "BOOL", true)
			plpgsqlValue(t, p, backend, conn, "SELECT pl_saved.row_type() AS v", "BOOL", true)
			parametersHeld(t, p, backend, conn, "SELECT pl_saved.add1($1) AS v", "INT4", [][]any{{4}, {9}}, []any{int64(5), int64(10)})
		}
		parametersReplay(t, p, b, c, "pl_saved", check)
		if b.PG {
			return
		}
		q := "SELECT pl_saved.add1($1) AS v"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		rows, err := c.QueryContext(p.Ctx, "SELECT v FROM pl_saved.captured")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		bad := dump.String() + "; CREATE TABLE public.pl_bad_prefix(x integer); CREATE FUNCTION public.pl_invalid() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 1+; END$$;"
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("late invalid PL restore accepted")
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, &plpgsqlFailReader{data: []byte(dump.String())}); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("reader error: %v", err)
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled restore: %v", err)
		}
		check(c, b)
		plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_class WHERE relname='pl_bad_prefix'", "INT8", int64(0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}, 4))
		if !rows.Next() {
			t.Fatal("old rows lost", rows.Err())
		}
		var v int64
		if err := rows.Scan(&v); err != nil || v != 1001 {
			t.Fatal(v, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("old row cardinality", rows.Err())
		}
	})
}

type plpgsqlFailReader struct{ data []byte }

func (r *plpgsqlFailReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}
