package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExtremaViewCollation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE extrema_view_coll(a TEXT COLLATE \"C\",b TEXT COLLATE \"POSIX\")", 0))
			for _, prepared := range []bool{false, true} {
				// This is view-column metadata validation, not an aggregate
				// comparison: even an empty source cannot publish such a column.
				q := "CREATE VIEW extrema_view_conflict AS SELECT min(COALESCE(a,b)) AS lo,max(COALESCE(a,b)) AS hi FROM extrema_view_coll"
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P22", Message: `could not determine which collation to use for view column "lo"`, Hint: "Use the COLLATE clause to set the collation explicitly."})
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				// Reusing the failed name proves no partial catalog publication.
				s = fixture.Exec("CREATE VIEW extrema_view_conflict AS SELECT min(COALESCE(a,b) COLLATE \"C\") AS lo,max(COALESCE(a,b) COLLATE \"C\") AS hi FROM extrema_view_coll", 0)
				s.Prepared = prepared
				run(s)
				fixture.AnyValueRoutes(t, p, backend, fixture.Select("SELECT lo,hi FROM extrema_view_conflict", []string{"lo", "hi"}, []string{"TEXT", "TEXT"}, [][]any{{nil, nil}}))
				run(fixture.Exec("INSERT INTO extrema_view_coll VALUES('a','b'),('c','d')", 2))
				fixture.AnyValueRoutes(t, p, backend, fixture.Select("SELECT lo,hi FROM extrema_view_conflict", []string{"lo", "hi"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "c"}}))
				run(fixture.Exec("DROP VIEW extrema_view_conflict", 0))
				run(fixture.Exec("DELETE FROM extrema_view_coll", 2))
			}
		})
	}
}
