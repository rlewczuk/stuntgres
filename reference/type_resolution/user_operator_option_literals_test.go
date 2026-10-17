package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUserOperatorOptionLiteralAndPrecedence(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"userop_option_reader"})
	fixture.SchemaReferenceCleanup(t, "userop_option_schema")
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		userOperatorExec(t, p, b, c, "CREATE TABLE userop_literal_sentinel(v integer)", "CREATE ROLE userop_option_reader LOGIN", "CREATE SCHEMA userop_option_schema", "GRANT USAGE ON SCHEMA userop_option_schema TO userop_option_reader", "CREATE OPERATOR userop_option_schema.#=# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO userop_literal_sentinel VALUES(42)", 1))
		for _, tc := range []struct{ options, code, message string }{
			{"LEFTARG=int4,RIGHTARG=int4,FUNCTION=DEFAULT", "42883", "function default(integer, integer) does not exist"},
			{"LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4", "42883", "function int4(integer, integer) does not exist"},
			{"LEFTARG=int4,RIGHTARG=int4,FUNCTION=INTEGER", "42883", "function pg_catalog.int4(integer, integer) does not exist"},
			{"LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.numeric(8,2)", "42883", "function pg_catalog.numeric(integer, integer) does not exist"},
			{"LEFTARG='',RIGHTARG=int4,FUNCTION=int4pl", "42704", `type "" does not exist`},
			{"LEFTARG=int4,RIGHTARG=int4,FUNCTION=''", "42883", "function (integer, integer) does not exist"},
			{"LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,COMMUTATOR=''", "42602", `"" is not a valid operator name`},
			{"LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,RESTRICT=NONE", "42883", "function none(internal, oid, internal, integer) does not exist"},
			{"LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,RESTRICT='none'", "42883", "function none(internal, oid, internal, integer) does not exist"},
		} {
			t.Run(tc.options, func(t *testing.T) {
				userOperatorError(t, p, b, c, "CREATE OPERATOR public.#%# ("+tc.options+")", tc.code, tc.message, "", "", "", false)
				userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname='#%#'", "INT8", int64(0))
			})
		}
		for _, value := range []string{"'none'", `"none"`} {
			userOperatorError(t, p, b, c, "ALTER OPERATOR userop_option_schema.#=# (int4,int4) SET (RESTRICT="+value+")", "42883", "function none(internal, oid, internal, integer) does not exist", "", "", "", false)
		}
		userOperatorExec(t, p, b, c, "CREATE OPERATOR public.#~# (LEFTARG='',LEFTARG='userop_missing_type',LEFTARG='int4',RIGHTARG='int4',FUNCTION='',FUNCTION='userop_missing_function',FUNCTION='int4pl',COMMUTATOR='abc',COMMUTATOR='#~#')")
		userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#~#) 3 AS v", "INT4", int64(5))
		userOperatorValue(t, p, b, c, "SELECT oprcom=oid AS v FROM pg_operator WHERE oid='public.#~#(int4,int4)'::regoperator", "BOOL", true)
		userOperatorExec(t, p, b, c, "SET ROLE userop_option_reader")
		for _, tc := range []struct{ query, code, message string }{
			{"ALTER OPERATOR userop_option_schema.#=# (int4,int4) SET (FUNCTION=7)", "42601", "argument of function must be a name"},
			{"ALTER OPERATOR userop_option_schema.#=# (int4,int4) SET (FUNCTION=int4pl)", "42601", `operator attribute "function" cannot be changed`},
			{"ALTER OPERATOR userop_option_schema.#=# (int4,int4) SET (RESTRICT=userop_missing_estimator)", "42501", "must be owner of operator #=#"},
			{"ALTER OPERATOR userop_option_schema.#=# (int4,int4) SET (COMMUTATOR='abc')", "42501", "must be owner of operator #=#"},
			{"ALTER OPERATOR userop_option_schema.#?# (int4,int4) SET (FUNCTION=7)", "42883", "operator does not exist: integer userop_option_schema.#?# integer"},
			{"CREATE OPERATOR userop_option_schema.#?# (FUNCTION=7)", "42501", "permission denied for schema userop_option_schema"},
		} {
			t.Run(tc.query, func(t *testing.T) { userOperatorError(t, p, b, c, tc.query, tc.code, tc.message, "", "", "", false) })
		}
		userOperatorExec(t, p, b, c, "RESET ROLE")
		userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(userop_option_schema.#=#) 2 AS v", "BOOL", true)
		userOperatorValue(t, p, b, c, "SELECT v FROM userop_literal_sentinel", "INT4", int64(42))
	})
}

func TestReferenceUserOperatorOptionSemanticCheckpoints(t *testing.T) {
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE userop_semantic_sentinel(v integer)",
			"CREATE OPERATOR public.#=# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,COMMUTATOR=OPERATOR(public.#!#),HASHES,MERGES)",
			"CREATE OPERATOR public.#!# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4ne,COMMUTATOR=OPERATOR(public.#=#))",
			"CREATE OPERATOR public.#@# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO userop_semantic_sentinel VALUES(42)", 1))
		for _, tc := range []struct{ options, code, message string }{
			{"HASHES=false,COMMUTATOR=OPERATOR(public.#?#)", "42883", "operator does not exist: integer public.#?# integer"},
			{"HASHES=false,MERGES=false", "42P13", `operator attribute "merges" cannot be changed if it has already been set`},
			{"COMMUTATOR=OPERATOR(public.#@#),NEGATOR=OPERATOR(public.#?#)", "42883", "operator does not exist: integer public.#?# integer"},
			{"COMMUTATOR=OPERATOR(public.#?#),NEGATOR=OPERATOR(public.#=#)", "42883", "operator does not exist: integer public.#?# integer"},
			{"COMMUTATOR=OPERATOR(public.#@#),NEGATOR=OPERATOR(public.#=#)", "42P13", "operator cannot be its own negator"},
			{"COMMUTATOR=OPERATOR(public.#@#),MERGES=false", "42P13", `operator attribute "commutator" cannot be changed if it has already been set`},
			{"NEGATOR=OPERATOR(public.#@#),MERGES=false", "42P13", `operator attribute "merges" cannot be changed if it has already been set`},
			{"RESTRICT=userop_missing_estimator,COMMUTATOR=OPERATOR(public.#?#),HASHES=false", "42883", "function userop_missing_estimator(internal, oid, internal, integer) does not exist"},
		} {
			t.Run(tc.options, func(t *testing.T) {
				userOperatorError(t, p, b, c, "ALTER OPERATOR public.#=# (int4,int4) SET ("+tc.options+")", tc.code, tc.message, "", "", "", false)
				userOperatorValue(t, p, b, c, "SELECT oprcanhash AND oprcanmerge AND oprcom='public.#!#(int4,int4)'::regoperator::oid AND oprnegate=0 AS v FROM pg_operator WHERE oid='public.#=#(int4,int4)'::regoperator", "BOOL", true)
				userOperatorValue(t, p, b, c, "SELECT oprcom=0 AND oprnegate=0 AS v FROM pg_operator WHERE oid='public.#@#(int4,int4)'::regoperator", "BOOL", true)
				userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#=#) 2 AS v", "BOOL", true)
				userOperatorValue(t, p, b, c, "SELECT v FROM userop_semantic_sentinel", "INT4", int64(42))
			})
		}
	})
}
