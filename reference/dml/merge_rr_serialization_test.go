package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// A pinned snapshot cannot acquire a peer's committed replacement as an own
// MERGE predecessor. The SQL key is not tuple identity: delete/reinsert still
// conflicts with the deleted original, not the new row with the same key.
func TestReferenceMergeRepeatableReadSerialization(t *testing.T) {
	for _, action := range []string{"matchedUpdate", "matchedDelete", "sourceUpdate"} {
		for _, change := range []string{"update", "delete", "deleteInsert"} {
			for _, waiting := range []bool{false, true} {
				name := action + "/" + change + "/" + map[bool]string{false: "committedBefore", true: "waiting"}[waiting]
				t.Run(name, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
							p := fixture.NewScenario(t)
							for _, b := range p.Backends() {
								t.Run(b.Name, func(t *testing.T) {
									holder, worker := fixture.LockConnections(t, p.Ctx, b.DB)
									run := fixture.MergeRun(t, p, b, false)
									run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
									run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20),(3,30)", 3))
									fixture.LockExec(t, p.Ctx, worker, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
									fixture.RunStep(t, p.Ctx, worker, b.Recorder, fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}}))
									fixture.LockExec(t, p.Ctx, worker, "UPDATE t SET v=21 WHERE id=2", 1)
									fixture.LockExec(t, p.Ctx, worker, "DELETE FROM t WHERE id=3", 1)
									fixture.LockExec(t, p.Ctx, worker, "INSERT INTO t VALUES(4,40)", 1)
									fixture.LockExec(t, p.Ctx, worker, "SAVEPOINT before_merge", 0)
									q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=100"
									if action == "matchedDelete" {
										q = "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN DELETE"
									} else if action == "sourceUpdate" {
										q = "MERGE INTO t USING (VALUES(9)) s(id) ON t.id=s.id WHEN NOT MATCHED BY SOURCE AND t.id=1 THEN UPDATE SET v=100"
									}
									var held *sql.Stmt
									if prepared {
										var err error
										held, err = worker.PrepareContext(p.Ctx, q)
										if err != nil {
											t.Fatal(err)
										}
										defer held.Close()
									}
									fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
									hold := "UPDATE t SET v=11 WHERE id=1"
									if change != "update" {
										hold = "DELETE FROM t WHERE id=1"
									}
									fixture.LockExec(t, p.Ctx, holder, hold, 1)
									if change == "deleteInsert" {
										fixture.LockExec(t, p.Ctx, holder, "INSERT INTO t VALUES(1,12)", 1)
									}
									await := func() fixture.RelationOutcome { return mergeUpdateOperation(p.Ctx, worker, held, q) }
									if waiting {
										if b.PG {
											hp, wp := fixture.LockPID(t, p.Ctx, holder), fixture.LockPID(t, p.Ctx, worker)
											await = fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome { return mergeUpdateOperation(ctx, worker, held, q) })
											fixture.RowWait(t, p.Ctx, wp, hp)
										} else {
											mergeUpdateNativeTimeout(t, p.Ctx, worker, held, q, true)
										}
									}
									fixture.LockExec(t, p.Ctx, holder, "COMMIT", 0)
									out := await()
									if out.Result != nil {
										t.Fatal("serialization exposed result", out.Result)
									}
									reason := "update"
									if change != "update" {
										reason = "delete"
									}
									d := fixture.ControlsReferenceDiagnostic("40001", "could not serialize access due to concurrent "+reason)
									fixture.AssertDiagnostic(t, out.Err, &d)
									_, err := worker.ExecContext(p.Ctx, "UPDATE t SET v=0 WHERE id=2")
									aborted := fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
									fixture.AssertDiagnostic(t, err, &aborted)
									fixture.LockExec(t, p.Ctx, worker, "ROLLBACK TO before_merge", 0)
									fixture.RunStep(t, p.Ctx, worker, b.Recorder, fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(21)}, {int64(4), int64(40)}}))
									fixture.LockExec(t, p.Ctx, worker, "RELEASE before_merge", 0)
									fixture.LockExec(t, p.Ctx, worker, "UPDATE t SET v=v+1 WHERE id=2", 1)
									fixture.LockExec(t, p.Ctx, worker, "ROLLBACK", 0)
									want := [][]any{{int64(2), int64(20)}, {int64(3), int64(30)}}
									if change == "update" {
										want = append([][]any{{int64(1), int64(11)}}, want...)
									} else if change == "deleteInsert" {
										want = append([][]any{{int64(1), int64(12)}}, want...)
									}
									run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
								})
							}
						})
					}
				})
			}
		}
	}
}
