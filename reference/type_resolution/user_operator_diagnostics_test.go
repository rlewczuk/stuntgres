package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUserOperatorDiagnosticsAtomicity(t *testing.T) {
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE TABLE userop_sentinel(v integer)", "CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO userop_sentinel VALUES (42)", 1))
		for _, tc := range []struct{ name, q, code, message, detail, position string }{
			{"missing_function", "CREATE OPERATOR public.#?# (LEFTARG=int4,RIGHTARG=int4)", "42P13", "operator function must be specified", "", ""},
			{"missing_types", "CREATE OPERATOR public.#?# (FUNCTION=int4pl)", "42P13", "operator argument types must be specified", "", ""},
			{"postfix", "CREATE OPERATOR public.#?# (LEFTARG=int4,FUNCTION=int4um)", "42P13", "operator right argument type must be specified", "Postfix operators are not supported.", ""},
			{"setof", "CREATE OPERATOR public.#?# (LEFTARG=SETOF int4,RIGHTARG=int4,FUNCTION=int4pl)", "42P13", "SETOF type not allowed for operator argument", "", ""},
			{"wrong_exact", "CREATE OPERATOR public.#?# (LEFTARG=int2,RIGHTARG=int2,FUNCTION=int4pl)", "42883", "function int4pl(smallint, smallint) does not exist", "", ""},
			{"unknown_function", "CREATE OPERATOR public.#?# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=userop_missing_function)", "42883", "function userop_missing_function(integer, integer) does not exist", "", ""},
			{"duplicate", "CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4mi)", "42723", "operator #~# already exists", "", ""},
			{"alter_function", "ALTER OPERATOR public.#~# (int4,int4) SET (FUNCTION=int4mi)", "42601", `operator attribute "function" cannot be changed`, "", ""},
			{"alter_unknown", "ALTER OPERATOR public.#~# (int4,int4) SET (userop_unknown=true)", "42601", `operator attribute "userop_unknown" not recognized`, "", ""},
			{"multidrop_missing", "DROP OPERATOR public.#~# (int4,int4),public.#?# (int4,int4)", "42883", "operator does not exist: integer public.#?# integer", "", ""},
			{"too_long", "CREATE OPERATOR public." + strings.Repeat("~", 64) + " (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl)", "42601", `operator too long at or near "` + strings.Repeat("~", 64) + `"`, "", "24"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				userOperatorError(t, p, b, c, tc.q, tc.code, tc.message, tc.detail, "", tc.position, false)
				userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#~#) 3 AS v", "INT4", int64(5))
				userOperatorValue(t, p, b, c, "SELECT v FROM userop_sentinel", "INT4", int64(42))
				userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname='#?#'", "INT8", int64(0))
			})
		}
		userOperatorError(t, p, b, c, "SELECT 2 OPERATOR(public.#?#) 3", "42883", "operator does not exist: integer public.#?# integer", "", "No operator matches the given name and argument types. You might need to add explicit type casts.", "10", true)
		userOperatorExec(t, p, b, c, "CREATE OPERATOR public."+strings.Repeat("~", 63)+" (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl)")
	})
}
func TestReferenceUserOperatorOptionsShells(t *testing.T) {
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE OPERATOR public.#=# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,COMMUTATOR=OPERATOR(public.#=#),NEGATOR=OPERATOR(public.#!#),RESTRICT=eqsel,JOIN=eqjoinsel,HASHES,MERGES)")
		var shell int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'public.#!#(int4,int4)'::regoperator::oid").Scan(&shell); err != nil || shell == 0 {
			t.Fatal("shell identity", shell, err)
		}
		userOperatorValue(t, p, b, c, "SELECT oprcode::oid::int8 AS v FROM pg_operator WHERE oid='public.#!#(int4,int4)'::regoperator", "INT8", int64(0))
		userOperatorValue(t, p, b, c, "SELECT oprresult::int8 AS v FROM pg_operator WHERE oid='public.#!#(int4,int4)'::regoperator", "INT8", int64(0))
		userOperatorValue(t, p, b, c, "SELECT oprcanhash AND oprcanmerge AND oprrest::oid=101 AND oprjoin::oid=105 AS v FROM pg_operator WHERE oid='public.#=#(int4,int4)'::regoperator", "BOOL", true)
		userOperatorError(t, p, b, c, "SELECT 2 OPERATOR(public.#!#) 3", "42883", "operator is only a shell: integer public.#!# integer", "", "", "10", true)
		userOperatorExec(t, p, b, c, "CREATE OPERATOR public.#!# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4ne,NEGATOR=OPERATOR(public.#=#))")
		var completed int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'public.#!#(int4,int4)'::regoperator::oid").Scan(&completed); err != nil || completed != shell {
			t.Fatal("shell completion changed identity", shell, completed, err)
		}
		userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#!#) 3 AS v", "BOOL", true)
		userOperatorValue(t, p, b, c, "SELECT oprnegate='public.#=#(int4,int4)'::regoperator::oid AS v FROM pg_operator WHERE oid='public.#!#(int4,int4)'::regoperator", "BOOL", true)
		userOperatorExec(t, p, b, c, "ALTER OPERATOR public.#=# (int4,int4) SET (RESTRICT=NONE,JOIN=NONE)")
		userOperatorValue(t, p, b, c, "SELECT oprrest::oid=0 AND oprjoin::oid=0 AS v FROM pg_operator WHERE oid='public.#=#(int4,int4)'::regoperator", "BOOL", true)
		userOperatorExec(t, p, b, c, "ALTER OPERATOR public.#=# (int4,int4) SET (RESTRICT=eqsel,JOIN=eqjoinsel,HASHES=true,MERGES=true)")
		for _, tc := range []struct{ name, opts, message string }{
			{"self_negator", "LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,NEGATOR=OPERATOR(public.#?#)", "operator cannot be its own negator"},
			{"unary_commutator", "RIGHTARG=int4,FUNCTION=int4um,COMMUTATOR=OPERATOR(public.#~#)", "only binary operators can have commutators"},
			{"unary_join", "RIGHTARG=int4,FUNCTION=int4um,JOIN=eqjoinsel", "only binary operators can have join selectivity"},
			{"unary_hash", "RIGHTARG=int4,FUNCTION=int4um,HASHES", "only binary operators can hash"},
			{"unary_merge", "RIGHTARG=int4,FUNCTION=int4um,MERGES", "only binary operators can merge join"},
			{"nonbool_negator", "LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl,NEGATOR=OPERATOR(public.#!#)", "only boolean operators can have negators"},
			{"nonbool_restrict", "LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl,RESTRICT=eqsel", "only boolean operators can have restriction selectivity"},
			{"nonbool_join", "LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl,JOIN=eqjoinsel", "only boolean operators can have join selectivity"},
			{"nonbool_hash", "LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl,HASHES", "only boolean operators can hash"},
			{"nonbool_merge", "LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl,MERGES", "only boolean operators can merge join"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				userOperatorError(t, p, b, c, "CREATE OPERATOR public.#?# ("+tc.opts+")", "42P13", tc.message, "", "", "", false)
			})
		}
		for _, attr := range []string{"hashes", "merges"} {
			userOperatorError(t, p, b, c, "ALTER OPERATOR public.#=# (int4,int4) SET ("+attr+"=false)", "42P13", `operator attribute "`+attr+`" cannot be changed if it has already been set`, "", "", "", false)
		}
		userOperatorError(t, p, b, c, "CREATE OPERATOR public.#?# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,RESTRICT=int4pl)", "42883", "function int4pl(internal, oid, internal, integer) does not exist", "", "", "", false)
		userOperatorExec(t, p, b, c, "DROP OPERATOR public.#!# (int4,int4)")
		userOperatorValue(t, p, b, c, "SELECT oprnegate::int8 AS v FROM pg_operator WHERE oid='public.#=#(int4,int4)'::regoperator", "INT8", int64(0))
		userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#=#) 2 AS v", "BOOL", true)
	})
}
