package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRecursiveViewBoundaries(t *testing.T) {
	recursiveViewReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{
				"CREATE RECURSIVE VIEW rv_boundary(n) WITH(security_barrier=true) AS SELECT 1",
				"CREATE OR REPLACE RECURSIVE VIEW rv_boundary(n) AS SELECT 7",
				"CREATE TEMP RECURSIVE VIEW rv_temp(n) AS SELECT 1",
				"CREATE OR REPLACE TEMP RECURSIVE VIEW rv_temp(n) AS SELECT 7",
			} {
				s := fixture.Exec(q, 0)
				s.Prepared = prepared
				run(s)
			}
			run(recursiveViewRows("SELECT n FROM rv_boundary", "INT4", 7))
			run(recursiveViewRows("SELECT n FROM rv_temp", "INT4", 7))
			run(fixture.Exec("DROP VIEW rv_boundary", 0))
			run(fixture.Exec("DROP VIEW rv_temp", 0))
		}
		run(fixture.Exec("CREATE VIEW recursive AS SELECT 7 AS recursive", 0))
		run(fixture.Select("SELECT recursive FROM recursive", []string{"recursive"}, []string{"INT4"}, recursiveViewInts(7)))
		for _, suffix := range []string{"WITH CHECK OPTION", "WITH LOCAL CHECK OPTION", "WITH CASCADED CHECK OPTION"} {
			q := "CREATE RECURSIVE VIEW rv_boundary(n) AS SELECT 1 " + suffix
			want := fixture.ControlsReferenceDiagnostic("0A000", "WITH CHECK OPTION not supported on recursive views")
			want.Position = recursiveViewPosition(t, q, suffix)
			recursiveViewAnalysisFailure(t, p, b, c, q, want, "prepare")
		}
		q := "CREATE RECURSIVE VIEW rv_boundary(n) WITH(check_option=local) AS SELECT 1 UNION ALL SELECT n FROM rv_boundary"
		want := fixture.ControlsReferenceDiagnostic("0A000", "WITH CHECK OPTION is supported only on automatically updatable views")
		want.Hint = "Views containing WITH are not automatically updatable."
		recursiveViewAnalysisFailure(t, p, b, c, q, want, "execute")
		for _, tc := range []struct{ q, code, message, marker, pgStage string }{
			{"CREATE RECURSIVE VIEW rv_boundary(n) AS SELECT 1 INTO rv_into", "42601", "SELECT ... INTO is not allowed here", "rv_into", "execute"},
			{"CREATE RECURSIVE VIEW rv_boundary(n) AS SELECT 1 UNION ALL SELECT n INTO rv_into FROM rv_boundary", "42601", "INTO is only allowed on first SELECT of UNION/INTERSECT/EXCEPT", "rv_into", "execute"},
			{"CREATE RECURSIVE VIEW rv_boundary(n) AS SELECT (SELECT 1 INTO rv_into)", "42601", "SELECT ... INTO is not allowed here", "rv_into", "execute"},
			{"CREATE VIEW rv_boundary AS SELECT 1 INTO rv_into", "0A000", "views must not contain SELECT INTO", "", "execute"},
			{"CREATE RECURSIVE VIEW rv_boundary(n) AS SELECT 1; @", "42601", `syntax error at or near "@"`, "@", "prepare"},
		} {
			for _, prefix := range []string{"", "/*é*/ "} {
				q := prefix + tc.q
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				if tc.marker != "" {
					want.Position = recursiveViewPosition(t, q, tc.marker)
				}
				stage := tc.pgStage
				if !b.PG && tc.code == "42601" {
					stage = "prepare" // Native parser validates INTO before utility execution.
				}
				recursiveViewAnalysisFailure(t, p, b, c, q, want, stage)
			}
		}
		run(fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('rv_boundary','rv_into')", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Exec("SELECT 7 AS n INTO rv_into", 1))
		run(recursiveViewRows("SELECT n FROM rv_into", "INT4", 7))
	})
}
