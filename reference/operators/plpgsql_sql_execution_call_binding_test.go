package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLSQLExecutionCallBinding(t *testing.T) {
	t.Run("notation_and_defaults", func(t *testing.T) {
		sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE PROCEDURE sx_default(OUT answer integer,IN x integer DEFAULT 4) LANGUAGE SQL AS 'SELECT x'",
				"CREATE PROCEDURE sx_mix(OUT first integer,IN x integer,INOUT total integer,OUT note text,IN bonus integer DEFAULT 7) LANGUAGE SQL AS $$SELECT x+bonus,total+x,'done'::text$$",
				"CREATE PROCEDURE sx_optional(IN x integer,INOUT y integer DEFAULT 10) LANGUAGE SQL AS 'SELECT x+y'",
				"CREATE FUNCTION sx_default_call() RETURNS integer LANGUAGE plpgsql AS $$DECLARE a integer; BEGIN CALL sx_default(a); RETURN a; END$$",
				"CREATE FUNCTION sx_omitted() RETURNS integer LANGUAGE plpgsql AS $$BEGIN CALL sx_optional(4); RETURN 1; END$$")
			sqlExecutionValue(t, p, b, c, "SELECT sx_default_call() AS v", "INT4", int64(4))
			sqlExecutionBoth(t, p, b, c, "CALL sx_optional(4)", []string{"y"}, []string{"INT4"}, [][]any{{int64(14)}})
			sqlExecutionCallError(t, p, c, "SELECT sx_omitted() AS v", "sx_omitted()", "42601", `procedure parameter "y" is an output parameter but corresponding argument is not writable`)
			for _, call := range []string{
				"CALL sx_mix(a,4,n,s)",
				"CALL sx_mix(note=>s,total=>n,x=>4,first=>a)",
				"CALL sx_mix(a,4,note=>s,total=>n)",
				"CALL sx_mix(note:=s,total:=n,x:=4,first:=a)",
			} {
				userOperatorExec(t, p, b, c, "CREATE OR REPLACE FUNCTION sx_notation() RETURNS text LANGUAGE plpgsql AS $$DECLARE a integer; n integer:=10; s text; BEGIN "+call+"; RETURN a||':'||n||':'||s; END$$")
				sqlExecutionValue(t, p, b, c, "SELECT sx_notation() AS v", "TEXT", "11:14:done")
			}
		})
	})
	t.Run("variadic_common_identity", func(t *testing.T) {
		sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE PROCEDURE sx_variadic(OUT answer integer,VARIADIC xs integer[] DEFAULT '{}'::integer[]) LANGUAGE SQL AS 'SELECT coalesce(xs[1],0)+coalesce(xs[2],0)'",
				"CREATE PROCEDURE sx_array(OUT answer integer[],VARIADIC xs integer[] DEFAULT '{}'::integer[]) LANGUAGE SQL AS 'SELECT xs'")
			for _, tc := range []struct {
				call string
				want int64
			}{
				{"CALL sx_variadic(a,30,7)", 37},
				{"CALL sx_variadic(a,VARIADIC ARRAY[30,7])", 37},
				{"CALL sx_variadic(answer=>a,VARIADIC xs=>ARRAY[30,7])", 37},
				{"CALL sx_variadic(a)", 0},
				{"CALL sx_variadic(a,VARIADIC NULL::integer[])", 0},
			} {
				userOperatorExec(t, p, b, c, "CREATE OR REPLACE FUNCTION sx_variadic_call() RETURNS integer LANGUAGE plpgsql AS $$DECLARE a integer; BEGIN "+tc.call+"; RETURN a; END$$")
				sqlExecutionValue(t, p, b, c, "SELECT sx_variadic_call() AS v", "INT4", tc.want)
			}
			for _, tc := range []struct {
				call string
				want any
			}{
				{"CALL sx_array(a)", "{}"},
				{"CALL sx_array(a,VARIADIC NULL::integer[])", nil},
				{"CALL sx_array(answer=>a,VARIADIC xs=>'[0:1]={30,7}'::integer[])", "[0:1]={30,7}"},
			} {
				userOperatorExec(t, p, b, c, "CREATE OR REPLACE FUNCTION sx_array_call() RETURNS text LANGUAGE plpgsql AS $$DECLARE a integer[]; BEGIN "+tc.call+"; RETURN a::text; END$$")
				sqlExecutionValue(t, p, b, c, "SELECT sx_array_call() AS v", "TEXT", tc.want)
			}
		})
	})
	t.Run("winner_and_actuals_once", func(t *testing.T) {
		sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE SEQUENCE sx_actual_seq", "CREATE SEQUENCE sx_winner_seq", "CREATE SEQUENCE sx_loser_seq", "CREATE SEQUENCE sx_out_seq",
				"CREATE PROCEDURE sx_once(OUT answer integer,IN x integer,IN y integer DEFAULT nextval('sx_winner_seq')::integer) LANGUAGE SQL AS 'SELECT x+y'",
				"CREATE PROCEDURE sx_once(OUT answer bigint,IN x bigint,IN y integer DEFAULT nextval('sx_loser_seq')::integer) LANGUAGE SQL AS 'SELECT x+y'",
				"CREATE FUNCTION sx_once_call() RETURNS integer LANGUAGE plpgsql AS $$DECLARE a integer; BEGIN CALL sx_once(a,nextval('sx_actual_seq')::integer); RETURN a; END$$",
				"CREATE PROCEDURE sx_snapshot(INOUT a integer,INOUT b integer) LANGUAGE SQL AS 'SELECT b,a'",
				"CREATE FUNCTION sx_snapshot_call() RETURNS text LANGUAGE plpgsql AS $$DECLARE a integer:=10; b integer:=20; BEGIN CALL sx_snapshot(a,b); RETURN a||':'||b; END$$",
				"CREATE FUNCTION sx_same_call() RETURNS integer LANGUAGE plpgsql AS $$DECLARE a integer:=10; BEGIN CALL sx_snapshot(a,a); RETURN a; END$$")
			q := "SELECT sx_once_call() AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			// Prepare cannot evaluate actuals or either candidate default.
			for _, name := range []string{"sx_actual_seq", "sx_winner_seq", "sx_loser_seq", "sx_out_seq"} {
				sqlExecutionValue(t, p, b, c, "SELECT is_called IS TRUE AS v FROM "+name, "BOOL", false)
			}
			for route := range 3 {
				for _, name := range []string{"sx_actual_seq", "sx_winner_seq", "sx_loser_seq", "sx_out_seq"} {
					userOperatorExec(t, p, b, c, "ALTER SEQUENCE "+name+" RESTART WITH 1")
				}
				s := sqlExecutionStep(b, q, []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}})
				var executor fixture.Executor = c
				if route == 1 {
					s.Prepared = true
				}
				if route == 2 {
					executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				}
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
				sqlExecutionValue(t, p, b, c, "SELECT currval('sx_actual_seq') AS v", "INT8", int64(1))
				sqlExecutionValue(t, p, b, c, "SELECT currval('sx_winner_seq') AS v", "INT8", int64(1))
				for _, name := range []string{"sx_loser_seq", "sx_out_seq"} {
					sqlExecutionValue(t, p, b, c, "SELECT is_called IS TRUE AS v FROM "+name, "BOOL", false)
				}
			}
			// SQL pure OUT actuals are analyzed but have no executable demand.
			sqlExecutionBoth(t, p, b, c, "CALL sx_once(nextval('sx_out_seq')::integer,4,3)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(7)}})
			sqlExecutionValue(t, p, b, c, "SELECT is_called IS TRUE AS v FROM sx_out_seq", "BOOL", false)
			sqlExecutionValue(t, p, b, c, "SELECT sx_snapshot_call() AS v", "TEXT", "20:10")
			sqlExecutionValue(t, p, b, c, "SELECT sx_same_call() AS v", "INT4", int64(10))
		})
	})
	t.Run("path_and_fixed_over_variadic", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "sx_call_path")
		sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE SCHEMA sx_call_path",
				"CREATE PROCEDURE public.sx_pick(INOUT x integer) LANGUAGE SQL AS 'SELECT x+1'",
				"CREATE PROCEDURE sx_call_path.sx_pick(INOUT x integer) LANGUAGE SQL AS 'SELECT x+10'",
				"CREATE PROCEDURE public.sx_rank(OUT a integer,IN x integer) LANGUAGE SQL AS 'SELECT 1'",
				"CREATE PROCEDURE public.sx_rank(OUT a integer,VARIADIC x integer[]) LANGUAGE SQL AS 'SELECT 2'",
				"CREATE FUNCTION sx_path() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer:=4; BEGIN CALL sx_pick(x); RETURN x; END$$",
				"CREATE FUNCTION sx_rank_call() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN CALL sx_rank(x,4); RETURN x; END$$",
				"SET search_path=sx_call_path,public")
			sqlExecutionValue(t, p, b, c, "SELECT sx_path() AS v", "INT4", int64(14))
			sqlExecutionValue(t, p, b, c, "SELECT sx_rank_call() AS v", "INT4", int64(1))
			userOperatorExec(t, p, b, c, "SET search_path=public,sx_call_path")
			sqlExecutionValue(t, p, b, c, "SELECT sx_path() AS v", "INT4", int64(5))
			userOperatorExec(t, p, b, c, "RESET search_path")
		})
	})
}
