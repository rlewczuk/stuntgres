package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceComparisonDeferredRecord(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TYPE comparison_record_route AS (x integer,y integer)", 0))
			for _, op := range comparisonSpellings {
				for _, operands := range [][2]string{
					{"NULL::record", "NULL::record"},
					{"NULL::record", "ROW(1,2)::comparison_record_route"},
					{"ROW(1,2)::comparison_record_route", "NULL::record"},
					{"ROW(1,2)::comparison_record_route", "NULL"},
					{"NULL", "ROW(1,2)::comparison_record_route"},
				} {
					t.Run(operands[0]+op+operands[1], func(t *testing.T) {
						comparisonCheckpointBool(t, p, b, operands[0]+" "+op+" "+operands[1], nil)
					})
				}
			}
			for _, expr := range []string{
				"NULL::record IS NULL", "NULL::record IS NOT DISTINCT FROM NULL::record",
				"NULL::record IS DISTINCT FROM ROW(1,2)::comparison_record_route",
			} {
				comparisonCheckpointBool(t, p, b, expr, true)
			}
			for _, expr := range []string{
				"ROW(1,2)::comparison_record_route = '(1,2)'",
				"'(1,2)' = ROW(1,2)::comparison_record_route",
			} {
				q := "SELECT " + expr + " AS v"
				comparisonCheckpointAnalysis(t, p, b, q, fixture.Diagnostic{Code: "0A000", Message: "input of anonymous composite types is not implemented", Position: comparisonCheckpointPosition(q, "'(1,2)'")})
			}
		})
	}
}

// REL_18_6 array_userfuncs.c:array_sort_internal exits on first dimension <2
// before requesting a recursive leaf ordering capability from typcache.
func TestReferenceComparisonCompositeSortConsumer(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, q := range []string{
				"CREATE TYPE comparison_sort_pair AS (x integer,y integer)",
				"CREATE TYPE comparison_sort_poison AS (x integer,j json)",
			} {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
			}
			for _, expr := range []string{
				"array_sort(ARRAY[ROW(1,NULL)::comparison_sort_pair,ROW(1,2)::comparison_sort_pair,ROW(0,9)::comparison_sort_pair]) = ARRAY[ROW(0,9)::comparison_sort_pair,ROW(1,2)::comparison_sort_pair,ROW(1,NULL)::comparison_sort_pair]",
				"array_sort(ARRAY[ROW(1,2)::comparison_sort_pair,ROW(1,NULL)::comparison_sort_pair],true) = ARRAY[ROW(1,NULL)::comparison_sort_pair,ROW(1,2)::comparison_sort_pair]",
				"array_sort(NULL::comparison_sort_poison[]) IS NULL",
				"cardinality(array_sort(ARRAY[ROW(1,NULL)::comparison_sort_poison])) = 1",
				"cardinality(array_sort(ARRAY[]::comparison_sort_poison[])) = 0",
			} {
				comparisonCheckpointBool(t, p, b, expr, true)
			}
			q := "SELECT array_sort(ARRAY[ROW(1,NULL)::comparison_sort_poison,ROW(2,NULL)::comparison_sort_poison]) AS v"
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "could not identify a comparison function for type comparison_sort_poison"})
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		})
	}
}
