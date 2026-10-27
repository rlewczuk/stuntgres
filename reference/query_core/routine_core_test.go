package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineCoreCreationAndCalls(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE FUNCTION add1(integer) RETURNS integer LANGUAGE SQL AS $$ SELECT $1 + 1 $$",
			"CREATE FUNCTION routine_twice(integer) RETURNS integer AS 'SELECT add1(add1($1))' LANGUAGE SQL",
			`CREATE FUNCTION "Routine Quote"(integer) RETURNS integer LANGUAGE SQL AS $body$ SELECT $1 + 1 AS ignored; $body$`)
		routineValue(t, p, b, c, "SELECT add1(4)", "add1", "INT4", int64(5))
		routineValue(t, p, b, c, "SELECT routine_twice(4)", "routine_twice", "INT4", int64(6))
		routineValue(t, p, b, c, `SELECT "Routine Quote"(4) AS answer`, "answer", "INT4", int64(5))
		q := "SELECT add1($1) AS answer"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, v := range []int64{4, 9, 4} {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder,
				fixture.Select(q, []string{"answer"}, []string{"INT4"}, [][]any{{v + 1}}, v))
		}
		// A CREATE body parameter is not a public prepared-statement parameter.
		create, err := c.PrepareContext(p.Ctx, "CREATE FUNCTION routine_prepared(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$")
		if err != nil {
			t.Fatal(err)
		}
		result, err := create.ExecContext(p.Ctx)
		create.Close()
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 0 {
			t.Fatal(n, err)
		}
		routineValue(t, p, b, c, "SELECT routine_prepared(8)", "routine_prepared", "INT4", int64(8))
	})
}

func TestReferenceRoutineCoreResolution(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "routine_early", "routine_later")
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE SCHEMA routine_early", "CREATE SCHEMA routine_later",
			"CREATE FUNCTION routine_pick(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 4$$",
			"CREATE FUNCTION routine_pick(bigint) RETURNS integer LANGUAGE SQL AS $$SELECT 8$$",
			"CREATE FUNCTION routine_wide(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION routine_narrow(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION routine_unknown(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 4$$",
			"CREATE FUNCTION routine_unknown(text) RETURNS integer LANGUAGE SQL AS $$SELECT 9$$",
			"CREATE FUNCTION routine_early.pathpick(bigint) RETURNS integer LANGUAGE SQL AS $$SELECT 8$$",
			"CREATE FUNCTION routine_later.pathpick(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 4$$",
			"CREATE FUNCTION routine_early.abs(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 99$$")
		for _, tc := range []struct {
			q     string
			typ   string
			value any
		}{
			{"SELECT routine_pick(1) AS v", "INT4", int64(4)},
			{"SELECT routine_pick(1::bigint) AS v", "INT4", int64(8)},
			{"SELECT routine_wide(4) AS v", "INT8", int64(4)},
			{"SELECT routine_narrow('4') AS v", "INT4", int64(4)},
			{"SELECT routine_narrow(NULL) AS v", "INT4", nil},
			{"SELECT routine_unknown('4') AS v", "INT4", int64(9)},
			{"SELECT routine_unknown(NULL) AS v", "INT4", int64(9)},
			{"SELECT routine_early.abs(-2) AS v", "INT4", int64(99)},
		} {
			routineValue(t, p, b, c, tc.q, "v", tc.typ, tc.value)
		}
		routineExec(t, p, b, c, "SET search_path TO routine_early,routine_later,public")
		routineValue(t, p, b, c, "SELECT pathpick(1) AS v", "v", "INT4", int64(4))
		routineValue(t, p, b, c, "SELECT abs(-2) AS v", "v", "INT4", int64(2))
		routineExec(t, p, b, c, "SET search_path TO routine_early,pg_catalog,public")
		routineValue(t, p, b, c, "SELECT abs(-2) AS v", "v", "INT4", int64(99))
		routineExec(t, p, b, c, "SET search_path TO public")
		routineDiagnostic(t, p, c, "SELECT routine_narrow(4::bigint)", "42883", "function routine_narrow(bigint) does not exist", "", true)
		routineDiagnostic(t, p, c, "SELECT routine_pick('4')", "42725", "function routine_pick(unknown) is not unique", "", true)
	})
	t.Run("authority", routineAuthority)
}

func TestReferenceRoutineCoreDiagnostics(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c, "CREATE FUNCTION add1(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$")
		for _, tc := range []struct {
			q, code, message, detail string
			query                    bool
		}{
			{"SELECT routine_missing(1)", "42883", "function routine_missing(integer) does not exist", "", true},
			{"SELECT add1()", "42883", "function add1() does not exist", "", true},
			{"SELECT add1(true)", "42883", "function add1(boolean) does not exist", "", true},
			{"SELECT 'é' AS label, routine_missing(1)", "42883", "function routine_missing(integer) does not exist", "", true},
			{"CREATE FUNCTION add1(integer) RETURNS bigint LANGUAGE SQL AS $$SELECT 1$$", "42723", `function "add1" already exists with same argument types`, "", false},
			{"CREATE FUNCTION routine_bad_result() RETURNS integer LANGUAGE SQL AS $$SELECT 'x'::text$$", "42P13", "return type mismatch in function declared to return integer", "Actual return type is text.", false},
			{"CREATE FUNCTION routine_bad_width() RETURNS integer LANGUAGE SQL AS $$SELECT 1,2$$", "42P13", "return type mismatch in function declared to return integer", "Final statement must return exactly one column.", false},
			{"CREATE FUNCTION routine_bad_body() RETURNS integer LANGUAGE SQL AS $$SELECT 1 +$$", "42601", "syntax error at end of input", "", false},
			{"CREATE FUNCTION routine_bad_unicode() RETURNS integer LANGUAGE SQL AS $$SELECT 'é', 1 +$$", "42601", "syntax error at end of input", "", false},
		} {
			routineDiagnostic(t, p, c, tc.q, tc.code, tc.message, tc.detail, tc.query)
		}
		// Every failed CREATE must leave its signature available for correction.
		routineExec(t, p, b, c,
			"CREATE FUNCTION routine_bad_result() RETURNS integer LANGUAGE SQL AS $$SELECT 5$$",
			"CREATE FUNCTION routine_bad_width() RETURNS integer LANGUAGE SQL AS $$SELECT 5$$",
			"CREATE FUNCTION routine_bad_body() RETURNS integer LANGUAGE SQL AS $$SELECT 5$$")
		routineValue(t, p, b, c, "SELECT routine_bad_result()+routine_bad_width()+routine_bad_body() AS v", "v", "INT4", int64(15))
	})
	t.Run("declaration forms", routineDeclarationForms)
}
