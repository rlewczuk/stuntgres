package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func hostQuote(s string) string { return strings.ReplaceAll(s, "'", "''") }

func TestReferenceRoutineHostFailures(t *testing.T) {
	hostScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer routineLanguagesCleanup(t, c, "DROP OWNED BY host_failure_owner CASCADE", "DROP ROLE host_failure_owner")()
		hostExec(t, p, b, c, "CREATE ROLE host_failure_owner")
		hostExec(t, p, b, c, "GRANT USAGE,CREATE ON SCHEMA public TO host_failure_owner")
		hostExec(t, p, b, c, "CREATE TABLE host_effects(v integer)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO host_effects VALUES (1)", 1))
		hostExec(t, p, b, c, "ALTER TABLE host_effects OWNER TO host_failure_owner")
		hostFunction(t, p, b, c, "host_good", "increment-v1", hostIncrementPython, "", true)
		q := "SELECT key,value FROM each(host_good($1::hstore)) ORDER BY key"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		hostRows(t, p, b, held, q, []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "2"}, {"c", "3"}}, "a=>1")
		for _, tc := range []struct{ name, program, body, code, message string }{
			{"host_wrong", "wrong-object-v1", "return 7", "42809", "not a Python mapping"},
			{"host_list", "wrong-list-v1", "return ['not', 'a mapping']", "42809", "not a Python mapping"},
			{"host_raise", "raise-v1", "raise ValueError('host program failed')", "38000", "ValueError: host program failed"},
			{"host_parse", "wrong-result-v1", "return 'not hstore syntax'", "42601", `syntax error in hstore, near "h" at position 4`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				transform := tc.name != "host_parse" || !b.PG
				hostFunction(t, p, b, c, tc.name, tc.program, tc.body, "SET application_name='host-inner'", transform)
				// Full diagnostics observed on the pinned PostgreSQL 18.6 oracle.
				d := hostPythonError(tc.code, tc.message, tc.name)
				if tc.name == "host_raise" {
					d.Context = "Traceback (most recent call last):\n  PL/Python function \"host_raise\", line 1, in <module>\n    raise ValueError('host program failed')\nPL/Python function \"host_raise\""
				} else {
					d.Context = "while creating return value\n" + d.Context
				}
				if b.PG {
					hostError(t, p, c, "SELECT "+tc.name+"('a=>1'::hstore)", d)
				} else {
					message := "host: result is not a nonnil map"
					if tc.name == "host_raise" {
						message = "host: program failed"
					}
					if tc.name == "host_parse" {
						message = "stuntgres: host result has wrong SQL type"
					}
					hostNativeError(t, p.Ctx, c, "SELECT "+tc.name+"('a=>1'::hstore)", message)
				}
				hostExec(t, p, b, c, "CREATE OR REPLACE FUNCTION host_prefix() RETURNS hstore LANGUAGE plpgsql SECURITY DEFINER SET application_name='host-wrapper' AS $$ BEGIN INSERT INTO host_effects VALUES (2); RETURN "+tc.name+"('a=>1'::hstore); END $$")
				hostExec(t, p, b, c, "ALTER FUNCTION host_prefix() OWNER TO host_failure_owner")
				d.Context += "\nPL/pgSQL function host_prefix() line 1 at RETURN"
				if b.PG {
					hostError(t, p, c, "SELECT host_prefix()", d)
				} else {
					message := "host: result is not a nonnil map"
					if tc.name == "host_raise" {
						message = "host: program failed"
					}
					if tc.name == "host_parse" {
						message = "stuntgres: host result has wrong SQL type"
					}
					hostNativeError(t, p.Ctx, c, "SELECT host_prefix()", message)
				}
				hostRows(t, p, b, c, "SELECT v FROM host_effects ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}})
				owner := "postgres"
				if b.PG {
					owner = "stuntgres"
				}
				hostRows(t, p, b, c, "SELECT current_user::text AS u,current_role::text AS r,current_setting('application_name') AS a", []string{"u", "r", "a"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{owner, owner, ""}})
				// The SAME prepared good frame remains usable after failures.
				for _, input := range []string{"a=>1", "a=>7", "a=>1"} {
					a := "2"
					if input == "a=>7" {
						a = "8"
					}
					hostRows(t, p, b, held, q, []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"a", a}, {"c", "3"}}, input)
				}
			})
		}
		// Real handler compilation must not publish an orphan or replace the
		// earlier callable; the pinned oracle reports this exact SyntaxError.
		for _, name := range []string{"host_bad_source", "host_good"} {
			q := "CREATE OR REPLACE FUNCTION " + name + "(v hstore) RETURNS hstore LANGUAGE plpython3u TRANSFORM FOR TYPE hstore AS 'return ('"
			d := fixture.ControlsReferenceDiagnostic("38000", "could not compile PL/Python function \""+name+"\"")
			d.Detail = "SyntaxError: '(' was never closed (<string>, line 2)"
			if b.PG {
				hostError(t, p, c, q, d)
			} else {
				q = "CREATE OR REPLACE FUNCTION " + name + "(v hstore) RETURNS hstore LANGUAGE go_host AS 'return ('"
				hostNativeError(t, p.Ctx, c, q, `host program "return (" is not registered`)
			}
		}
		hostRows(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='host_bad_source'", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}})
		hostBag(t, p, b, c, "host_good('a=>7'::hstore)", [][]any{{"a", "8"}, {"c", "3"}})
		// FROM-SQL input rejection occurs at actual hstore input, before Python;
		// arbitrary native converter/handler errors have no fake PG counterpart.
		d := fixture.ControlsReferenceDiagnostic("42601", "syntax error in hstore: unexpected end of string")
		d.Position = "18"
		hostErrorAtStage(t, p, c, "SELECT host_good('broken'::hstore)", d, "prepare")
		if !b.PG {
			hostNativeFailures(t, p, b, c, held, q)
		}
	})
}
