package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWAnalyzeInheritedCancellation(t *testing.T) {
	for _, kind := range []string{"ordinary", "partition", "foreign"} {
		for _, prepared := range []bool{false, true} {
			t.Run(kind+"/"+fdwRowLockModeName(prepared), func(t *testing.T) {
				fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
					local, _ := fdwAnalyzeMixedSetup(t, p, b, r, run, kind, 10, 30)
					run(fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0))
					before := fdwAnalyzePassState(t, p, b, c, "fdw_analyze_parent", -1)
					locked := local
					if kind == "ordinary" {
						locked = "fdw_analyze_blocked"
						run(fixture.Exec("CREATE TABLE fdw_analyze_blocked() INHERITS(fdw_analyze_parent)", 0))
						// Establish the changed structural authority before the failure.
						run(fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0))
						before = fdwAnalyzePassState(t, p, b, c, "fdw_analyze_parent", before.count+1)
					}
					if kind == "foreign" {
						fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.data VALUES"+fdwAnalyzeValues(20, 3, "new", false), 20)
					} else {
						run(fixture.Exec("INSERT INTO "+local+" VALUES"+fdwAnalyzeValues(20, 1, "new", false), 20))
					}
					holder, _ := fixture.LockConnections(t, p.Ctx, b.DB)
					fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
					defer holder.ExecContext(p.Ctx, "ROLLBACK")
					if kind == "partition" {
						// Real leaf TRUNCATE holds its AccessExclusive lock until
						// rollback. Ancestor AccessShare remains compatible with
						// parent ANALYZE admission; inherited child acquisition waits.
						fixture.LockExec(t, p.Ctx, holder, "TRUNCATE ONLY "+locked, 0)
					} else {
						fixture.LockExec(t, p.Ctx, holder, "LOCK TABLE ONLY "+locked+" IN ACCESS EXCLUSIVE MODE", 0)
					}
					query := "ANALYZE (SKIP_LOCKED) ONLY fdw_analyze_parent"
					statement, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer statement.Close()
					waiterPID, holderPID := 0, 0
					var oid int64
					if b.PG {
						waiterPID = fixture.LockPID(t, p.Ctx, c)
						holderPID = fixture.LockPID(t, p.Ctx, holder)
						oid = fixture.LockOID(t, p.Ctx, locked)
					}
					// SKIP_LOCKED applies to target admission, never to inherited
					// child AccessShare acquisition. Observe a real wait before cancel.
					fixture.BinaryConversionActiveCancelAfter(t, p, b, c, query, "(*relationRequest).wait", func(ctx context.Context) {
						if b.PG {
							fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, oid, "AccessShareLock")
						}
					}, func(ctx context.Context) fixture.RelationOutcome {
						var out fixture.RelationOutcome
						if prepared {
							out.Result, out.Err = statement.ExecContext(ctx)
						} else {
							out.Result, out.Err = c.ExecContext(ctx, query)
						}
						return out
					})
					fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
					increment, population := int64(1), float64(30)
					if kind == "partition" {
						increment, population = 0, 40
					}
					if kind == "foreign" {
						population = 50
					}
					fdwAnalyzeRelationPopulation(run, "fdw_analyze_parent", population)
					fdwAnalyzePassState(t, p, b, c, "fdw_analyze_parent", before.count+increment)
					fdwAnalyzeBaselineFacts(t, p, b, c, run)
					run(fixture.Prepared(fixture.Exec(query, 0)))
					run(fixture.Select("SELECT count(*) FROM fdw_analyze_parent WHERE label='new'", []string{"count"}, []string{"INT8"}, [][]any{{int64(20)}}))
				})
			})
		}
	}
}
