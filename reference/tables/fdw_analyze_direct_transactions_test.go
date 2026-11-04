package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWAnalyzeDirectTransactions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fdwRowLockModeName(prepared), func(t *testing.T) {
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
				fdwAnalyzeDirectSetup(t, p, b, r, run)
				fdwAnalyzeDirectCommand(run, prepared, "")
				fdwAnalyzeDirectJoint(run, 3)
				joint := fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_joint")
				expression := fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_expression")
				fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.direct SET a=9,b=9,tag='rare'", 1000)
				fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.direct VALUES"+fdwAnalyzeDirectConstantValues(1000, 9), 1000)
				unchanged := func(population float64) {
					t.Helper()
					fdwAnalyzeRelationPopulation(run, "fdw_direct", population)
					fdwAnalyzeDirectJoint(run, 3)
					fdwAnalyzeDirectExpression(run, 3, "")
					if fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_joint") != joint || fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_expression") != expression {
						t.Fatal("failed/rolled-back direct collection changed old typed payload")
					}
				}
				// Utilities bind target columns during execution, not Prepare. Neither
				// error may publish the distinguishable new2000-row population.
				for _, tc := range []struct{ columns, code, message string }{
					{"(missing)", "42703", `column "missing" of relation "fdw_direct" does not exist`},
					{"(a,a)", "42701", `column "a" of relation "fdw_direct" appears more than once`},
				} {
					step := fixture.ErrorDiagnostic("ANALYZE fdw_direct"+tc.columns, fdwDiagnostic(tc.code, tc.message))
					step.Prepared, step.ErrorStage = prepared, "execute"
					run(step)
					unchanged(1000)
				}
				// Evaluation is real deferred work over the acquired sample. A new
				// definition that divides by zero must not complete scalar maintenance.
				run(fixture.Exec("CREATE STATISTICS fdw_direct_failure ON (100/(a-9)) FROM fdw_direct", 0))
				failure := fixture.ErrorDiagnostic("ANALYZE fdw_direct", fdwDiagnostic("22012", "division by zero"))
				failure.Prepared, failure.ErrorStage = prepared, "execute"
				run(failure)
				unchanged(1000)
				run(fixture.Select("SELECT count(*) FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='fdw_direct_failure'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SAVEPOINT before_failed_analyze", 0))
				run(failure)
				aborted := fixture.ErrorDiagnostic("ANALYZE fdw_direct", fdwDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
				aborted.ErrorStage = "execute"
				run(aborted)
				run(fixture.Exec("ROLLBACK TO before_failed_analyze", 0))
				unchanged(1000)
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Exec("DROP STATISTICS fdw_direct_failure", 0))
				if !b.PG && !prepared {
					// Registered callback gates are explicitly native-only. They still
					// own real SQL source resources and must cancel/join without publish.
					fdwGatedInformation(t, p, b, c, r, "AcquireSampleRows", "cancel", nil, "ANALYZE fdw_direct")
					unchanged(1000)
					fdwGatedInformation(t, p, b, c, r, "AcquireSampleRows", "provider", nil, "ANALYZE fdw_direct")
					unchanged(1000)
				}
				run(fixture.Exec("BEGIN", 0))
				fdwAnalyzeDirectCommand(run, prepared, "")
				fdwAnalyzeDirectJoint(run, 1)
				fdwAnalyzeDirectExpression(run, 1, "{10}")
				// A genuine second SQL session still sees the committed joint3.
				peer := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step) }
				fdwAnalyzeDirectJoint(peer, 3)
				fdwAnalyzeRelationPopulation(peer, "fdw_direct", 2000)
				run(fixture.Exec("ROLLBACK", 0))
				unchanged(2000)                        // Existing-object scan population is nontransactional.
				fdwAnalyzeDirectEstimate(t, p, c, 600) // Old .3 mass, new global2000.
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SAVEPOINT before_direct_analyze", 0))
				fdwAnalyzeDirectCommand(run, prepared, "")
				fdwAnalyzeDirectJoint(run, 1)
				run(fixture.Exec("ROLLBACK TO before_direct_analyze", 0))
				unchanged(2000)
				fdwAnalyzeDirectCommand(run, prepared, "")
				run(fixture.Exec("COMMIT", 0))
				fdwAnalyzeDirectJoint(run, 1)
				fdwAnalyzeDirectExpression(run, 1, "{10}")
				fdwAnalyzeDirectEstimate(t, p, c, 2000)
				run(fixture.Select("SELECT a,b,tag,count(*) FROM fdw_direct GROUP BY a,b,tag", []string{"a", "b", "tag", "count"}, []string{"INT4", "INT4", "TEXT", "INT8"}, [][]any{{int64(9), int64(9), "rare", int64(2000)}}))
				// Creation-owned definitions/facts must vanish with the real private
				// foreign target; no committed canonical metadata may become a ghost.
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("CREATE FOREIGN TABLE fdw_direct_private(a integer,b integer,tag text) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'direct')", 0))
				run(fixture.Exec("CREATE STATISTICS fdw_direct_private_joint(ndistinct) ON a,b FROM fdw_direct_private", 0))
				private := fixture.Exec("ANALYZE fdw_direct_private", 0)
				private.Prepared = prepared
				run(private)
				run(fixture.Select("SELECT stxdinherit,stxdndistinct::text AS distincts FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='fdw_direct_private_joint'", []string{"stxdinherit", "distincts"}, []string{"BOOL", "TEXT"}, [][]any{{false, `{"1, 2": 1}`}}))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='fdw_direct_private'", []string{"relname"}, []string{"NAME"}, nil))
				run(fixture.Select("SELECT stxname FROM pg_statistic_ext WHERE stxname='fdw_direct_private_joint'", []string{"stxname"}, []string{"NAME"}, nil))
			})
		})
	}
}

func fdwAnalyzeDirectConstantValues(rows, value int) string {
	values := make([]string, rows)
	for i := range values {
		values[i] = fmt.Sprintf("(%d,%d,'rare')", value, value)
	}
	return strings.Join(values, ",")
}
