package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLtreeLqueryAlternatives(t *testing.T) {
	ltreeLqueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ltreeInstall(t, p, b, c)
		for _, tc := range []struct{ path, queries string; want any }{
			{"'Top.A.Child'::ltree", `'{Other.*,"Top.*{1,2}.Child"}'::lquery[]`, true},
			{"'Top.A.Child'::ltree", `'{"Top.*{1,2}.Child",Other.*}'::lquery[]`, true},
			{"'Top.A.Child'::ltree", "'{Other.*,Top.Child}'::lquery[]", false},
			{"'Top.A.Child'::ltree", "'{}'::lquery[]", false},
			{"'Top.A.Child'::ltree", `'[0:1]={Other.*,"Top.*{1,2}.Child"}'::lquery[]`, true},
			{"NULL::ltree", "'{*}'::lquery[]", nil}, {"'Top'::ltree", "NULL::lquery[]", nil},
			{"NULL::ltree", "NULL::lquery[]", nil},
		} {
			q := "SELECT "+tc.path+" ? "+tc.queries+" AS forward,"+tc.queries+" ? "+tc.path+" AS reverse"
			s := ltreeLqueryPair(b, q, tc.want)
			ltreeLqueryRun(t, p, b, c, s); ltreeLqueryRun(t, p, b, c, fixture.Prepared(s))
		}
		for _, tc := range []struct{ expression string; want any }{
			{"'{Top,A.Child}'::ltree[] ~ 'Top.*.Child'::lquery", false},
			{"'Top.*.Child'::lquery ~ '{Top,A.Child}'::ltree[]", false},
			{"'{Other,Top.A.Child}'::ltree[] ~ 'Top.*{1,2}.Child'::lquery", true},
			{"'Top.*{1,2}.Child'::lquery ~ '{Other,Top.A.Child}'::ltree[]", true},
			{`'{Other,Top.A.Child}'::ltree[] ? '{Other.*,"Top.*{1,2}.Child"}'::lquery[]`, true},
			{`'{Other.*,"Top.*{1,2}.Child"}'::lquery[] ? '{Other,Top.A.Child}'::ltree[]`, true},
		} { ltreeLqueryRun(t, p, b, c, ltreeArrayResult(b, "SELECT "+tc.expression+" AS v", tc.want)) }
		for _, tc := range []struct{ array, code, message string }{
			{"'{{a},{b}}'", "2202E", "array must be one-dimensional"},
			{"'{NULL}'", "22004", "array must not contain nulls"},
		} {
			for _, q := range []string{"SELECT 'a'::ltree ? "+tc.array+"::lquery[]", "SELECT "+tc.array+"::lquery[] ? 'a'::ltree"} {
				for _, prepared := range []bool{false, true} {
					ltreeError(t, p, b, c, q, prepared, true, false, fixture.ControlsReferenceDiagnostic(tc.code, tc.message))
				}
			}
		}
	})
}
