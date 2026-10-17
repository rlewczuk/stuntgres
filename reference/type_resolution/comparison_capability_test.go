package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 typcache.c rejects record/array leaf capabilities recursively.
// This differs from direct record_eq/record_cmp's first-decisive-field frontier.
func TestReferenceComparisonNestedCapabilities(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, q := range []string{
				"CREATE TYPE comparison_poison AS (x integer,j json)",
				"CREATE TYPE comparison_outer AS (p comparison_poison,z integer)",
				"CREATE TYPE comparison_array_outer AS (p comparison_poison[],z integer)",
				"CREATE DOMAIN comparison_poison_domain AS comparison_poison",
				"CREATE DOMAIN comparison_poison_array_domain AS comparison_poison[]",
				"CREATE TYPE comparison_domain_outer AS (p comparison_poison_domain,z integer)",
				"CREATE TYPE comparison_array_domain_outer AS (p comparison_poison_array_domain,z integer)",
			} {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
			}
			poison := "ROW(1,NULL)::comparison_poison"
			for _, tc := range []struct{ name, value, leaf string }{
				{"nested", "ROW(" + poison + ",0)::comparison_outer", "comparison_poison"},
				{"array", "ARRAY[" + poison + "]", "comparison_poison"},
				{"nested_array", "ROW(ARRAY[" + poison + "],0)::comparison_array_outer", "comparison_poison[]"},
				{"nested_domain", "ROW((" + poison + ")::comparison_poison_domain,0)::comparison_domain_outer", "comparison_poison_domain"},
				{"nested_array_domain", "ROW(ARRAY[" + poison + "]::comparison_poison_array_domain,0)::comparison_array_domain_outer", "comparison_poison_array_domain"},
			} {
				for _, op := range []string{"=", "<"} {
					t.Run(tc.name+op, func(t *testing.T) {
						message := "could not identify an equality operator for type " + tc.leaf
						if op == "<" {
							message = "could not identify a comparison function for type " + tc.leaf
						}
						s := fixture.ImplicitError("SELECT "+tc.value+" "+op+" "+tc.value+" AS v", fixture.Diagnostic{Code: "42883", Message: message})
						s.SelectRows, s.ErrorStage = true, "execute"
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
					})
				}
			}
			comparisonCheckpointBool(t, p, b, "ROW(0,NULL)::comparison_poison < ROW(1,NULL)::comparison_poison", true)
		})
	}
}

// Released pg_cast.dat plus pg_proc.dat identities, not native projections.
// These are real cast rows, not synthetic comparison input-pair signatures.
func TestReferenceComparisonCatalogConversions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			q := "SELECT castsource,casttarget,castfunc,castcontext,castmethod FROM pg_cast WHERE (castsource=18 AND casttarget=25) OR (castsource=194 AND casttarget=25) OR (castsource=1083 AND casttarget=1186) ORDER BY castsource"
			// OID has distinct public database/sql codecs: native exposes its
			// unsigned logical value as int64; lib/pq exposes OID text.
			rows := [][]any{{"18", "25", "946", "i", "f"}, {"194", "25", "0", "i", "b"}, {"1083", "1186", "1370", "i", "f"}}
			if !b.PG {
				rows = [][]any{{int64(18), int64(25), int64(946), "i", "f"}, {int64(194), int64(25), int64(0), "i", "b"}, {int64(1083), int64(1186), int64(1370), "i", "f"}}
			}
			comparisonCheckpointRoutes(t, p, b, fixture.Select(q,
				[]string{"castsource", "casttarget", "castfunc", "castcontext", "castmethod"},
				[]string{"OID", "OID", "OID", "CHAR", "CHAR"},
				rows))
		})
	}
}
