package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAnalyzeProgressAdmission(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"anprog_denied"})
			analyzeProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				admin, holder := vacuumConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
				var notices []fixture.Diagnostic
				_, worker, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				for _, q := range []string{
					"CREATE ROLE anprog_denied",
					"GRANT USAGE ON SCHEMA public TO anprog_denied",
					"CREATE TABLE analyze_admission(a INT)",
					"INSERT INTO analyze_admission VALUES(1),(2)",
					"CREATE VIEW analyze_admission_view AS SELECT a FROM analyze_admission",
				} {
					copyProgressExec(t, p.Ctx, admin, q)
				}
				id := copyProgressIdentify(t, p, worker, "analyze_admission")
				h := vacuumHeld(t, p, worker, "ANALYZE ONLY analyze_admission")
				analyzeProgressIdle(t, p, admin, id.pid) // Prepare never starts maintenance.
				copyProgressExec(t, p.Ctx, holder, "BEGIN")
				copyProgressExec(t, p.Ctx, holder, "LOCK TABLE ONLY analyze_admission IN SHARE MODE")
				run := analyzeProgressStart(t, p, worker, h.SQL, prepared)
				fixture.RelationLockPending(t, p, b, int(id.pid), fixture.LockPID(t, p.Ctx, holder), id.relid, "ShareUpdateExclusiveLock", run.finished)
				run.live(t)
				analyzeProgressIdle(t, p, admin, id.pid)
				copyProgressExec(t, p.Ctx, holder, "ROLLBACK")
				if err := run.result(t); err != nil {
					t.Fatal(err)
				}
				analyzeProgressIdle(t, p, admin, id.pid)
				copyProgressExec(t, p.Ctx, holder, "BEGIN")
				copyProgressExec(t, p.Ctx, holder, "LOCK TABLE ONLY analyze_admission IN SHARE MODE")
				s := fixture.Exec("ANALYZE (SKIP_LOCKED) ONLY analyze_admission", 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, worker, rec, s)
				vacuumWarning(t, notices, "55P03", "skipping analyze of \"analyze_admission\" --- lock not available")
				analyzeProgressIdle(t, p, admin, id.pid)
				copyProgressExec(t, p.Ctx, holder, "ROLLBACK")
				for _, tc := range []struct{ q, warning string }{
					{"ANALYZE analyze_admission_view", "skipping \"analyze_admission_view\" --- cannot analyze non-tables or special system tables"},
					{"ANALYZE ONLY analyze_admission", "permission denied to analyze \"analyze_admission\", skipping it"},
				} {
					if tc.q == "ANALYZE ONLY analyze_admission" {
						copyProgressExec(t, p.Ctx, worker, "SET ROLE anprog_denied")
					}
					notices = nil
					s := fixture.Exec(tc.q, 0)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, worker, rec, s)
					vacuumWarning(t, notices, "01000", tc.warning)
					analyzeProgressIdle(t, p, admin, id.pid)
				}
				copyProgressExec(t, p.Ctx, worker, "RESET ROLE")
				copyProgressExec(t, p.Ctx, worker, "BEGIN")
				s = fixture.Exec(h.SQL, 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, worker, rec, s)
				analyzeProgressIdle(t, p, admin, id.pid) // Command end, not transaction end.
				copyProgressExec(t, p.Ctx, worker, "ROLLBACK")
				vacuumRun(t, p, b, admin, fixture.Select("SELECT count(*) FROM analyze_admission", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
			})
		})
	}
}
