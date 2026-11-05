package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAnalyzeProgressReadOnly(t *testing.T) {
	analyzeProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		admin, worker := vacuumConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
		analyzeProgressSetup(t, p, admin, "analyze_readonly")
		id, holder, run := analyzeProgressHold(t, p, b, worker, "analyze_readonly", false)
		first := analyzeProgressObserve(t, p, admin, id, run)
		analyzeProgressVisible(t, first, id, b.PG)
		for _, tc := range []struct{ q, verb, action, gerund string }{
			{"INSERT INTO pg_stat_progress_analyze(pid) VALUES(1)", "insert into", "INSERT", "inserting into"},
			{"INSERT INTO pg_stat_progress_analyze(pid) SELECT pid FROM pg_stat_progress_analyze", "insert into", "INSERT", "inserting into"},
			{"UPDATE pg_stat_progress_analyze SET pid=1", "update", "UPDATE", "updating"},
			{"UPDATE pg_stat_progress_analyze SET pid=1 WHERE false", "update", "UPDATE", "updating"},
			{"DELETE FROM pg_stat_progress_analyze", "delete from", "DELETE", "deleting from"},
			{"DELETE FROM pg_stat_progress_analyze WHERE false", "delete from", "DELETE", "deleting from"},
			{"TRUNCATE pg_stat_progress_analyze", "", "", ""},
		} {
			want := fixture.ControlsReferenceDiagnostic("55000", "cannot "+tc.verb+" view \"pg_stat_progress_analyze\"")
			want.Detail = "Views that do not select from a single table or view are not automatically updatable."
			want.Hint = "To enable " + tc.gerund + " the view, provide an INSTEAD OF " + tc.action + " trigger or an unconditional ON " + tc.action + " DO INSTEAD rule."
			if tc.action == "" {
				want = fixture.ControlsReferenceDiagnostic("42809", "\"pg_stat_progress_analyze\" is not a table")
			}
			for _, qualified := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					q := tc.q
					if qualified {
						q = strings.ReplaceAll(q, "pg_stat_progress_analyze", "pg_catalog.pg_stat_progress_analyze")
					}
					s := fixture.ImplicitError(q, want)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared && tc.action != "" {
						s.ErrorStage = "prepare"
					}
					vacuumRun(t, p, b, admin, s)
					next := analyzeProgressObserve(t, p, admin, id, run)
					analyzeProgressVisible(t, next, id, b.PG)
					if next != first {
						t.Fatalf("mutation changed live worker: before=%+v after=%+v", first, next)
					}
					vacuumRun(t, p, b, admin, fixture.Select("SELECT count(*),min(a),max(a) FROM ONLY analyze_readonly", []string{"count", "min", "max"}, []string{"INT8", "INT4", "INT4"}, [][]any{{int64(129), int64(1), int64(129)}}))
				}
			}
		}
		copyProgressExec(t, p.Ctx, holder, "ROLLBACK")
		if err := run.result(t); err != nil {
			t.Fatal(err)
		}
		analyzeProgressIdle(t, p, admin, id.pid)
	})
}
