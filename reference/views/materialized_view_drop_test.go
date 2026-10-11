package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMaterializedViewDrop(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			_, run := fixture.QueryTableCreateSession(t, p, b)
			materializedViewSeed(run)
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_drop AS SELECT i,s FROM mv_base", 3))
			sourceRestrict := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop table mv_base because other objects depend on it")
			sourceRestrict.Detail = "materialized view mv_drop depends on table mv_base"
			sourceRestrict.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			run(fixture.ErrorDiagnostic("DROP TABLE mv_base", sourceRestrict))
			run(fixture.Exec("CREATE VIEW mv_dependent AS SELECT i,s FROM mv_drop", 0))
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_dependent_stored AS SELECT i,s FROM mv_dependent", 3))
			for _, tc := range []struct{ q, message, hint string }{
				{"DROP MATERIALIZED VIEW mv_base", `"mv_base" is not a materialized view`, "Use DROP TABLE to remove a table."},
				{"DROP MATERIALIZED VIEW mv_dependent", `"mv_dependent" is not a materialized view`, "Use DROP VIEW to remove a view."},
				{"DROP TABLE mv_drop", `"mv_drop" is not a table`, "Use DROP MATERIALIZED VIEW to remove a materialized view."},
				{"DROP VIEW mv_drop", `"mv_drop" is not a view`, "Use DROP MATERIALIZED VIEW to remove a materialized view."},
				{"DROP MATERIALIZED VIEW mv_dependent_stored,mv_base", `"mv_base" is not a materialized view`, "Use DROP TABLE to remove a table."},
			} {
				want := fixture.ControlsReferenceDiagnostic("42809", tc.message)
				want.Hint = tc.hint
				run(fixture.ErrorDiagnostic(tc.q, want))
				run(materializedViewBag("mv_drop"))
				run(materializedViewBag("mv_dependent_stored"))
			}
			run(fixture.ImplicitError("DROP MATERIALIZED VIEW mv_dependent_stored,mv_missing", fixture.Diagnostic{Code: "42P01", Message: `materialized view "mv_missing" does not exist`}))
			run(materializedViewBag("mv_dependent_stored"))
			for _, tc := range []struct{ q, message, detail string }{
				{"DROP MATERIALIZED VIEW mv_drop", "cannot drop materialized view mv_drop because other objects depend on it", "view mv_dependent depends on materialized view mv_drop\nmaterialized view mv_dependent_stored depends on view mv_dependent"},
			} {
				want := fixture.ControlsReferenceDiagnostic("2BP01", tc.message)
				want.Detail, want.Hint = tc.detail, "Use DROP ... CASCADE to drop the dependent objects too."
				run(fixture.ErrorDiagnostic(tc.q, want))
			}
			run(fixture.Exec("DROP MATERIALIZED VIEW mv_drop CASCADE", 0))
			for _, name := range []string{"mv_drop", "mv_dependent", "mv_dependent_stored"} {
				fixture.QueryTableCreateAbsent(t, run, b.PG, name)
			}
			run(materializedViewBag("mv_base"))
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("DROP MATERIALIZED VIEW IF EXISTS mv_missing", 0))
			want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `materialized view "mv_missing" does not exist, skipping`}
			if len(notices) != 1 || notices[0] != want {
				t.Fatalf("notices=%#v want=%#v", notices, want)
			}
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_one AS SELECT i FROM mv_base", 3))
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_two AS SELECT i FROM mv_base", 3))
			run(fixture.Prepared(fixture.Exec("DROP MATERIALIZED VIEW mv_one,mv_two RESTRICT", 0)))
			fixture.QueryTableCreateAbsent(t, run, b.PG, "mv_one")
			fixture.QueryTableCreateAbsent(t, run, b.PG, "mv_two")
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_cascade AS SELECT i FROM mv_base", 3))
			run(fixture.Exec("DROP TABLE mv_base CASCADE", 0))
			fixture.QueryTableCreateAbsent(t, run, b.PG, "mv_cascade")
		})
	}
}
