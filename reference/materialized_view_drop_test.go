package reference_test

import "testing"

func TestReferenceMaterializedViewDrop(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			_, run := queryTableCreateSession(t, p, b)
			materializedViewSeed(run)
			run(referenceExec("CREATE MATERIALIZED VIEW mv_drop AS SELECT i,s FROM mv_base", 3))
			sourceRestrict := controlsReferenceDiagnostic("2BP01", "cannot drop table mv_base because other objects depend on it")
			sourceRestrict.Detail = "materialized view mv_drop depends on table mv_base"
			sourceRestrict.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			run(referenceErrorDiagnostic("DROP TABLE mv_base", sourceRestrict))
			run(referenceExec("CREATE VIEW mv_dependent AS SELECT i,s FROM mv_drop", 0))
			run(referenceExec("CREATE MATERIALIZED VIEW mv_dependent_stored AS SELECT i,s FROM mv_dependent", 3))
			for _, tc := range []struct{ q, message, hint string }{
				{"DROP MATERIALIZED VIEW mv_base", `"mv_base" is not a materialized view`, "Use DROP TABLE to remove a table."},
				{"DROP MATERIALIZED VIEW mv_dependent", `"mv_dependent" is not a materialized view`, "Use DROP VIEW to remove a view."},
				{"DROP TABLE mv_drop", `"mv_drop" is not a table`, "Use DROP MATERIALIZED VIEW to remove a materialized view."},
				{"DROP VIEW mv_drop", `"mv_drop" is not a view`, "Use DROP MATERIALIZED VIEW to remove a materialized view."},
				{"DROP MATERIALIZED VIEW mv_dependent_stored,mv_base", `"mv_base" is not a materialized view`, "Use DROP TABLE to remove a table."},
			} {
				want := controlsReferenceDiagnostic("42809", tc.message)
				want.Hint = tc.hint
				run(referenceErrorDiagnostic(tc.q, want))
				run(materializedViewBag("mv_drop"))
				run(materializedViewBag("mv_dependent_stored"))
			}
			run(referenceImplicitError("DROP MATERIALIZED VIEW mv_dependent_stored,mv_missing", referenceDiagnostic{Code: "42P01", Message: `materialized view "mv_missing" does not exist`}))
			run(materializedViewBag("mv_dependent_stored"))
			for _, tc := range []struct{ q, message, detail string }{
				{"DROP MATERIALIZED VIEW mv_drop", "cannot drop materialized view mv_drop because other objects depend on it", "view mv_dependent depends on materialized view mv_drop\nmaterialized view mv_dependent_stored depends on view mv_dependent"},
			} {
				want := controlsReferenceDiagnostic("2BP01", tc.message)
				want.Detail, want.Hint = tc.detail, "Use DROP ... CASCADE to drop the dependent objects too."
				run(referenceErrorDiagnostic(tc.q, want))
			}
			run(referenceExec("DROP MATERIALIZED VIEW mv_drop CASCADE", 0))
			for _, name := range []string{"mv_drop", "mv_dependent", "mv_dependent_stored"} {
				queryTableCreateAbsent(t, run, b.pg, name)
			}
			run(materializedViewBag("mv_base"))
			var notices []referenceDiagnostic
			_, conn, recorder := noticeReferencePool(t, p, b.pg, &notices)
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("DROP MATERIALIZED VIEW IF EXISTS mv_missing", 0))
			want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `materialized view "mv_missing" does not exist, skipping`}
			if len(notices) != 1 || notices[0] != want {
				t.Fatalf("notices=%#v want=%#v", notices, want)
			}
			run(referenceExec("CREATE MATERIALIZED VIEW mv_one AS SELECT i FROM mv_base", 3))
			run(referenceExec("CREATE MATERIALIZED VIEW mv_two AS SELECT i FROM mv_base", 3))
			run(referencePrepared(referenceExec("DROP MATERIALIZED VIEW mv_one,mv_two RESTRICT", 0)))
			queryTableCreateAbsent(t, run, b.pg, "mv_one")
			queryTableCreateAbsent(t, run, b.pg, "mv_two")
			run(referenceExec("CREATE MATERIALIZED VIEW mv_cascade AS SELECT i FROM mv_base", 3))
			run(referenceExec("DROP TABLE mv_base CASCADE", 0))
			queryTableCreateAbsent(t, run, b.pg, "mv_cascade")
		})
	}
}
