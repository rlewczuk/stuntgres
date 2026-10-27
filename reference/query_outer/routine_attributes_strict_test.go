package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineAttributesStrictEffectsAndVariadic(t *testing.T) {
	for _, language := range []string{"sql", "plpgsql"} {
		t.Run(language+"_effects", func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				body := "AS $$INSERT INTO ra_s_log VALUES($1,$2); SELECT 7$$"
				if language == "plpgsql" {
					body = "AS $$BEGIN INSERT INTO ra_s_log VALUES($1,$2); RETURN 7; END$$"
				}
				resultsExec(t, p, b, c, "CREATE TABLE ra_s_log(a integer,b integer)", "CREATE FUNCTION ra_s_pair(a integer,b integer DEFAULT NULL) RETURNS integer LANGUAGE "+language+" STRICT "+body)
				for _, args := range [][]any{{nil, 2}, {1, nil}, {nil, nil}} {
					proceduresBoth(t, p, b, c, "SELECT ra_s_pair($1::integer,$2::integer) AS v", []string{"v"}, []string{"INT4"}, [][]any{{nil}}, args...)
					proceduresCount(t, p, b, c, "ra_s_log", 0)
				}
				proceduresBoth(t, p, b, c, "SELECT ra_s_pair(b=>NULL::integer,a=>1) AS v", []string{"v"}, []string{"INT4"}, [][]any{{nil}})
				proceduresBoth(t, p, b, c, "SELECT ra_s_pair(a=>1) AS v", []string{"v"}, []string{"INT4"}, [][]any{{nil}})
				proceduresCount(t, p, b, c, "ra_s_log", 0)
				proceduresBoth(t, p, b, c, "SELECT ra_s_pair(1,2) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
				resultsStoredBoth(t, p, b, c, "SELECT a,b FROM ra_s_log ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(1), int64(2)}})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ra_s_log", 2))
				proceduresHeld(t, p, b, c, "SELECT ra_s_pair($1::integer,2) AS v", []string{"v"}, []string{"INT4"}, [][]any{{nil}, {2}, {nil}}, [][][]any{{{nil}}, {{int64(7)}}, {{nil}}})
				resultsStoredBoth(t, p, b, c, "SELECT a,b FROM ra_s_log", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(2)}})
				resultsExec(t, p, b, c, "ALTER FUNCTION ra_s_pair(integer,integer) CALLED ON NULL INPUT")
				proceduresBoth(t, p, b, c, "SELECT ra_s_pair(NULL::integer,2) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
				resultsStoredBoth(t, p, b, c, "SELECT a,b FROM ra_s_log ORDER BY a NULLS FIRST,b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{nil, int64(2)}, {nil, int64(2)}, {int64(2), int64(2)}})
			})
		})
		name := language + "_variadic"
		if language == "sql" {
			name = "sql_composite_set_variadic"
		}
		t.Run(name, func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				variadic := "AS 'INSERT INTO ra_s_effect VALUES(3); SELECT 7'"
				if language == "plpgsql" {
					variadic = "AS $$BEGIN INSERT INTO ra_s_effect VALUES(3); RETURN 7; END$$"
				}
				resultsExec(t, p, b, c, "CREATE TABLE ra_s_effect(v integer)",
					"CREATE FUNCTION ra_s_var(VARIADIC xs integer[]) RETURNS integer LANGUAGE "+language+" STRICT "+variadic)
				if language == "sql" {
					resultsExec(t, p, b, c, "CREATE TYPE ra_s_tuple AS (a integer,b integer)",
						"CREATE FUNCTION ra_s_tuple_fn(integer) RETURNS ra_s_tuple LANGUAGE SQL STRICT AS 'INSERT INTO ra_s_effect VALUES(1); SELECT $1,$1+1'",
						"CREATE FUNCTION ra_s_set(integer) RETURNS SETOF integer LANGUAGE SQL STRICT AS 'INSERT INTO ra_s_effect VALUES(2); SELECT $1'")
					resultsBoth(t, p, b, c, "SELECT ra_s_tuple_fn(NULL::integer) AS v", []string{"v"}, []string{""}, [][]any{{nil}})
					proceduresBoth(t, p, b, c, "SELECT * FROM ra_s_set(NULL::integer) AS v", []string{"v"}, []string{"INT4"}, [][]any{})
				}
				proceduresBoth(t, p, b, c, "SELECT ra_s_var(VARIADIC NULL::integer[]) AS v", []string{"v"}, []string{"INT4"}, [][]any{{nil}})
				proceduresCount(t, p, b, c, "ra_s_effect", 0)
				if language == "sql" {
					resultsBoth(t, p, b, c, "SELECT ra_s_tuple_fn(2) AS v", []string{"v"}, []string{""}, [][]any{{"(2,3)"}})
					proceduresBoth(t, p, b, c, "SELECT * FROM ra_s_set(2) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}})
				}
				for _, arg := range []string{"ARRAY[1,NULL,3]::integer[]", "ARRAY[]::integer[]"} {
					proceduresBoth(t, p, b, c, "SELECT ra_s_var(VARIADIC "+arg+") AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
				}
				want := [][]any{{int64(3)}, {int64(3)}, {int64(3)}, {int64(3)}}
				if language == "sql" {
					want = [][]any{{int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(3)}, {int64(3)}, {int64(3)}, {int64(3)}}
				}
				resultsStoredBoth(t, p, b, c, "SELECT v FROM ra_s_effect ORDER BY v", []string{"v"}, []string{"INT4"}, want)
			})
		})
	}
	t.Run("execute_permission_precedes_null", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ra_s_denied"})
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c, "CREATE ROLE ra_s_denied", "CREATE TABLE ra_s_effect(v integer)", "CREATE FUNCTION ra_s_private(integer) RETURNS integer LANGUAGE SQL STRICT SECURITY DEFINER AS 'INSERT INTO ra_s_effect VALUES($1); SELECT 7'", "REVOKE ALL ON FUNCTION ra_s_private(integer) FROM PUBLIC")
			resultsExec(t, p, b, c, "GRANT USAGE ON SCHEMA public TO ra_s_denied")
			resultsExec(t, p, b, c, "CREATE TABLE ra_s_input(v integer)", "GRANT SELECT ON ra_s_input TO ra_s_denied")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_s_input VALUES(NULL)", 1))
			q := "SELECT ra_s_private($1::integer) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			runtimeSQL := "SELECT ra_s_private(v) AS v FROM ra_s_input"
			runtimeStmt, err := c.PrepareContext(p.Ctx, runtimeSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer runtimeStmt.Close()
			runtimeHeld := fixture.HeldStatement{Executor: c, Statement: runtimeStmt, SQL: runtimeSQL}
			resultsExec(t, p, b, c, "SET SESSION AUTHORIZATION ra_s_denied")
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			want := proceduresDiagnostic("42501", "permission denied for function ra_s_private", 0)
			// Pinned PG eliminates a STRICT call whose argument is a known NULL,
			// including a NULL parameter in a custom plan. No invocation occurs.
			proceduresBoth(t, p, b, c, "SELECT ra_s_private(NULL::integer) AS v", []string{"v"}, []string{"INT4"}, [][]any{{nil}})
			proceduresBoth(t, p, b, c, q, []string{"v"}, []string{"INT4"}, [][]any{{nil}}, nil)
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, proceduresStep(b, q, []string{"v"}, []string{"INT4"}, [][]any{{nil}}, nil))
			resultsError(t, p, c, q, true, "execute", want, 1)
			s := fixture.ErrorDiagnostic(q, want, 1)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			// A table value is not a planner-known constant. Both runtime NULL
			// and non-NULL inputs still require EXECUTE before strict dispatch.
			for _, value := range []any{nil, int64(1)} {
				resultsExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE ra_s_input SET v=$1::integer", 1, value))
				resultsExec(t, p, b, c, "SET SESSION AUTHORIZATION ra_s_denied")
				resultsError(t, p, c, runtimeSQL, true, "execute", want)
				runtimeError := fixture.ErrorDiagnostic(runtimeSQL, want)
				runtimeError.SelectRows, runtimeError.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, runtimeHeld, b.Recorder, runtimeError)
				resultsExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
				proceduresCount(t, p, b, c, "ra_s_effect", 0)
				resultsExec(t, p, b, c, "SET SESSION AUTHORIZATION ra_s_denied")
			}
			resultsExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
			proceduresCount(t, p, b, c, "ra_s_effect", 0)
		})
	})
}
