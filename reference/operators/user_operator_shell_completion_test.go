package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent expectations come from REL_18_6 OperatorCreate/OperatorUpd:
// completion replaces the shell tuple, not its OID, and reciprocal links may
// remain asymmetric. A later third operator cannot steal a populated link.
func TestReferenceUserOperatorShellCompletionReciprocalConflicts(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"userop_shell_old_owner"})
	fixture.SchemaReferenceCleanup(t, "userop_shell_complete")
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE ROLE userop_shell_old_owner",
			"CREATE SCHEMA userop_shell_complete",
			"GRANT CREATE ON SCHEMA userop_shell_complete TO userop_shell_old_owner",
			"CREATE OPERATOR userop_shell_complete.#=# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,COMMUTATOR=OPERATOR(userop_shell_complete.#!#),NEGATOR=OPERATOR(userop_shell_complete.#!#))",
			"ALTER OPERATOR userop_shell_complete.#!# (int4,int4) OWNER TO userop_shell_old_owner")
		var shellOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'userop_shell_complete.#!#(int4,int4)'::regoperator::oid").Scan(&shellOID); err != nil || shellOID == 0 {
			t.Fatal(shellOID, err)
		}
		userOperatorValue(t, p, b, c, "SELECT oprcode::oid=0 AND oprresult=0 AND oprcom='userop_shell_complete.#=#(int4,int4)'::regoperator::oid AND oprnegate=oprcom AND oprowner=(SELECT oid FROM pg_roles WHERE rolname='userop_shell_old_owner') AS v FROM pg_operator WHERE oid='userop_shell_complete.#!#(int4,int4)'::regoperator", "BOOL", true)
		userOperatorExec(t, p, b, c, "CREATE OPERATOR userop_shell_complete.#!# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4ne)")
		userOperatorValue(t, p, b, c, "SELECT 'userop_shell_complete.#!#(int4,int4)'::regoperator::oid::int8 AS v", "INT8", shellOID)
		userOperatorValue(t, p, b, c, "SELECT oprcode::oid=144 AND oprresult=16 AND oprcom=0 AND oprnegate=0 AND oprowner=(SELECT oid FROM pg_roles WHERE rolname=current_user) AS v FROM pg_operator WHERE oid='userop_shell_complete.#!#(int4,int4)'::regoperator", "BOOL", true)
		unchanged := "SELECT oprcom='userop_shell_complete.#!#(int4,int4)'::regoperator::oid AND oprnegate=oprcom AS v FROM pg_operator WHERE oid='userop_shell_complete.#=#(int4,int4)'::regoperator"
		userOperatorValue(t, p, b, c, unchanged, "BOOL", true)
		for _, tc := range []struct{ symbol, attribute string }{{"#?#", "commutator"}, {"#@#", "negator"}} {
			query := "CREATE OPERATOR userop_shell_complete." + tc.symbol + " (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq," + tc.attribute + "=OPERATOR(userop_shell_complete.#=#))"
			userOperatorError(t, p, b, c, query, "42P13", tc.attribute+" operator #=# is already the "+tc.attribute+" of operator #!#", "", "", "", false)
			userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_operator WHERE oprnamespace='userop_shell_complete'::regnamespace AND oprname='"+tc.symbol+"'", "INT8", int64(0))
			userOperatorValue(t, p, b, c, unchanged, "BOOL", true)
			userOperatorValue(t, p, b, c, "SELECT oprcom=0 AND oprnegate=0 AS v FROM pg_operator WHERE oid='userop_shell_complete.#!#(int4,int4)'::regoperator", "BOOL", true)
		}
		userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(userop_shell_complete.#!#) 3 AS v", "BOOL", true)
	})
}
