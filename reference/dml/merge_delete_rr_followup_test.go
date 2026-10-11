package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// The prerequisite's actual old10/own100 bag is established before deletion.
// Pinned DELETE retires own100 while leaving the unrelated snapshot10 visible.
func TestReferenceMergeDeleteRepeatableReadFollowup(t *testing.T) {
	for _, tc := range []struct {
		name, condition    string
		n                  int64
		duplicate, deleted bool
	}{
		{"unconditional", "", 0, true, false},
		{"original10Only", " AND t.v=10", 0, false, false},
		{"own100Only", " AND t.v=100", 1, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
				defer cancel()
				holder, worker := fixture.LockConnections(t, ctx, b.DB)
				run := fixture.MergeRun(t, p, b, false)
				run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
				var hp, wp int
				if b.PG {
					hp, wp = fixture.LockPID(t, ctx, holder), fixture.LockPID(t, ctx, worker)
				}
				fixture.LockExec(t, ctx, worker, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
				fixture.RunStep(t, ctx, worker, b.Recorder, fixture.Select("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
				fixture.LockExec(t, ctx, worker, "UPDATE t SET v=21 WHERE id=2", 1)
				fixture.LockExec(t, ctx, holder, "BEGIN", 0)
				fixture.LockExec(t, ctx, holder, "UPDATE t SET v=11 WHERE id=1", 1)
				initial := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=100"
				var held *sql.Stmt
				if prepared {
					var err error
					held, err = worker.PrepareContext(ctx, initial)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
				}
				var await func() fixture.RelationOutcome
				if b.PG {
					await = fixture.LockOperation(t, ctx, func(c context.Context) fixture.RelationOutcome { return mergeUpdateOperation(c, worker, held, initial) })
					fixture.RowWait(t, ctx, wp, hp)
				} else {
					mergeUpdateNativeTimeout(t, ctx, worker, held, initial, true)
					await = func() fixture.RelationOutcome { return mergeUpdateOperation(ctx, worker, held, initial) }
				}
				fixture.LockExec(t, ctx, holder, "COMMIT", 0)
				out := await()
				mergeInsertAffected(t, b, out.Result, out.Err, 1)
				original := fixture.Select("SELECT id,v FROM t ORDER BY id,v", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(100)}, {int64(2), int64(21)}})
				fixture.RunStep(t, ctx, worker, b.Recorder, original)
				fixture.LockExec(t, ctx, worker, "SAVEPOINT deletion", 0)
				q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED" + tc.condition + " THEN DELETE"
				step := fixture.Exec(q, tc.n)
				if tc.duplicate {
					step = fixture.ImplicitError(q, mergeUpdateCardinalityDiagnostic())
					step.ErrorStage = "execute"
				}
				step.Prepared = prepared
				if b.PG {
					step.Affected = 0
				}
				fixture.InsertSelectRun(t, ctx, worker, b.Recorder, step)
				if tc.duplicate {
					_, err := worker.ExecContext(ctx, "SELECT * FROM t")
					d := fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
					fixture.AssertDiagnostic(t, err, &d)
					fixture.LockExec(t, ctx, worker, "ROLLBACK TO deletion", 0)
				}
				if tc.deleted {
					fixture.RunStep(t, ctx, worker, b.Recorder, fixture.Select("SELECT id,v FROM t ORDER BY id,v", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(21)}}))
				} else {
					fixture.RunStep(t, ctx, worker, b.Recorder, original)
				}
				fixture.LockExec(t, ctx, worker, "ROLLBACK", 0)
				run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(2), int64(20)}}))
			})
		})
	}
}
