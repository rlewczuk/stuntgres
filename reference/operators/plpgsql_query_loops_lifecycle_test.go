package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLQueryLoopsLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, target, source, body, code, message, action string
	}{
		{"source", "record", "SELECT g AS i,100/(100-g) AS probe FROM generate_series(1,100) AS g", "n:=ql_put(r.i);", "22012", "division by zero", "FOR over SELECT rows"},
		{"assignment", "ql_atomic_row%ROWTYPE", "SELECT CASE WHEN g=100 THEN 'bad' ELSE g::text END AS i FROM generate_series(1,100) AS g", "n:=ql_put(r.i);", "22P02", `invalid input syntax for type integer: "bad"`, "FOR over SELECT rows"},
		{"body", "record", "SELECT g AS i FROM generate_series(1,100) AS g ORDER BY g", "n:=ql_put(r.i); n:=100/(100-r.i);", "22012", "division by zero", "assignment"},
		{"dml_body", "record", "INSERT INTO ql_atomic_data SELECT g FROM generate_series(1,100) AS g RETURNING i", "n:=ql_put(r.i); n:=100/(100-r.i);", "22012", "division by zero", "assignment"},
	} {
		t.Run("atomic_"+tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c,
					"CREATE TABLE ql_atomic_marker(i integer)", "CREATE TABLE ql_atomic_data(i integer)", "CREATE TABLE ql_atomic_row(i integer)",
					"CREATE FUNCTION ql_put(x integer) RETURNS integer LANGUAGE SQL AS 'INSERT INTO ql_atomic_marker VALUES(x); SELECT x'",
					"CREATE FUNCTION ql_atomic_run() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r "+tc.target+"; n integer:=0; BEGIN n:=ql_put(0); FOR r IN "+tc.source+" LOOP "+tc.body+" END LOOP; RETURN n; END$$")
				want := plpgsqlRuntimeDiagnostic(tc.code, tc.message, "ql_atomic_run()", tc.action)
				plpgsqlError(t, p, c, "SELECT ql_atomic_run() AS v", true, want)
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM ql_atomic_marker", "INT8", int64(0))
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM ql_atomic_data", "INT8", int64(0))
				userOperatorExec(t, p, b, c, "DROP FUNCTION ql_atomic_run()", "CREATE FUNCTION ql_atomic_run() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r record; n integer:=0; BEGIN FOR r IN SELECT g AS i FROM generate_series(1,3) AS g ORDER BY g LOOP n:=ql_put(r.i); END LOOP; RETURN n; END$$")
				for _, prepared := range []bool{false, true} {
					s := fixture.Select("SELECT ql_atomic_run() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT i FROM ql_atomic_marker ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ql_atomic_marker", 3))
				}
			})
		})
	}
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel_direct", true: "cancel_held"}[prepared], func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				source := "WITH q AS (SELECT 1 AS i) SELECT i,pg_sleep(30) AS wait FROM q"
				userOperatorExec(t, p, b, c, "CREATE FUNCTION ql_cancel() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r record; BEGIN FOR r IN "+source+" LOOP END LOOP; RETURN 1; END$$")
				q := "SELECT ql_cancel() AS v"
				var stmt *sql.Stmt
				if prepared {
					var err error
					stmt, err = c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				wantContext := "PL/pgSQL function ql_cancel() line 1 at FOR over SELECT rows"
				fixture.SleepActiveCancelWithContext(t, p, b, c, q, wantContext, nil, func(ctx context.Context) fixture.RelationOutcome {
					var rows *sql.Rows
					var err error
					if stmt != nil {
						rows, err = stmt.QueryContext(ctx)
					} else {
						rows, err = c.QueryContext(ctx, q)
					}
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				plpgsqlValue(t, p, b, c, "SELECT 1 AS v", "INT4", int64(1))
				if !b.PG {
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					rows, err := c.QueryContext(ctx, q)
					if rows != nil {
						rows.Close()
					}
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("pre-cancel error=%v, want context.Canceled", err)
					}
				}
			})
		})
	}
	t.Run("held_stored_two_generations", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, initial *sql.Conn) {
			userOperatorExec(t, p, b, initial,
				"CREATE TABLE ql_saved_rows(i integer,t text)",
				"CREATE FUNCTION ql_saved(take_row boolean) RETURNS text LANGUAGE plpgsql AS $$DECLARE r ql_saved_rows%ROWTYPE; s record; result text; BEGIN FOR r IN SELECT 7,'ok'::text WHERE take_row LOOP END LOOP; result:=coalesce(r.i::text,'NULL')||':'||coalesce(r.t,'NULL'); FOR s IN SELECT result AS label LOOP END LOOP; RETURN s.label; END$$",
				"CREATE VIEW ql_saved_view AS SELECT ql_saved(true) AS v",
				"CREATE TABLE ql_saved_defaults(id integer,v text DEFAULT ql_saved(true))")
			fixture.RunStep(t, p.Ctx, initial, b.Recorder, fixture.Exec("INSERT INTO ql_saved_defaults(id) VALUES(1)", 1))
			// DB-owned original statement survives releasing the pinned session and
			// both native replacements. No SQL handles borrow a control connection.
			if err := initial.Close(); err != nil {
				t.Fatal(err)
			}
			q := "SELECT ql_saved($1) AS v"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			check := func() {
				c, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				plpgsqlValue(t, p, b, c, "SELECT v FROM ql_saved_view", "TEXT", "7:ok")
				plpgsqlValue(t, p, b, c, "SELECT v FROM ql_saved_defaults WHERE id=1", "TEXT", "7:ok")
				for _, tc := range []struct {
					input bool
					want  string
				}{{true, "7:ok"}, {false, "NULL:NULL"}, {true, "7:ok"}} {
					s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.input)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				}
				if err := c.Close(); err != nil {
					t.Fatal(err)
				}
				for _, tc := range []struct {
					input bool
					want  string
				}{{true, "7:ok"}, {false, "NULL:NULL"}, {true, "7:ok"}} {
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.input))
				}
			}
			check()
			for range 2 {
				if !b.PG {
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
				}
				check()
			}
		})
	})
}
