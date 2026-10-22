package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoutineSQLBodiesNestedPersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "body_nested")
	sqlBodiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE SCHEMA body_nested",
			"CREATE TABLE body_nested.counter(v integer)",
			"CREATE FUNCTION body_nested.read(x integer) RETURNS integer RETURN (SELECT (SELECT x+1))",
			"CREATE FUNCTION body_nested.write(x integer) RETURNS integer BEGIN ATOMIC UPDATE body_nested.counter SET v=(SELECT (SELECT x+1)) RETURNING (SELECT v+(SELECT x)); END",
			"CREATE TABLE body_nested.soft_source(v integer)",
			"CREATE FUNCTION body_nested.soft() RETURNS integer LANGUAGE SQL AS $$SELECT v FROM body_nested.soft_source$$")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO body_nested.counter VALUES(0)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO body_nested.soft_source VALUES(23)", 1))
		check := func(conn *sql.Conn, backend fixture.Backend) {
			t.Helper()
			routineValue(t, p, backend, conn, "SELECT body_nested.read(4) AS v", "v", "INT4", int64(5))
			routineValue(t, p, backend, conn, "SELECT body_nested.write(4) AS v", "v", "INT4", int64(9))
			routineValueWithNativeNullability(t, p, backend, conn, "SELECT v AS v FROM body_nested.counter", "v", "INT4", int64(5), true, true)
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Error("DROP TABLE body_nested.counter", "2BP01"))
			routineValue(t, p, backend, conn, "SELECT body_nested.soft() AS v", "v", "INT4", int64(23))
			// Text dependencies remain soft after every replacement. Rebinding is
			// observed, not inferred from function existence or dump spelling.
			routineExec(t, p, backend, conn, "DROP TABLE body_nested.soft_source", "CREATE TABLE body_nested.soft_source(v integer)")
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("INSERT INTO body_nested.soft_source VALUES(29)", 1))
			routineValue(t, p, backend, conn, "SELECT body_nested.soft() AS v", "v", "INT4", int64(29))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("UPDATE body_nested.soft_source SET v=23", 1))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select(
				"SELECT DISTINCT refobjid::regclass::text AS relation,refobjsubid,deptype FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='body_nested.write(integer)'::regprocedure AND refclassid='pg_class'::regclass ORDER BY relation,refobjsubid,deptype",
				[]string{"relation", "refobjsubid", "deptype"}, []string{"TEXT", "INT4", "CHAR"},
				[][]any{{"body_nested.counter", int64(1), "n"}}))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select(
				"SELECT refobjid::regclass::text AS relation FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='body_nested.soft()'::regprocedure AND refclassid='pg_class'::regclass",
				[]string{"relation"}, []string{"TEXT"}, nil))
		}
		sqlBodiesReplayTwice(t, p, b, c, "body_nested", check, nil)
	})
}

// Native controls have no PostgreSQL equivalent. All SQL assertions are shared,
// including executing each unchanged native stream on the pinned server.
func sqlBodiesReplayTwice(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, schema string, check, finish func(*sql.Conn, fixture.Backend)) {
	t.Helper()
	check(c, b)
	if b.PG {
		if finish != nil {
			finish(c, b)
		}
		return
	}
	for range 2 {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(c, b)
		if p.Postgres == nil {
			continue
		}
		func() {
			pg, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer pg.Close()
			defer func() {
				if _, err := pg.ExecContext(p.Ctx, "SET check_function_bodies=true; DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
					t.Error(err)
				}
			}()
			if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
				t.Fatal(err)
			}
			if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("unchanged native stream replay", err)
			}
			check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
			if finish != nil {
				finish(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
			}
		}()
	}
	if finish != nil {
		finish(c, b)
	}
}
