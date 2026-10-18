package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUserOperatorCreationCheckpoints(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"userop_shell_owner", "userop_shell_creator"})
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		userOperatorExec(t, p, b, c, "CREATE ROLE userop_shell_owner LOGIN", "CREATE ROLE userop_shell_creator LOGIN", "GRANT USAGE,CREATE ON SCHEMA public TO userop_shell_owner,userop_shell_creator", "CREATE TABLE userop_create_sentinel(v integer)", "GRANT SELECT ON userop_create_sentinel TO userop_shell_creator", "CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl)", "CREATE OPERATOR public.#=# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,COMMUTATOR=OPERATOR(public.#?#))", "ALTER OPERATOR public.#?# (int4,int4) OWNER TO userop_shell_owner")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO userop_create_sentinel VALUES(42)", 1))
		for _, tc := range []struct{ option, code, message string }{
			{"RESTRICT=userop_missing_estimator", "42883", "function userop_missing_estimator(internal, oid, internal, integer) does not exist"},
			{"HASHES", "42P13", "only boolean operators can hash"},
		} {
			t.Run("duplicate_"+tc.option, func(t *testing.T) {
				userOperatorError(t, p, b, c, "CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl,"+tc.option+")", tc.code, tc.message, "", "", "", false)
				userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#~#) 3 AS v", "INT4", int64(5))
				userOperatorValue(t, p, b, c, "SELECT NOT oprcanhash AND oprrest::oid=0 AS v FROM pg_operator WHERE oid='public.#~#(int4,int4)'::regoperator", "BOOL", true)
			})
		}
		userOperatorExec(t, p, b, c, "SET ROLE userop_shell_creator")
		for _, tc := range []struct{ option, code, message string }{
			{"RESTRICT=userop_missing_estimator", "42883", "function userop_missing_estimator(internal, oid, internal, integer) does not exist"},
			{"HASHES", "42P13", "only boolean operators can hash"},
		} {
			t.Run("shell_"+tc.option, func(t *testing.T) {
				userOperatorError(t, p, b, c, "CREATE OPERATOR public.#?# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl,"+tc.option+")", tc.code, tc.message, "", "", "", false)
				userOperatorValue(t, p, b, c, "SELECT oprcode::oid=0 AND oprresult=0 AND oprowner=(SELECT oid FROM pg_roles WHERE rolname='userop_shell_owner') AS v FROM pg_operator WHERE oid='public.#?#(int4,int4)'::regoperator", "BOOL", true)
				userOperatorValue(t, p, b, c, "SELECT v FROM userop_create_sentinel", "INT4", int64(42))
			})
		}
		userOperatorError(t, p, b, c, "CREATE OPERATOR public.#?# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq)", "42501", "must be owner of operator #?#", "", "", "", false)
		userOperatorExec(t, p, b, c, "RESET ROLE")
		userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#=#) 2 AS v", "BOOL", true)
		userOperatorValue(t, p, b, c, "SELECT v FROM userop_create_sentinel", "INT4", int64(42))
	})
}

func TestReferenceUserOperatorParameterCheckpoints(t *testing.T) {
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE TABLE userop_parameter_sentinel(v integer)", "CREATE OPERATOR public.##~ (RIGHTARG=int4,FUNCTION=int4um)", "CREATE OPERATOR public.##! (RIGHTARG=int4,FUNCTION=int4um)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO userop_parameter_sentinel VALUES(42)", 1))
		for _, tc := range []struct{ options, message string }{
			{"NEGATOR=OPERATOR(public.##@),HASHES", "only binary operators can hash"},
			{"NEGATOR=OPERATOR(public.##@),MERGES", "only binary operators can merge join"},
			{"RESTRICT=eqsel,HASHES", "only binary operators can hash"},
			{"NEGATOR=OPERATOR(public.##@),JOIN=eqjoinsel,MERGES,HASHES", "only binary operators can have join selectivity"},
		} {
			t.Run("create_"+tc.options, func(t *testing.T) {
				userOperatorError(t, p, b, c, "CREATE OPERATOR public.##? (RIGHTARG=int4,FUNCTION=int4um,"+tc.options+")", "42P13", tc.message, "", "", "", false)
				userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname IN ('##?','##@')", "INT8", int64(0))
				userOperatorValue(t, p, b, c, "SELECT v FROM userop_parameter_sentinel", "INT4", int64(42))
			})
		}
		for _, tc := range []struct{ options, message string }{
			{"NEGATOR=OPERATOR(public.##!),HASHES", "only binary operators can hash"},
			{"NEGATOR=OPERATOR(public.##!),MERGES", "only binary operators can merge join"},
			{"RESTRICT=eqsel,HASHES", "only binary operators can hash"},
			{"NEGATOR=OPERATOR(public.##!),JOIN=eqjoinsel,MERGES,HASHES", "only binary operators can have join selectivity"},
		} {
			t.Run("alter_"+tc.options, func(t *testing.T) {
				userOperatorError(t, p, b, c, "ALTER OPERATOR public.##~ (NONE,int4) SET ("+tc.options+")", "42P13", tc.message, "", "", "", false)
				for _, name := range []string{"##~", "##!"} {
					userOperatorValue(t, p, b, c, "SELECT oprcom=0 AND oprnegate=0 AND NOT oprcanhash AND NOT oprcanmerge AS v FROM pg_operator WHERE oid='public."+name+"(NONE,int4)'::regoperator", "BOOL", true)
					userOperatorValue(t, p, b, c, "SELECT OPERATOR(public."+name+") 3 AS v", "INT4", int64(-3))
				}
				userOperatorValue(t, p, b, c, "SELECT v FROM userop_parameter_sentinel", "INT4", int64(42))
			})
		}
	})
}

func TestReferenceUserOperatorPeerSetupCheckpoints(t *testing.T) {
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE TABLE userop_peer_sentinel(v integer)",
			"CREATE OPERATOR public.#=# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,COMMUTATOR=OPERATOR(public.#!#))",
			"CREATE OPERATOR public.#!# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4ne,COMMUTATOR=OPERATOR(public.#=#))",
			"CREATE OPERATOR public.#@# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4ne)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO userop_peer_sentinel VALUES(42)", 1))
		for _, tc := range []struct{ negator, code, message string }{
			{"OPERATOR(public.#?#)", "42P13", "operator cannot be its own negator"},
			{"'abc'", "42602", `"abc" is not a valid operator name`},
			{"OPERATOR(userop_missing_schema.#!#)", "3F000", `schema "userop_missing_schema" does not exist`},
			{"OPERATOR(public.#@#)", "42P13", "commutator operator #=# is already the commutator of operator #!#"},
		} {
			t.Run(tc.negator, func(t *testing.T) {
				userOperatorError(t, p, b, c, "CREATE OPERATOR public.#?# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,COMMUTATOR=OPERATOR(public.#=#),NEGATOR="+tc.negator+")", tc.code, tc.message, "", "", "", false)
				userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname='#?#'", "INT8", int64(0))
				userOperatorValue(t, p, b, c, "SELECT oprcom='public.#!#(int4,int4)'::regoperator::oid AND oprnegate=0 AS v FROM pg_operator WHERE oid='public.#=#(int4,int4)'::regoperator", "BOOL", true)
				userOperatorValue(t, p, b, c, "SELECT oprcom=0 AND oprnegate=0 AS v FROM pg_operator WHERE oid='public.#@#(int4,int4)'::regoperator", "BOOL", true)
				userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#=#) 2 AS v", "BOOL", true)
				userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#!#) 2 AS v", "BOOL", false)
				userOperatorValue(t, p, b, c, "SELECT v FROM userop_peer_sentinel", "INT4", int64(42))
			})
		}
	})
}
