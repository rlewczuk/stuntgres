package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineLanguagesInquiry(t *testing.T) {
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer routineLanguagesCleanup(t, c, `DROP LANGUAGE IF EXISTS "RL Inquiry",rl_inquiry_call CASCADE`)()
		defer routineLanguagesRestoreBuiltin(t, c, "rl_inquiry_sql_language", "sql")()
		defer routineLanguagesRestoreBuiltin(t, c, "rl_inquiry_pl_language", "plpgsql")()
		userOperatorExec(t, p, b, c,
			`CREATE TRUSTED LANGUAGE "RL Inquiry"`+routineLanguageFull,
			"CREATE TRUSTED LANGUAGE rl_inquiry_call"+routineLanguageCall,
			`CREATE FUNCTION rl_inquiry_full(x integer) RETURNS integer LANGUAGE "RL Inquiry" AS 'BEGIN RETURN x+1; END'`,
			"CREATE FUNCTION rl_inquiry_call_fn(x integer) RETURNS integer LANGUAGE rl_inquiry_call AS 'BEGIN RETURN x+1; END'",
			"CREATE FUNCTION rl_inquiry_bad() RETURNS integer LANGUAGE rl_inquiry_call AS 'BEGIN RETURN 1;'",
			"CREATE FUNCTION rl_inquiry_result() RETURNS cstring LANGUAGE rl_inquiry_call AS 'BEGIN RETURN NULL; END'",
			"CREATE FUNCTION rl_inquiry_argument(x cstring) RETURNS integer LANGUAGE rl_inquiry_call AS 'BEGIN RETURN NULL; END'",
			"CREATE FUNCTION rl_inquiry_sql(x integer) RETURNS integer LANGUAGE sql AS 'SELECT $1+1'",
			"CREATE FUNCTION rl_inquiry_pl(x integer) RETURNS integer LANGUAGE plpgsql AS 'BEGIN RETURN x+1; END'")
		definition := func(name, args, result, language, body string) string {
			return "CREATE OR REPLACE FUNCTION public." + name + "(" + args + ")\n RETURNS " + result + "\n LANGUAGE " + language + "\nAS $function$" + body + "$function$\n"
		}
		inquiry := func(c *sql.Conn, b fixture.Backend, identity, args, result, want string) {
			t.Helper()
			for _, tc := range []struct{ function, want string }{
				{"pg_get_functiondef", want},
				{"pg_get_function_arguments", args},
				{"pg_get_function_identity_arguments", args},
				{"pg_get_function_result", result},
			} {
				step := fixture.DeparseRelationsTextStep(t, b, "SELECT "+tc.function+"('"+identity+"'::regprocedure) AS definition", tc.want)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
			}
		}
		old := definition("rl_inquiry_sql", "x integer", "integer", "sql", "SELECT $1+1")
		inquiry(c, b, "rl_inquiry_sql(integer)", "x integer", "integer", old)
		inquiry(c, b, "rl_inquiry_pl(integer)", "x integer", "integer", definition("rl_inquiry_pl", "x integer", "integer", "plpgsql", "BEGIN RETURN x+1; END"))
		q := "SELECT pg_get_functiondef('rl_inquiry_sql(integer)'::regprocedure) AS definition"
		retained, err := c.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		var captured string
		consumeRetained := func() {
			fixture.DeparseRelationsMetadata(t, b.PG, retained)
			if !retained.Next() {
				t.Fatal("missing retained inquiry", retained.Err())
			}
			if err := retained.Scan(&captured); err != nil || captured != old {
				t.Fatal("retained inquiry changed", captured, err)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("extra retained inquiry", retained.Err())
			}
			retained.Close()
		}
		// pq streams on this physical connection; native eager rows can remain
		// unread across the rename to prove detached backing ownership.
		if b.PG {
			consumeRetained()
		}
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		userOperatorExec(t, p, b, c, "ALTER LANGUAGE sql RENAME TO rl_inquiry_sql_language", "ALTER LANGUAGE plpgsql RENAME TO rl_inquiry_pl_language")
		if !b.PG {
			consumeRetained()
		}
		renamed := definition("rl_inquiry_sql", "x integer", "integer", "rl_inquiry_sql_language", "SELECT $1+1")
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, fixture.DeparseRelationsTextStep(t, b, q, renamed))
		check := func(c *sql.Conn, b fixture.Backend) {
			for _, tc := range []struct{ name, signature, args, result, language, body string }{
				{"rl_inquiry_full", "integer", "x integer", "integer", `"RL Inquiry"`, "BEGIN RETURN x+1; END"},
				{"rl_inquiry_call_fn", "integer", "x integer", "integer", "rl_inquiry_call", "BEGIN RETURN x+1; END"},
				{"rl_inquiry_bad", "", "", "integer", "rl_inquiry_call", "BEGIN RETURN 1;"},
				{"rl_inquiry_result", "", "", "cstring", "rl_inquiry_call", "BEGIN RETURN NULL; END"},
				{"rl_inquiry_argument", "cstring", "x cstring", "integer", "rl_inquiry_call", "BEGIN RETURN NULL; END"},
				{"rl_inquiry_sql", "integer", "x integer", "integer", "rl_inquiry_sql_language", "SELECT $1+1"},
				{"rl_inquiry_pl", "integer", "x integer", "integer", "rl_inquiry_pl_language", "BEGIN RETURN x+1; END"},
			} {
				inquiry(c, b, tc.name+"("+tc.signature+")", tc.args, tc.result, definition(tc.name, tc.args, tc.result, tc.language, tc.body))
				name := tc.language
				if name == `"RL Inquiry"` {
					name = "RL Inquiry"
				}
				plpgsqlValue(t, p, b, c, "SELECT p.prolang=l.oid AS v FROM pg_proc p JOIN pg_language l ON l.lanname='"+name+"' WHERE p.proname='"+tc.name+"'", "BOOL", true)
			}
			// Released SQL declarations have no user SQL payload or language
			// address; their fixed SQL identity still follows the current name.
			round := "CREATE OR REPLACE FUNCTION pg_catalog.round(numeric)\n RETURNS numeric\n LANGUAGE rl_inquiry_sql_language\n IMMUTABLE PARALLEL SAFE STRICT COST 1\nRETURN round($1, 0)\n"
			inquiry(c, b, "round(numeric)", "numeric", "numeric", round)
			for _, fn := range []string{"rl_inquiry_full", "rl_inquiry_call_fn", "rl_inquiry_sql", "rl_inquiry_pl"} {
				parametersValue(t, p, b, c, "SELECT "+fn+"($1::integer) AS v", "v", "INT4", int64(5), 4)
			}
		}
		check(c, b)
		if !b.PG {
			routineLanguagesExtensionReplay(t, p, b, c, nil, "rl_inquiry_call_fn", check)
		}
	})
}
