package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLRaiseAssertLifecycle(t *testing.T) {
	t.Run("nested_diagnostics", func(t *testing.T) {
		for _, action := range []string{"RAISE EXCEPTION 'échec';", "ASSERT false,'échec';", "RAISE NOTICE '%',1/0;", "ASSERT false,1/0;"} {
			t.Run(action, func(t *testing.T) {
				plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					userOperatorExec(t, p, b, c,
						"CREATE FUNCTION raise_inner() RETURNS integer LANGUAGE plpgsql AS $$\nBEGIN\n-- é\n"+action+"\nRETURN 7;\nEND$$",
						"CREATE FUNCTION raise_outer() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN raise_inner(); END$$",
						"CREATE FUNCTION raise_sql() RETURNS integer LANGUAGE SQL AS $$SELECT 1; SELECT raise_outer()$$")
					code, message, at := "P0001", "échec", "RAISE"
					if action[0] == 'A' { code, at = "P0004", "ASSERT" }
					if action == "RAISE NOTICE '%',1/0;" || action == "ASSERT false,1/0;" { code, message = "22012", "division by zero" }
					want := fixture.ControlsReferenceDiagnostic(code, message)
					want.Context = "PL/pgSQL function raise_inner() line 4 at "+at
					if code == "22012" {
						want.Context = "PL/pgSQL expression \"1/0\"\n" + want.Context
					}
					plpgsqlError(t, p, c, "SELECT raise_inner() AS v", true, want)
					want.Context += "\nPL/pgSQL function raise_outer() line 1 at RETURN"
					plpgsqlError(t, p, c, "SELECT raise_outer() AS v", true, want)
					want.Context += "\nSQL function \"raise_sql\" statement 2"
					plpgsqlError(t, p, c, "SELECT raise_sql() AS v", true, want)
				})
			})
		}
	})
	for _, action := range []string{"RAISE EXCEPTION 'bad';", "ASSERT false,'bad';"} {
		t.Run(action, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c,
					"CREATE TABLE raise_atomic(v integer)",
					"CREATE FUNCTION raise_atomic_value(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN IF x=0 THEN "+action+" END IF; RETURN x; END$$")
				code, at := "P0001", "RAISE"
				if action[0] == 'A' { code, at = "P0004", "ASSERT" }
				want := plpgsqlRuntimeDiagnostic(code, "bad", "raise_atomic_value(integer)", at)
				plpgsqlError(t, p, c, "INSERT INTO raise_atomic VALUES(raise_atomic_value(1)),(raise_atomic_value(0))", false, want)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM raise_atomic ORDER BY v", []string{"v"}, []string{"INT4"}, nil))
				q := "SELECT raise_atomic_value($1) AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil { t.Fatal(err) }
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, x := range []int{1, 0, 2, 0, 1} {
					if x == 0 {
						rows, err := stmt.QueryContext(p.Ctx, x)
						if rows != nil { rows.Close(); t.Fatal("failure exposed rows") }
						fixture.AssertDiagnostic(t, err, &want)
					} else {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(x)}}, x))
					}
				}
				if !b.PG {
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					rows, err := stmt.QueryContext(ctx, 1)
					if rows != nil { rows.Close(); t.Fatal("canceled invocation exposed rows") }
					var state interface{ SQLState() string }
					if !errors.Is(err, context.Canceled) || errors.As(err, &state) { t.Fatalf("ordinary cancellation: %v", err) }
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}, 1))
				}
				userOperatorExec(t, p, b, c,
					"CREATE FUNCTION raise_put(x integer) RETURNS integer LANGUAGE SQL AS $$INSERT INTO raise_atomic VALUES(x) RETURNING v$$",
					"CREATE FUNCTION raise_protected() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN x:=raise_put(1); BEGIN x:=raise_put(2); "+action+" EXCEPTION WHEN SQLSTATE '"+code+"' THEN x:=raise_put(3); END; RETURN 7; END$$")
				for _, prepared := range []bool{false, true} {
					userOperatorExec(t, p, b, c, "TRUNCATE raise_atomic")
					s := fixture.Select("SELECT raise_protected() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM raise_atomic ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
				}
			})
		})
	}
	t.Run("stored_replay", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "raise_saved")
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			var records []fixture.Diagnostic
			_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &records)
			c, b.Recorder = noticeConn, recorder
			userOperatorExec(t, p, b, c,
				"CREATE SCHEMA raise_saved",
				"CREATE SEQUENCE raise_saved.operands",
				"CREATE FUNCTION raise_saved.pick(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN ASSERT x>0,'positive'; RAISE NOTICE 'value=%',x; RETURN x; END$$",
				"CREATE FUNCTION raise_saved.positive(x integer) RETURNS boolean LANGUAGE plpgsql AS $$BEGIN ASSERT x>0,'positive'; RETURN true; END$$",
				"CREATE FUNCTION raise_saved.lazy() RETURNS integer LANGUAGE plpgsql AS $$BEGIN ASSERT true,nextval('raise_saved.operands'); IF false THEN RAISE NOTICE '%',nextval('raise_saved.operands'); END IF; RETURN 7; END$$",
				"CREATE FUNCTION raise_saved.effects() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RAISE DEBUG '%',nextval('raise_saved.operands'); ASSERT nextval('raise_saved.operands')>0,nextval('raise_saved.operands'); RETURN 7; END$$",
				"CREATE VIEW raise_saved.captured AS SELECT raise_saved.pick(7) AS v",
				"CREATE TABLE raise_saved.data(id integer,v integer DEFAULT raise_saved.pick(7),CHECK(raise_saved.positive(v)))")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO raise_saved.data(id) VALUES(1)", 1))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT nextval('raise_saved.operands') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
			q := "SELECT raise_saved.pick($1) AS v"
			original, err := c.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal(err) }
			defer original.Close()
			effects, err := c.PrepareContext(p.Ctx, "SELECT raise_saved.effects() AS v")
			if err != nil { t.Fatal(err) }
			defer effects.Close()
			check := func(conn *sql.Conn, backend fixture.Backend) {
				plpgsqlValue(t, p, backend, conn, "SELECT last_value AS v FROM raise_saved.operands", "INT8", int64(1))
				plpgsqlValue(t, p, backend, conn, "SELECT raise_saved.lazy() AS v", "INT4", int64(7))
				plpgsqlValue(t, p, backend, conn, "SELECT v FROM raise_saved.captured", "INT4", int64(7))
				plpgsqlValue(t, p, backend, conn, "SELECT v FROM raise_saved.data WHERE id=1", "INT4", int64(7))
				parametersValue(t, p, backend, conn, q, "v", "INT4", int64(8), 8)
				want := plpgsqlRuntimeDiagnostic("P0004", "positive", "raise_saved.pick(integer)", "ASSERT")
				plpgsqlError(t, p, conn, "SELECT raise_saved.pick(0) AS v", true, want)
				want = plpgsqlRuntimeDiagnostic("P0004", "positive", "raise_saved.positive(integer)", "ASSERT")
				plpgsqlError(t, p, conn, "INSERT INTO raise_saved.data VALUES(2,-1)", false, want)
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT count(*) AS v FROM raise_saved.data", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
				if conn == c && backend.PG == b.PG {
					held := fixture.HeldStatement{Executor: conn, Statement: original, SQL: q}
					records = nil
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}, 9))
					raiseAssertNotices(t, records, []fixture.Diagnostic{raiseAssertNotice("NOTICE", "00000", "value=9", "raise_saved.pick(integer)")})
					records = nil
					userOperatorExec(t, p, backend, conn, "SET plpgsql.check_asserts TO off")
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}}, 0))
					raiseAssertNotices(t, records, []fixture.Diagnostic{raiseAssertNotice("NOTICE", "00000", "value=0", "raise_saved.pick(integer)")})
					userOperatorExec(t, p, backend, conn, "RESET plpgsql.check_asserts")
					rows, err := original.QueryContext(p.Ctx, 0)
					if rows != nil { rows.Close(); t.Fatal("re-enabled held assertion exposed rows") }
					want = plpgsqlRuntimeDiagnostic("P0004", "positive", "raise_saved.pick(integer)", "ASSERT")
					fixture.AssertDiagnostic(t, err, &want)
					var v int
					if err := effects.QueryRowContext(p.Ctx).Scan(&v); err != nil || v != 7 { t.Fatalf("held effects value=%d err=%v", v, err) }
					plpgsqlValue(t, p, backend, conn, "SELECT last_value AS v FROM raise_saved.operands", "INT8", int64(3))
					userOperatorExec(t, p, backend, conn, "ALTER SEQUENCE raise_saved.operands RESTART WITH 1")
					fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT nextval('raise_saved.operands') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
				}
			}
			parametersReplay(t, p, b, c, "raise_saved", check)
		})
	})
}
