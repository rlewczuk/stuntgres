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

func sqlExecutionPersistSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	userOperatorExec(t, p, b, c,
		"CREATE TABLE sx_saved_data(id integer,t text)",
		"CREATE TYPE sx_saved_pair AS (n integer,t text)",
		"CREATE PROCEDURE sx_saved_sql(INOUT x integer) LANGUAGE SQL AS 'SELECT x+1'",
		"CREATE PROCEDURE sx_saved_pl(INOUT x integer) LANGUAGE plpgsql AS $$BEGIN CALL sx_saved_sql(x); END$$",
		"CREATE PROCEDURE sx_saved_default(OUT answer integer,IN x integer DEFAULT 4) LANGUAGE SQL AS 'SELECT x'",
		"CREATE PROCEDURE sx_saved_variadic(OUT answer integer,VARIADIC xs integer[]) LANGUAGE SQL AS 'SELECT xs[1]+xs[2]'",
		"CREATE FUNCTION sx_saved(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN INSERT INTO sx_saved_data VALUES(100,'local'); UPDATE sx_saved_data SET t='changed' WHERE id=100; DELETE FROM sx_saved_data WHERE id=100; PERFORM 1 WHERE false; CALL sx_saved_pl(x); RETURN x; END$$",
		"CREATE FUNCTION sx_saved_fields() RETURNS text LANGUAGE plpgsql AS $$DECLARE r sx_saved_pair:=ROW(4,'keep'); a integer; v integer; BEGIN CALL sx_saved_pl(r.n); CALL sx_saved_default(answer=>a); CALL sx_saved_variadic(v,30,7); RETURN r.n||':'||r.t||':'||a||':'||v||':'||FOUND; END$$",
		"CREATE FUNCTION sx_saved_detached() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer:=7; BEGIN CALL sx_saved_sql(x); RETURN x; END$$",
		"CREATE VIEW sx_saved_view AS SELECT sx_saved_fields() AS v",
		"CREATE TABLE sx_saved_defaults(id integer,v integer DEFAULT sx_saved(4))")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO sx_saved_data VALUES(1,'saved')", 1))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO sx_saved_defaults(id) VALUES(1)", 1))
}

func sqlExecutionPersistCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, original *sql.Stmt) {
	t.Helper()
	q := "SELECT sx_saved($1::integer) AS v"
	for _, tc := range []struct{ in, out any }{{4, int64(5)}, {8, int64(9)}, {nil, nil}} {
		sqlExecutionValue(t, p, b, c, q, "INT4", tc.out, tc.in)
		if original != nil {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: original, SQL: q}, b.Recorder, sqlExecutionStep(b, q, []string{"v"}, []string{"INT4"}, [][]any{{tc.out}}, tc.in))
		}
	}
	for _, q := range []string{"SELECT sx_saved_fields() AS v", "SELECT v||'' AS v FROM sx_saved_view"} {
		sqlExecutionValue(t, p, b, c, q, "TEXT", "5:keep:4:37:false")
	}
	sqlExecutionValue(t, p, b, c, "SELECT sx_saved_detached() AS v", "INT4", int64(8))
	sqlExecutionValue(t, p, b, c, "SELECT v+0 AS v FROM sx_saved_defaults WHERE id=1", "INT4", int64(5))
	sqlExecutionBoth(t, p, b, c, "SELECT id+0 AS id,t||'' AS t FROM sx_saved_data ORDER BY id", []string{"id", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "saved"}})
}

func TestReferencePLpgSQLSQLExecutionPersistence(t *testing.T) {
	sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlExecutionPersistSetup(t, p, b, c)
		q := "SELECT sx_saved($1::integer) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		// Preparing the original handle does not run its embedded DML.
		sqlExecutionCount(t, p, b, c, "sx_saved_data WHERE id=100", 0)
		check := func(conn *sql.Conn, backend fixture.Backend) {
			var held *sql.Stmt
			if conn == c {
				held = stmt
			}
			sqlExecutionPersistCheck(t, p, backend, conn, held)
		}
		check(c, b)
		if b.PG {
			return
		} // Only native controls replace engines.
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE FUNCTION sx_saved_detached() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer:=6; BEGIN CALL sx_saved_sql(x); RETURN x; END$$")
		retained, err := c.QueryContext(p.Ctx, "SELECT sx_saved_detached() AS v")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE FUNCTION sx_saved_detached() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer:=7; BEGIN CALL sx_saved_sql(x); RETURN x; END$$")
		var emitted []byte
		for range 2 {
			emitted = parametersNativeReplayRound(t, p, b, c, check)
			if p.Postgres != nil {
				func() {
					pg, err := p.Postgres.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer pg.Close()
					if _, err = pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public; GRANT USAGE ON SCHEMA public TO PUBLIC"); err != nil {
						t.Fatal(err)
					}
					if _, err = pg.ExecContext(p.Ctx, string(emitted)); err != nil {
						t.Fatal("unchanged embedded-SQL dump replay", err)
					}
					if _, err = pg.ExecContext(p.Ctx, "SET check_function_bodies=true"); err != nil {
						t.Fatal(err)
					}
					check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
					if _, err = pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
						t.Fatal(err)
					}
				}()
			}
		}
		sqlExecutionMetadata(b, []string{"v"}, []string{"INT4"})(t, retained)
		if !retained.Next() {
			t.Fatal("detached row lost", retained.Err())
		}
		var old any
		if err := retained.Scan(&old); err != nil || fixture.Normalize(old) != int64(7) {
			t.Fatal(old, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("detached cardinality", retained.Err())
		}
		retained.Close()
		connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/fresh")
		if err != nil {
			t.Fatal(err)
		}
		raw := sql.OpenDB(connector)
		defer raw.Close()
		recorded, rec := fixture.RecordedNativePool(t, connector)
		fixture.RunStep(t, p.Ctx, recorded, rec, fixture.Exec("CREATE TYPE sx_destination_pressure AS (n integer)", 0))
		if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		clone, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer clone.Close()
			backend := fixture.Backend{DB: recorded, Recorder: rec}
			cloneStmt, err := clone.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer cloneStmt.Close()
			sqlExecutionPersistCheck(t, p, backend, clone, cloneStmt)
			fixture.RunStep(t, p.Ctx, clone, rec, fixture.Exec("UPDATE sx_saved_data SET t='clone'", 1))
		}()
		check(c, b) // Fresh Driver state cannot mutate the original engine.
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		bad := before.String() + "; CREATE TABLE sx_restore_prefix(i integer); CREATE FUNCTION sx_restore_bad() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM 1+; RETURN 1; END$$;"
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("late invalid Restore accepted")
		}
		check(c, b)
		sqlExecutionCount(t, p, b, c, "pg_class WHERE relname='sx_restore_prefix'", 0)
		sqlExecutionCount(t, p, b, c, "pg_proc WHERE proname='sx_restore_bad'", 0)
		if err := stuntgres.Restore(p.Ctx, p.Native, &plpgsqlFailReader{data: before.Bytes()}); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal("Restore read error", err)
		}
		check(c, b)
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(before.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal("Restore cancellation", err)
		}
		check(c, b)
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Error("failed controls changed persistent routines/data")
		}
	})
}
