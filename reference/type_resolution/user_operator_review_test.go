package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUserOperatorReviewRegressions(t *testing.T) {
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE userop_review_sentinel(v integer)",
			"CREATE OPERATOR public.#=# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4eq,COMMUTATOR=OPERATOR(public.#!#))",
			"CREATE OPERATOR public.#!# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4ne,COMMUTATOR=OPERATOR(public.#=#))")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO userop_review_sentinel VALUES (42)", 1))
		t.Run("commutator", func(t *testing.T) {
			userOperatorError(t, p, b, c, "ALTER OPERATOR public.#=# (int4,int4) SET (COMMUTATOR=OPERATOR(public.#=#))", "42P13", `operator attribute "commutator" cannot be changed if it has already been set`, "", "", "", false)
			userOperatorValue(t, p, b, c, "SELECT oprcom='public.#!#(int4,int4)'::regoperator::oid AS v FROM pg_operator WHERE oid='public.#=#(int4,int4)'::regoperator", "BOOL", true)
			userOperatorValue(t, p, b, c, "SELECT oprcom='public.#=#(int4,int4)'::regoperator::oid AS v FROM pg_operator WHERE oid='public.#!#(int4,int4)'::regoperator", "BOOL", true)
			userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#=#) 2 AS v", "BOOL", true)
			userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#!#) 2 AS v", "BOOL", false)
			// Existing value is a genuine no-op; an initially unset self-link is legal.
			userOperatorExec(t, p, b, c,
				"ALTER OPERATOR public.#=# (int4,int4) SET (COMMUTATOR=OPERATOR(public.#!#))",
				"CREATE OPERATOR public.#?# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4eq)",
				"ALTER OPERATOR public.#?# (int4,int4) SET (COMMUTATOR=OPERATOR(public.#?#))",
				"ALTER OPERATOR public.#?# (int4,int4) SET (COMMUTATOR=OPERATOR(public.#?#))")
			userOperatorValue(t, p, b, c, "SELECT oprcom=oid AS v FROM pg_operator WHERE oid='public.#?#(int4,int4)'::regoperator", "BOOL", true)
		})
		t.Run("unknown_positions", func(t *testing.T) {
			userOperatorExec(t, p, b, c,
				"CREATE OPERATOR public.#~# (LEFTARG=float4,RIGHTARG=float8,FUNCTION=pg_catalog.float48pl)",
				"CREATE OPERATOR public.#~# (LEFTARG=float8,RIGHTARG=float4,FUNCTION=pg_catalog.float84pl)")
			userOperatorError(t, p, b, c, "SELECT '2' OPERATOR(public.#~#) '3'", "42725", "operator is not unique: unknown public.#~# unknown", "", "Could not choose a best candidate operator. You might need to add explicit type casts.", "12", true)
			userOperatorValue(t, p, b, c, "SELECT 2::float4 OPERATOR(public.#~#) 3::float8 AS v", "FLOAT8", float64(5))
			userOperatorValue(t, p, b, c, "SELECT 2::float8 OPERATOR(public.#~#) 3::float4 AS v", "FLOAT8", float64(5))
		})
		userOperatorValue(t, p, b, c, "SELECT v FROM userop_review_sentinel", "INT4", int64(42))
	})
}
