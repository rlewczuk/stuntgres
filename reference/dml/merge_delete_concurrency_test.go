package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceMergeDeleteConcurrentClasses(t *testing.T) {
	for _, lane := range []string{"matchedLosesOn", "bySourceGainsOn"} {
		for _, commit := range []bool{false, true} {
			t.Run(lane+map[bool]string{false: "Rollback", true: "Commit"}[commit], func(t *testing.T) {
				mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
					ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
					defer cancel()
					holder, worker := fixture.LockConnections(t, ctx, b.DB)
					run := fixture.MergeRun(t, p, b, false)
					run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
					initial, next := 10, 20
					if lane == "bySourceGainsOn" {
						initial, next = 20, 10
					}
					if initial == 10 {
						run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
					} else {
						run(fixture.Exec("INSERT INTO t VALUES(1,20)", 1))
					}
					var hp, wp int
					if b.PG {
						hp, wp = fixture.LockPID(t, ctx, holder), fixture.LockPID(t, ctx, worker)
					}
					q := "MERGE INTO t USING (VALUES(1,10)) s(id,v) ON t.id=s.id AND t.v=s.v WHEN MATCHED THEN DELETE WHEN NOT MATCHED BY SOURCE THEN DELETE WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,70)"
					if lane == "bySourceGainsOn" {
						q = "MERGE INTO t USING (VALUES(1,10)) s(id,v) ON t.id=s.id AND t.v=s.v WHEN MATCHED THEN UPDATE SET v=99 WHEN NOT MATCHED BY SOURCE THEN DELETE WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,70)"
					}
					var held *sql.Stmt
					if prepared {
						var err error
						held, err = worker.PrepareContext(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
					}
					fixture.LockExec(t, ctx, holder, "BEGIN", 0)
					if next == 20 {
						fixture.LockExec(t, ctx, holder, "UPDATE t SET v=20 WHERE id=1", 1)
					} else {
						fixture.LockExec(t, ctx, holder, "UPDATE t SET v=10 WHERE id=1", 1)
					}
					var await func() fixture.RelationOutcome
					if b.PG {
						await = fixture.LockOperation(t, ctx, func(c context.Context) fixture.RelationOutcome { return mergeUpdateOperation(c, worker, held, q) })
						fixture.RowWait(t, ctx, wp, hp)
					} else {
						// A timeout followed by fresh RC execution is recovery, not evidence of
						// the original native candidate's class. Actual-executor units add that.
						mergeUpdateNativeTimeout(t, ctx, worker, held, q, false)
						run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(initial)}}))
						await = func() fixture.RelationOutcome { return mergeUpdateOperation(ctx, worker, held, q) }
					}
					end := "ROLLBACK"
					if commit {
						end = "COMMIT"
					}
					fixture.LockExec(t, ctx, holder, end, 0)
					out := await()
					n := int64(2)
					want := [][]any{{int64(1), int64(70)}}
					if lane == "matchedLosesOn" && !commit {
						n = 1
						want = nil
					}
					if lane == "bySourceGainsOn" && commit && !b.PG {
						n = 1
						want = [][]any{{int64(1), int64(99)}}
					}
					mergeInsertAffected(t, b, out.Result, out.Err, n)
					run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
				})
			})
		}
	}
}
