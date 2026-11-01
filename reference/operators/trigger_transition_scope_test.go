package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerTransitionScope(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE tt(id integer,v integer)", "CREATE TABLE tt_child(id integer,v integer)", "CREATE TABLE x(id integer,v integer)",
			"CREATE TABLE tt_scope(phase text,id integer,v integer)", "CREATE TABLE tt_scope_whole(value text)", "CREATE TABLE tt_scope_caught(code text,n bigint)",
			"CREATE FUNCTION tt_helper() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN (SELECT id FROM x); END$$",
			"CREATE FUNCTION tt_hidden_helper() RETURNS bigint LANGUAGE plpgsql AS $$BEGIN RETURN (SELECT count(*) FROM hidden); END$$",
			"CREATE FUNCTION tt_child_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_scope SELECT 'child',a.* FROM x a; RETURN NULL; END$$",
			"CREATE FUNCTION tt_outer_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_scope SELECT 'before',a.* FROM x a; INSERT INTO tt_scope SELECT 'alias',k,z FROM x a(k,z); INSERT INTO tt_scope_whole SELECT a::text FROM x a; INSERT INTO tt_scope SELECT 'qualified',a.* FROM public.x a; WITH x AS(SELECT 777 AS id,778 AS v) INSERT INTO tt_scope SELECT 'cte',id,v FROM x; INSERT INTO tt_scope VALUES('helper',tt_helper(),0); EXECUTE 'INSERT INTO tt_scope SELECT ''dynamic'',id,v FROM x'; INSERT INTO tt_child SELECT id+100,v+100 FROM x; INSERT INTO tt_scope SELECT 'after',a.* FROM x a; RETURN NULL; END$$",
			"CREATE FUNCTION tt_hidden_body() RETURNS trigger LANGUAGE plpgsql AS $$DECLARE code text; BEGIN BEGIN PERFORM tt_hidden_helper(); EXCEPTION WHEN undefined_table THEN GET STACKED DIAGNOSTICS code=RETURNED_SQLSTATE; END; INSERT INTO tt_scope_caught VALUES(code,(SELECT count(*) FROM hidden)); RETURN NULL; END$$",
			"CREATE TRIGGER c AFTER INSERT ON tt_child REFERENCING NEW TABLE x EXECUTE FUNCTION tt_child_body()",
			"CREATE TRIGGER a AFTER INSERT ON tt REFERENCING NEW TABLE x EXECUTE FUNCTION tt_outer_body()",
			"CREATE TRIGGER z AFTER INSERT ON tt REFERENCING NEW TABLE hidden EXECUTE FUNCTION tt_hidden_body()")
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		run(fixture.Exec("INSERT INTO x VALUES(900,901)", 1))
		run(fixture.Exec("INSERT INTO tt VALUES(1,10),(2,20)", 2))
		want := [][]any{{"qualified", int64(900), int64(901)}, {"cte", int64(777), int64(778)}, {"helper", int64(900), int64(0)}}
		for _, phase := range []string{"before", "alias", "dynamic", "after"} {
			want = append(want, []any{phase, int64(1), int64(10)}, []any{phase, int64(2), int64(20)})
		}
		want = append(want, []any{"child", int64(101), int64(110)}, []any{"child", int64(102), int64(120)})
		s := fixture.Select("SELECT phase,id,v FROM tt_scope", []string{"phase", "id", "v"}, []string{"TEXT", "INT4", "INT4"}, want)
		s.Bag = true
		run(s)
		run(fixture.Select("SELECT value FROM tt_scope_whole ORDER BY value", []string{"value"}, []string{"TEXT"}, [][]any{{"(1,10)"}, {"(2,20)"}}))
		run(fixture.Select("SELECT code,n FROM tt_scope_caught", []string{"code", "n"}, []string{"TEXT", "INT8"}, [][]any{{"42P01", int64(2)}}))
		missing := "SELECT * FROM hidden"
		d := fixture.ControlsReferenceDiagnostic("42P01", `relation "hidden" does not exist`)
		d.Position = "15"
		errStep := fixture.ErrorDiagnostic(missing, d)
		errStep.SelectRows = true
		run(errStep)
		run(fixture.Select("SELECT id,v FROM x", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(900), int64(901)}}))
		userOperatorExec(t, p, b, c, "TRUNCATE tt_scope,tt_scope_whole,tt_scope_caught")
		run(fixture.Exec("INSERT INTO tt VALUES(3,30)", 1))
		want = [][]any{{"qualified", int64(900), int64(901)}, {"cte", int64(777), int64(778)}, {"helper", int64(900), int64(0)}, {"child", int64(103), int64(130)}}
		for _, phase := range []string{"before", "alias", "dynamic", "after"} {
			want = append(want, []any{phase, int64(3), int64(30)})
		}
		s.Rows = want
		run(s)
		run(fixture.Select("SELECT code,n FROM tt_scope_caught", []string{"code", "n"}, []string{"TEXT", "INT8"}, [][]any{{"42P01", int64(1)}}))
	})
}

func TestReferenceTriggerTransitionReadOnly(t *testing.T) {
	cases := []struct{ name, command, message string }{
		{"insert", "INSERT INTO x VALUES(8)", `relation "x" cannot be the target of a modifying statement`},
		{"update", "UPDATE x SET id=8", `relation "x" cannot be the target of a modifying statement`},
		{"delete", "DELETE FROM x", `relation "x" cannot be the target of a modifying statement`},
		{"cte-target", "WITH a AS(DELETE FROM x RETURNING id) SELECT count(*) FROM a", `relation "x" cannot be the target of a modifying statement`},
		{"view", "CREATE VIEW tt_view AS SELECT * FROM x", `relation "x" does not exist`},
		{"materialized-view", "CREATE MATERIALIZED VIEW tt_view AS SELECT * FROM x", `transition table "x" cannot be referenced in a persistent object`},
		{"row-lock", "SELECT * FROM x FOR UPDATE OF x", "FOR UPDATE cannot be applied to a named tuplestore"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				userOperatorExec(t, p, b, c,
					"CREATE TABLE tt(id integer)", "CREATE TABLE x(id integer)", "CREATE TABLE tt_read_audit(id integer)",
					"CREATE FUNCTION tt_read() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_read_audit SELECT id FROM x; EXECUTE $command$"+tc.command+"$command$; RETURN NULL; END$$",
					"CREATE TRIGGER i AFTER INSERT ON tt REFERENCING NEW TABLE x EXECUTE FUNCTION tt_read()")
				run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
				run(fixture.Exec("INSERT INTO x VALUES(900)", 1))
				d := fixture.ControlsReferenceDiagnostic("0A000", tc.message)
				// Pinned18.6 emits the dynamic SQL frame as well as EXECUTE.
				d.Context = "SQL statement \"" + tc.command + "\"\nPL/pgSQL function tt_read() line 1 at EXECUTE"
				if tc.name == "view" {
					// DefineView reanalyzes without the invocation query environment.
					run(fixture.Exec("DROP TABLE x", 0))
					d.Code = "42P01"
					d.InternalPosition, d.InternalQuery = "38", tc.command
					d.Context = "PL/pgSQL function tt_read() line 1 at EXECUTE"
				}
				if tc.name == "row-lock" {
					// Explicit OF targets reject ENRs. Cursor conversion is predicted
					// from released SPI error handling, pending pinned observation.
					d.InternalPosition, d.InternalQuery = "31", tc.command
					d.Context = "PL/pgSQL function tt_read() line 1 at EXECUTE"
				}
				s := fixture.ErrorDiagnostic("INSERT INTO tt VALUES(1),(2) RETURNING id", d)
				s.SelectRows = true
				run(s)
				for _, table := range []string{"tt", "tt_read_audit"} {
					run(fixture.Select("SELECT id FROM "+table, []string{"id"}, []string{"INT4"}, nil))
				}
				if tc.name == "view" {
					run(fixture.Select("SELECT relname FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='x'", []string{"relname"}, []string{"NAME"}, nil))
					userOperatorExec(t, p, b, c, "CREATE TABLE x(id integer)")
					run(fixture.Exec("INSERT INTO x VALUES(900)", 1))
				} else {
					run(fixture.Select("SELECT id FROM x", []string{"id"}, []string{"INT4"}, [][]any{{int64(900)}}))
				}
				run(fixture.Select("SELECT relname FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='tt_view'", []string{"relname"}, []string{"NAME"}, nil))
				userOperatorExec(t, p, b, c, "CREATE OR REPLACE FUNCTION tt_read() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO public.x SELECT id FROM x; UPDATE public.x SET id=id+1 WHERE id=900; DELETE FROM public.x WHERE id=901; RETURN NULL; END$$")
				run(fixture.Exec("INSERT INTO tt VALUES(3)", 1))
				run(fixture.Select("SELECT id FROM x", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}))
			})
		})
	}
	t.Run("catalog-view-and-blanket-lock", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			userOperatorExec(t, p, b, c,
				"CREATE TABLE tt(id integer)", "CREATE TABLE x(id integer)", "CREATE TABLE tt_read_audit(id integer)",
				"CREATE FUNCTION tt_read() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN EXECUTE 'CREATE VIEW tt_view AS SELECT * FROM x'; EXECUTE 'INSERT INTO tt_read_audit SELECT id FROM x FOR UPDATE'; RETURN NULL; END$$",
				"CREATE TRIGGER i AFTER INSERT ON tt REFERENCING NEW TABLE x EXECUTE FUNCTION tt_read()")
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
			run(fixture.Exec("INSERT INTO x VALUES(900)", 1))
			run(fixture.Exec("INSERT INTO tt VALUES(1),(2)", 2))
			run(fixture.Select("SELECT id FROM tt_read_audit ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			run(fixture.Select("SELECT id FROM tt_view", []string{"id"}, []string{"INT4"}, [][]any{{int64(900)}}))
			run(fixture.Exec("UPDATE public.x SET id=901", 1))
			run(fixture.Select("SELECT id FROM tt_view", []string{"id"}, []string{"INT4"}, [][]any{{int64(901)}}))
			userOperatorExec(t, p, b, c,
				"DROP VIEW tt_view", "DROP TABLE x", "TRUNCATE tt_read_audit",
				"CREATE OR REPLACE FUNCTION tt_read() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN EXECUTE 'INSERT INTO tt_read_audit SELECT id FROM x FOR UPDATE'; RETURN NULL; END$$")
			run(fixture.Exec("INSERT INTO tt VALUES(3),(4)", 2))
			run(fixture.Select("SELECT id FROM tt_read_audit ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(4)}}))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='x'", []string{"relname"}, []string{"NAME"}, nil))
		})
	})
}
