package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRecursiveViewColumns(t *testing.T) {
	recursiveViewReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE RECURSIVE VIEW rv_short(n) AS SELECT 1,0 AS depth UNION SELECT n,depth+1 FROM rv_short WHERE depth<2", 0))
		for _, prepared := range []bool{false, true} {
			s := recursiveViewRows("SELECT * FROM rv_short", "INT4", 1, 1, 1)
			s.Prepared = prepared
			s.CheckMetadata = recursiveViewMetadata(b, s.Columns, s.Types)
			run(s)
		}
		for _, tc := range []struct{ q, code, message, marker, hint, pgStage string }{
			{"CREATE RECURSIVE VIEW rv_bad AS SELECT 1", "42601", `syntax error at or near "AS"`, "AS SELECT", "", "prepare"},
			{"CREATE RECURSIVE VIEW rv_bad() AS SELECT 1", "42601", `syntax error at or near ")"`, ") AS", "", "prepare"},
			{"CREATE RECURSIVE VIEW rv_bad(n,m) AS SELECT 1", "42P10", `WITH query "rv_bad" has 1 columns available but 2 columns specified`, "", "", "execute"},
			{"CREATE RECURSIVE VIEW rv_bad(n,n) AS SELECT 1,2", "42702", `column reference "n" is ambiguous`, "", "", "execute"},
			{"CREATE RECURSIVE VIEW rv_bad(n) AS SELECT 1,0 AS depth UNION ALL SELECT n,depth::bigint FROM rv_bad", "42804", `recursive query "rv_bad" column 2 has type integer in non-recursive term but type bigint overall`, "0 AS depth", "Cast the output of the non-recursive term to the correct type.", "execute"},
		} {
			want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			want.Hint = tc.hint
			if tc.marker != "" {
				want.Position = recursiveViewPosition(t, tc.q, tc.marker)
			}
			recursiveViewAnalysisFailure(t, p, b, c, tc.q, want, tc.pgStage)
		}
		run(fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace AND relname='rv_bad'", []string{"relname"}, []string{"NAME"}, nil))
		run(recursiveViewRows("SELECT n FROM rv_short", "INT4", 1, 1, 1))
	})
}
