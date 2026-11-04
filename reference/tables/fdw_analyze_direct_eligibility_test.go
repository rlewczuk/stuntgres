package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func fdwAnalyzeDirectPayload(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) [4]string {
	t.Helper()
	var result [4]string
	err := c.QueryRowContext(p.Ctx, "SELECT COALESCE(stxdndistinct::text,''),COALESCE(stxddependencies::text,''),COALESCE(stxdmcv::text,''),COALESCE(stxdexpr::text,'') FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname=$1 AND NOT d.stxdinherit", name).Scan(&result[0], &result[1], &result[2], &result[3])
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func fdwAnalyzeDirectMissingKey() fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42P17", Message: `statistics object "public.fdw_direct_joint" could not be computed for relation "public.fdw_direct"`, Schema: "public", Table: "fdw_direct"}
}

func TestReferenceFDWAnalyzeDirectEligibility(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fdwRowLockModeName(prepared), func(t *testing.T) {
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
				fdwAnalyzeDirectSetup(t, p, b, r, run)
				fdwAnalyzeDirectCommand(run, prepared, "")
				fdwAnalyzeDirectJoint(run, 3)
				joint := fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_joint")
				fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.direct SET b=b+10 WHERE tag='rare'", 300)
				fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.direct SET a=9,tag='rare'", 1000)
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
							t.Fatal("direct missing-key warning", notices)
						}
					} else if len(notices) != 0 {
						t.Fatal("unexpected direct extended warning", notices)
					}
				}
				retained := func() {
					t.Helper()
					fdwAnalyzeDirectJoint(run, 3)
					if got := fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_joint"); got != joint {
						t.Fatal("ineligible direct definition replaced complete joint payload", got, joint)
					}
				}
				// Selected tag admits expression input from unselected a. The simple
				// a,b definition is missing its keys, emits once, and retains joint3
				// although the actual data now has six groups and expression10.
				analyze("(tag)", true)
				retained()
				fdwAnalyzeDirectExpression(run, 1, "{10}")
				fdwAnalyzeDirectEstimate(t, p, c, 1000)
				run(fixture.Exec("ALTER FOREIGN TABLE fdw_direct ALTER a SET STATISTICS 0", 0))
				for _, target := range []string{"100", "0"} {
					run(fixture.Exec("ALTER STATISTICS fdw_direct_joint SET STATISTICS "+target, 0))
					// Required-key admission/warning precedes even object target0.
					analyze("", true)
					retained()
				}
				run(fixture.Exec("ALTER FOREIGN TABLE fdw_direct ALTER a SET STATISTICS 100", 0))
				analyze("", false) // Object target0 alone is silent and retained.
				retained()
				run(fixture.Exec("ALTER STATISTICS fdw_direct_joint SET STATISTICS 100", 0))
				analyze("", false)
				fdwAnalyzeDirectJoint(run, 6)
				joint = fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_joint")
				expression := fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_expression")
				for _, column := range []string{"a", "b", "tag"} {
					run(fixture.Exec("ALTER FOREIGN TABLE fdw_direct ALTER "+column+" SET STATISTICS 0", 0))
				}
				fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.direct SET a=19,b=b+100", 1000)
				analyze("", false)
				fdwAnalyzeDirectJoint(run, 6)
				fdwAnalyzeDirectExpression(run, 1, "{10}")
				if fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_joint") != joint || fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_expression") != expression {
					t.Fatal("empty scalar admission changed retained direct extended payload")
				}
				run(fixture.Select("SELECT min(a),max(a),count(*) FROM fdw_direct", []string{"min", "max", "count"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(19), int64(19), int64(1000)}}))
				run(fixture.Exec("ALTER FOREIGN TABLE fdw_direct ALTER tag SET STATISTICS 100", 0))
				analyze("(tag)", true)
				fdwAnalyzeDirectJoint(run, 6)
				fdwAnalyzeDirectExpression(run, 1, "{20}")
				// Empty accepted input neither rebuilds nor warns about missing keys.
				fdwSourceExec(t, p, b, r, run, "DELETE FROM fdw_source.direct", 1000)
				analyze("(tag)", false)
				fdwAnalyzeRelationPopulation(run, "fdw_direct", 0)
				fdwAnalyzeDirectJoint(run, 6)
				fdwAnalyzeDirectExpression(run, 1, "{20}")
			})
		})
	}
}
