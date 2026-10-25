package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineDefaultAttributeCalls(t *testing.T) {
	for _, kind := range []string{"table", "type"} {
		t.Run(kind, func(t *testing.T) {
			parametersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				create := "CREATE TABLE public.rda_calls_t(a integer,b integer)"
				if kind == "type" {
					create = "CREATE TYPE public.rda_calls_t AS (a integer,b integer)"
				}
				userOperatorExec(t, p, b, c, create,
					"CREATE FUNCTION public.rda_calls_q(x integer DEFAULT (NULL::public.rda_calls_t).a) RETURNS integer LANGUAGE SQL AS 'SELECT x'",
					"CREATE FUNCTION public.rda_calls_s(x integer DEFAULT (NULL::public.rda_calls_t).a) RETURNS integer LANGUAGE SQL RETURN x")
				for _, f := range []string{"public.rda_calls_q", "public.rda_calls_s"} {
					defaultAttributeCalls(t, p, b, c, f)
					defaultAttributeEdges(t, p, b, c, f+"(integer)", "public.rda_calls_t", []int64{1}, []int64{1})
					defaultAttributeTypeEdge(t, p, b, c, f+"(integer)", "public.rda_calls_t", 1)
				}
			})
		})
	}
}
