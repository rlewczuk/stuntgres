package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Independent release-source expectations; pinned18.6 must settle every field
// before production edits. Generated CTE/projection nodes have no source position.
func TestReferenceRecursiveViewDiagnostics(t *testing.T) {
	recursiveViewReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE RECURSIVE VIEW rv_valid(n) AS SELECT 7", 0))
		for _, tc := range []struct{ body, code, message, marker, hint string }{
			{"SELECT n FROM rv_diag", "42P19", `recursive query "rv_diag" does not have the form non-recursive-term UNION [ALL] recursive-term`, "", ""},
			{"SELECT n FROM rv_diag UNION ALL SELECT 1", "42P19", `recursive reference to query "rv_diag" must not appear within its non-recursive term`, "rv_diag UNION", ""},
			{"SELECT 1 UNION ALL SELECT a.n FROM rv_diag a CROSS JOIN rv_diag b", "42P19", `recursive reference to query "rv_diag" must not appear more than once`, "rv_diag b", ""},
			{"SELECT 1 UNION ALL SELECT (SELECT n FROM rv_diag)", "42P19", `recursive reference to query "rv_diag" must not appear within a subquery`, "rv_diag)", ""},
			{"SELECT 1 UNION ALL SELECT n::bigint FROM rv_diag", "42804", `recursive query "rv_diag" column 1 has type integer in non-recursive term but type bigint overall`, "1 UNION", "Cast the output of the non-recursive term to the correct type."},
			{"SELECT 1 UNION ALL SELECT n,2 FROM rv_diag", "42601", "each UNION query must have the same number of columns", "n,2", ""},
			{"SELECT '7' UNION ALL SELECT n+1 FROM rv_diag", "42883", "operator does not exist: text + integer", "+1", "No operator matches the given name and argument types. You might need to add explicit type casts."},
			{"SELECT 1 UNION ALL SELECT n+1 FROM public.rv_diag", "42P01", `relation "public.rv_diag" does not exist`, "public.rv_diag", ""},
			{"SELECT $1", "42P02", "there is no parameter $1", "$1", ""},
			{"SELECT 1 EXCEPT SELECT n FROM rv_diag", "42P19", `recursive query "rv_diag" does not have the form non-recursive-term UNION [ALL] recursive-term`, "", ""},
		} {
			for _, prefix := range []string{"", "/*é*/ "} {
				q := prefix + "CREATE RECURSIVE VIEW rv_diag(n) AS " + tc.body
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Hint = tc.hint
				if tc.marker != "" {
					want.Position = recursiveViewPosition(t, q, tc.marker)
				}
				stage := "execute"
				if !b.PG && tc.code == "42P19" {
					stage = "prepare" // Native lexical recursion validation precedes utility execution.
				}
				recursiveViewAnalysisFailure(t, p, b, c, q, want, stage)
				run(fixture.Select("SELECT viewname FROM pg_catalog.pg_views WHERE schemaname='public' AND viewname='rv_diag'", []string{"viewname"}, []string{"NAME"}, nil))
				run(recursiveViewRows("SELECT n FROM rv_valid", "INT4", 7))
			}
		}
		run(fixture.Exec("CREATE RECURSIVE VIEW rv_shadow(n) AS WITH rv_shadow(n) AS (SELECT 7) SELECT n FROM rv_shadow", 0))
		run(recursiveViewRows("SELECT n FROM rv_shadow", "INT4", 7))
	})
}
