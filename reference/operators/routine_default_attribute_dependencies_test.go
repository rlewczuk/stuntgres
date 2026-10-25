package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineDefaultAttributeDependencies(t *testing.T) {
	for _, tc := range []struct {
		name, args, body, signature string
		attrs, pgCounts             []int64
	}{
		{"default_only", "x integer DEFAULT (NULL::public.rda_dep_t).a", "AS 'SELECT x'", "integer", []int64{1}, []int64{1}},
		{"repeated", "x integer DEFAULT (NULL::public.rda_dep_t).a,y integer DEFAULT (NULL::public.rda_dep_t).a", "AS 'SELECT x'", "integer,integer", []int64{1}, []int64{1}},
		// ProcedureCreate records SQL-standard body and declaration defaults
		// separately; REL_18_6 pg_depend.c does not globally deduplicate passes.
		{"body_same", "x integer DEFAULT (NULL::public.rda_dep_t).a", "RETURN (NULL::public.rda_dep_t).a", "integer", []int64{1}, []int64{2}},
		{"body_other", "x integer DEFAULT (NULL::public.rda_dep_t).a", "RETURN (NULL::public.rda_dep_t).b", "integer", []int64{1, 2}, []int64{1, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parametersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TABLE public.rda_dep_t(a integer,b integer)",
					"CREATE FUNCTION public.rda_dep_f("+tc.args+") RETURNS integer LANGUAGE SQL "+tc.body)
				counts := tc.pgCounts
				defaultAttributeEdges(t, p, b, c, "public.rda_dep_f("+tc.signature+")", "public.rda_dep_t", tc.attrs, counts)
				// Both existing native publication and PostgreSQL retain the
				// independently recorded body/default pass rows.
				typeCount := int64(1)
				if tc.name == "body_same" || tc.name == "body_other" {
					typeCount = 2
				}
				defaultAttributeTypeEdge(t, p, b, c, "public.rda_dep_f("+tc.signature+")", "public.rda_dep_t", typeCount)
				parametersValue(t, p, b, c, "SELECT public.rda_dep_f() AS v", "v", "INT4", nil)
			})
		})
	}
}
