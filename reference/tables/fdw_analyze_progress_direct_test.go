package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func fdwAnalyzeProgressDirectRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, prepared bool, columns string) {
	t.Helper()
	id := fdwAnalyzeProgressIdentify(t, p, c, "fdw_direct")
	query := "ANALYZE fdw_direct" + columns
	if b.PG {
		step := fixture.Exec(query, 0)
		step.Prepared = prepared
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
	} else {
		gate := fdwAnalyzeProgressHold(t, p, b, c, r, query, "AcquireSampleRows", prepared, nil)
		// The request is genuinely admitted but no sample has returned. Pages,
		// population and eventual fetched definitions are not completed work.
		fdwAnalyzeProgressObserve(t, p, b, id, "acquiring sample rows", 0, 0, 0, 0, 0, 0, 0, gate.finished)
		gate.release()
		fdwAnalyzeProgressSuccess(t, gate.join())
		fdwAnalyzeProgressResources(t, r)
	}
	fdwAnalyzeProgressAbsent(t, p, b, id)
}

func TestReferenceFDWAnalyzeProgressDirect(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fdwRowLockModeName(prepared), func(t *testing.T) {
			fdwAnalyzeProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
				fdwAnalyzeDirectSetup(t, p, b, r, run)
				id := fdwAnalyzeProgressIdentify(t, p, c, "fdw_direct")
				statement, err := c.PrepareContext(p.Ctx, "ANALYZE fdw_direct")
				if err != nil {
					t.Fatal(err)
				}
				statement.Close()
				fdwAnalyzeProgressAbsent(t, p, b, id)
				fdwAnalyzeRelationPopulation(run, "fdw_direct", -1)
				run(fixture.Select("SELECT count(*)"+fdwAnalyzeDirectData, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				// This helper creates joint AND expression definitions: fetched2,
				// successful whole definitions2, never one per requested kind.
				run(fixture.Select("SELECT count(*) FROM pg_statistic_ext WHERE stxrelid='fdw_direct'::regclass", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
				fdwAnalyzeProgressDirectRun(t, p, b, c, r, prepared, "")
				fdwAnalyzeRelationPopulation(run, "fdw_direct", 1000)
				fdwAnalyzeProgressDirectProducts(run)
				fdwAnalyzeDirectEstimate(t, p, c, 300)
				joint := fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_joint")
				savedConn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { savedConn.Close() })
				held := fdwHeld(t, p, b, savedConn, "SELECT stxdndistinct::text"+fdwAnalyzeDirectData)
				fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.direct SET b=b+10 WHERE tag='rare'", 300)
				fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.direct SET a=9,tag='rare'", 1000)
				// A fetched but explicitly disabled third definition cannot be
				// silently subtracted from the progress inventory before checking.
				run(fixture.Exec("CREATE STATISTICS fdw_progress_disabled ON (b+1) FROM fdw_direct", 0))
				run(fixture.Exec("ALTER STATISTICS fdw_progress_disabled SET STATISTICS 0", 0))
				run(fixture.Select("SELECT count(*) FROM pg_statistic_ext WHERE stxrelid='fdw_direct'::regclass", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
				var notices []fixture.Diagnostic
				_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				analyze := func(columns string, missing bool) {
					t.Helper()
					notices = nil
					step := fixture.Exec("ANALYZE fdw_direct"+columns, 0)
					step.Prepared = prepared
					fixture.RunStep(t, p.Ctx, noticeConn, recorder, step)
					if missing {
						if len(notices) != 1 || notices[0] != fdwAnalyzeDirectMissingKey() {
							t.Fatal("fetched missing-key warning", notices)
						}
					} else if len(notices) != 0 {
						t.Fatal("unexpected extended warning", notices)
					}
					fdwAnalyzeProgressAbsent(t, p, b, fdwAnalyzeProgressIdentify(t, p, noticeConn, "fdw_direct"))
				}
				retained := func() {
					t.Helper()
					fdwAnalyzeDirectJoint(run, 3)
					if fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_joint") != joint {
						t.Fatal("ineligible definition replaced whole typed payload")
					}
				}
				analyze("(tag)", true)
				retained()
				fdwAnalyzeDirectExpression(run, 1, "{10}")
				fdwAnalyzeDirectEstimate(t, p, c, 1000)
				run(fixture.Exec("ALTER FOREIGN TABLE fdw_direct ALTER a SET STATISTICS 0", 0))
				for _, target := range []string{"100", "0"} {
					run(fixture.Exec("ALTER STATISTICS fdw_direct_joint SET STATISTICS "+target, 0))
					analyze("", true) // missing key precedes even object target0.
					retained()
				}
				run(fixture.Exec("ALTER FOREIGN TABLE fdw_direct ALTER a SET STATISTICS 100", 0))
				analyze("", false)
				retained()
				run(fixture.Exec("ALTER STATISTICS fdw_direct_joint SET STATISTICS 100", 0))
				fdwAnalyzeProgressDirectRun(t, p, b, c, r, prepared, "")
				fdwAnalyzeDirectJoint(run, 6)
				joint = fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_joint")
				expression := fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_expression")
				for _, column := range []string{"a", "b", "tag"} {
					run(fixture.Exec("ALTER FOREIGN TABLE fdw_direct ALTER "+column+" SET STATISTICS 0", 0))
				}
				fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.direct SET a=19,b=b+100", 1000)
				analyze("", false)
				if fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_joint") != joint || fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_expression") != expression {
					t.Fatal("all-disabled scalar set published extended completion")
				}
				fdwSourceExec(t, p, b, r, run, "DELETE FROM fdw_source.direct", 1000)
				analyze("", false)
				fdwAnalyzeRelationPopulation(run, "fdw_direct", 0)
				fdwAnalyzeDirectJoint(run, 6)
				if fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_expression") != expression {
					t.Fatal("empty input replaced retained expression")
				}
				fixture.RunStep(t, p.Ctx, held, held.recorder, fixture.Select(held.query, []string{"stxdndistinct"}, []string{"TEXT"}, [][]any{{`{"1, 2": 3}`}}))
				fdwAnalyzeProgressAbsent(t, p, b, id)
			})
		})
	}
}
