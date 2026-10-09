package reference_test

import "testing"

func TestReferenceExtremaViewCollation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE extrema_view_coll(a TEXT COLLATE \"C\",b TEXT COLLATE \"POSIX\")", 0))
			for _, prepared := range []bool{false, true} {
				// This is view-column metadata validation, not an aggregate
				// comparison: even an empty source cannot publish such a column.
				q := "CREATE VIEW extrema_view_conflict AS SELECT min(COALESCE(a,b)) AS lo,max(COALESCE(a,b)) AS hi FROM extrema_view_coll"
				s := referenceImplicitError(q, referenceDiagnostic{Code: "42P22", Message: `could not determine which collation to use for view column "lo"`, Hint: "Use the COLLATE clause to set the collation explicitly."})
				s.prepared, s.errorStage = prepared, "execute"
				run(s)
				// Reusing the failed name proves no partial catalog publication.
				s = referenceExec("CREATE VIEW extrema_view_conflict AS SELECT min(COALESCE(a,b) COLLATE \"C\") AS lo,max(COALESCE(a,b) COLLATE \"C\") AS hi FROM extrema_view_coll", 0)
				s.prepared = prepared
				run(s)
				anyValueRoutes(t, p, backend, referenceSelect("SELECT lo,hi FROM extrema_view_conflict", []string{"lo", "hi"}, []string{"TEXT", "TEXT"}, [][]any{{nil, nil}}))
				run(referenceExec("INSERT INTO extrema_view_coll VALUES('a','b'),('c','d')", 2))
				anyValueRoutes(t, p, backend, referenceSelect("SELECT lo,hi FROM extrema_view_conflict", []string{"lo", "hi"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "c"}}))
				run(referenceExec("DROP VIEW extrema_view_conflict", 0))
				run(referenceExec("DELETE FROM extrema_view_coll", 2))
			}
		})
	}
}
