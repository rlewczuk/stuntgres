package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceMergeNothingConcurrency(t *testing.T) {
	for _, lane := range []string{"initial", "recheck", "lostON"} {
		t.Run(lane, func(t *testing.T) {
			mergeNothingRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
				run(fixture.Exec("CREATE TABLE source(id INT,v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
				run(fixture.Exec("INSERT INTO source VALUES(1,7)", 1))
				ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
				defer cancel()
				holder, worker := fixture.LockConnections(t, ctx, b.DB)
				q := "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED THEN DO NOTHING RETURNING merge_action(),t.id,t.v"
				if lane == "recheck" {
					q = "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED AND t.v=40 AND s.v=7 THEN DO NOTHING WHEN MATCHED THEN UPDATE SET v=t.v+s.v RETURNING merge_action(),t.id,t.v"
				}
				if lane == "lostON" {
					q = "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.v WHEN NOT MATCHED BY SOURCE THEN DO NOTHING WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,s.v) RETURNING merge_action(),t.id,t.v"
				}
				var held *sql.Stmt
				if prepared {
					var e error
					held, e = worker.PrepareContext(ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					defer held.Close()
				}
				fixture.LockExec(t, ctx, holder, "BEGIN", 0)
				defer holder.ExecContext(p.Ctx, "ROLLBACK")
				change := "UPDATE t SET v=40 WHERE id=1"
				if lane == "lostON" {
					change = "UPDATE t SET id=2,v=40 WHERE id=1"
				}
				fixture.LockExec(t, ctx, holder, change, 1)
				hp, wp := 0, 0
				if b.PG {
					hp, wp = fixture.LockPID(t, ctx, holder), fixture.LockPID(t, ctx, worker)
				}
				if lane != "initial" && !b.PG {
					// Native pending/restart causality is proved by actual-executor units;
					// this public lane proves genuine mutation admission waits, not a retry.
					mergeUpdateNativeTimeout(t, ctx, worker, held, q, false)
					fixture.LockExec(t, ctx, holder, "ROLLBACK", 0)
					run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
					run(fixture.Exec("MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED THEN DO NOTHING", 0))
					return
				}
				await := fixture.LockOperation(t, ctx, func(c context.Context) fixture.RelationOutcome {
					var rows *sql.Rows
					var e error
					if held != nil {
						rows, e = held.QueryContext(c)
					} else {
						rows, e = worker.QueryContext(c, q)
					}
					return fixture.RelationOutcome{Rows: rows, Err: e}
				})
				if lane != "initial" {
					fixture.RowWait(t, ctx, wp, hp)
					fixture.LockExec(t, ctx, holder, "UPDATE source SET v=99", 1)
					fixture.LockExec(t, ctx, holder, "COMMIT", 0)
				}
				out := await()
				if out.Err != nil {
					t.Fatal(out.Err)
				}
				defer out.Rows.Close()
				names, e := out.Rows.Columns()
				if e != nil || len(names) != 3 || names[0] != "merge_action" || names[1] != "id" || names[2] != "v" {
					t.Fatal(names, e)
				}
				meta, e := out.Rows.ColumnTypes()
				if e != nil || meta[0].DatabaseTypeName() != "TEXT" || meta[1].DatabaseTypeName() != "INT4" || meta[2].DatabaseTypeName() != "INT4" {
					t.Fatal("output types", meta, e)
				}
				if lane == "lostON" {
					if !out.Rows.Next() {
						t.Fatal("missing retained source insertion", out.Rows.Err())
					}
					var action string
					var id, v int64
					if e = out.Rows.Scan(&action, &id, &v); e != nil || action != "INSERT" || id != 1 || v != 7 {
						t.Fatal(action, id, v, e)
					}
				}
				if out.Rows.Next() || out.Rows.Err() != nil {
					t.Fatal("unexpected no-op output", out.Rows.Err())
				}
				out.Rows.Close()
				if lane == "initial" {
					// Completion occurred while the conflicting holder remained active.
					fixture.RunStep(t, ctx, holder, b.Recorder, fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(40)}}))
					fixture.LockExec(t, ctx, holder, "ROLLBACK", 0)
					run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
				} else if lane == "recheck" {
					run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(40)}}))
				} else {
					run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(40)}}))
				}
			})
		})
	}
}
func TestReferenceMergeNothingCancellation(t *testing.T) {
	for _, setting := range []struct{ name, code, message string }{{"lock_timeout", "55P03", "canceling statement due to lock timeout"}, {"statement_timeout", "57014", "canceling statement due to statement timeout"}} {
		t.Run(setting.name, func(t *testing.T) {
			mergeNothingRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT PRIMARY KEY,v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(3,30)", 1))
				holder, worker := fixture.LockConnections(t, p.Ctx, b.DB)
				var tuple string
				if b.PG {
					if e := holder.QueryRowContext(p.Ctx, "SELECT ctid::text FROM t WHERE id=3").Scan(&tuple); e != nil {
						t.Fatal(e)
					}
				}
				fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
				defer holder.ExecContext(p.Ctx, "ROLLBACK")
				fixture.LockExec(t, p.Ctx, holder, "UPDATE t SET v=99 WHERE id=3", 1)
				fixture.LockExec(t, p.Ctx, worker, "BEGIN", 0)
				defer worker.ExecContext(p.Ctx, "ROLLBACK")
				fixture.LockExec(t, p.Ctx, worker, "SET LOCAL "+setting.name+"='500ms'", 0)
				q := "MERGE INTO t USING (SELECT id FROM (VALUES(1),(2),(3)) x(id) ORDER BY id) s ON t.id=s.id WHEN NOT MATCHED AND s.id=1 THEN DO NOTHING WHEN NOT MATCHED THEN INSERT VALUES(s.id,20) WHEN MATCHED THEN UPDATE SET v=t.v+1 RETURNING t.id"
				d := fixture.ControlsReferenceDiagnostic(setting.code, setting.message)
				if b.PG {
					d.Context = "while updating tuple " + tuple + ` in relation "t"`
				}
				var held *sql.Stmt
				if prepared {
					var e error
					held, e = worker.PrepareContext(p.Ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					defer held.Close()
				}
				var out fixture.RelationOutcome
				if b.PG {
					hp, wp := fixture.LockPID(t, p.Ctx, holder), fixture.LockPID(t, p.Ctx, worker)
					await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						return mergeUpdateOperation(ctx, worker, held, q)
					})
					fixture.RowWait(t, p.Ctx, wp, hp)
					out = await()
				} else {
					out = mergeUpdateOperation(p.Ctx, worker, held, q)
				}
				if out.Result != nil {
					t.Fatal("timeout exposed result")
				}
				fixture.AssertDiagnostic(t, out.Err, &d)
				s := fixture.ImplicitError("SELECT * FROM t", fixture.TransactionAborted())
				s.SelectRows = true
				fixture.RunStep(t, p.Ctx, worker, b.Recorder, s)
				run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(30)}}))
				fixture.LockExec(t, p.Ctx, worker, "ROLLBACK", 0)
				fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
				run(fixture.Exec(q, 2))
				run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}, {int64(3), int64(31)}}))
			})
		})
	}
}
