package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceMergeUpdateReadCommitted(t *testing.T) {
	for _, lane := range []string{"restart", "keyChange", "delete", "deleteNoInsert", "fixedSource", "noWiden"} {
		for _, commit := range []bool{false, true} {
			t.Run(lane+map[bool]string{false: "Rollback", true: "Commit"}[commit], func(t *testing.T) {
				mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {

					ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
					defer cancel()
					holder, worker := fixture.LockConnections(t, ctx, b.DB)
					run := fixture.MergeRun(t, p, b, false)
					run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1,10),(3,30)", 2))
					run(fixture.Exec("CREATE TABLE source(id INT,x INT)", 0))
					run(fixture.Exec("INSERT INTO source VALUES(1,7)", 1))
					var hp, wp int
					if b.PG {
						hp, wp = fixture.LockPID(t, ctx, holder), fixture.LockPID(t, ctx, worker)
					}
					q := "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED AND t.v>=20 THEN UPDATE SET v=100 WHEN MATCHED THEN UPDATE SET v=200"
					hold := "UPDATE t SET v=20 WHERE id=1"
					if lane == "keyChange" {
						hold = "UPDATE t SET id=2 WHERE id=1"
					}
					if lane == "delete" || lane == "deleteNoInsert" {
						hold = "DELETE FROM t WHERE id=1"
					}
					if lane == "fixedSource" {
						q = "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.x"
					}
					if lane == "noWiden" {
						q = "MERGE INTO t USING source s ON t.v=10 WHEN MATCHED THEN UPDATE SET v=100"
					}
					if lane == "keyChange" || lane == "delete" {
						q += " WHEN NOT MATCHED THEN INSERT VALUES(s.id,99)"
					}
					var held *sql.Stmt
					var err error
					if prepared {
						held, err = worker.PrepareContext(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
					}
					fixture.LockExec(t, ctx, holder, "BEGIN", 0)
					fixture.LockExec(t, ctx, holder, hold, 1)
					var await func() fixture.RelationOutcome
					if b.PG {
						await = fixture.LockOperation(t, ctx, func(c context.Context) fixture.RelationOutcome { return mergeUpdateOperation(c, worker, held, q) })
						fixture.RowWait(t, ctx, wp, hp)
					} else {
						mergeUpdateNativeTimeout(t, ctx, worker, held, q, false)
						run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(3), int64(30)}}))
						await = func() fixture.RelationOutcome { return mergeUpdateOperation(ctx, worker, held, q) }
					}
					if lane == "fixedSource" {
						run(fixture.Exec("UPDATE source SET x=8", 1))
					}
					if lane == "noWiden" {
						run(fixture.Exec("UPDATE t SET v=10 WHERE id=3", 1))
					}
					end := "ROLLBACK"
					if commit {
						end = "COMMIT"
					}
					fixture.LockExec(t, ctx, holder, end, 0)
					out := await()
					n := int64(1)
					if (lane == "deleteNoInsert" || lane == "noWiden") && commit {
						n = 0
					}
					if !b.PG && lane == "noWiden" {
						n = 2
						if commit {
							n = 1
						}
					}
					mergeInsertAffected(t, b, out.Result, out.Err, n)
					want := [][]any{{int64(1), int64(200)}, {int64(3), int64(30)}}
					switch lane {
					case "restart":
						if commit {
							want[0][1] = int64(100)
						}
					case "keyChange":
						if commit {
							want = [][]any{{int64(1), int64(99)}, {int64(2), int64(10)}, {int64(3), int64(30)}}
						}
					case "delete":
						if commit {
							want[0][1] = int64(99)
						}
					case "deleteNoInsert":
						if commit {
							want = want[1:]
						}
					case "fixedSource":
						want[0][1] = int64(7)
					case "noWiden":
						want[0][1] = int64(100)
						if commit {
							want[0][1] = int64(20)
						}
						want[1][1] = int64(10)
					}
					// Native retry has a fresh RC frontier, not the original retained candidate/source.
					if !b.PG {
						if lane == "fixedSource" {
							want[0][1] = int64(8)
						}
						if lane == "noWiden" {
							want[1][1] = int64(100)
						}
					}
					run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
				})
			})
		}
	}
}

func TestReferenceMergeUpdateRepeatableRead(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(map[bool]string{false: "Update", true: "Delete"}[deleted]+map[bool]string{false: "Rollback", true: "Commit"}[commit], func(t *testing.T) {
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
					hold := "UPDATE t SET v=11 WHERE id=1"
					if deleted {
						hold = "DELETE FROM t WHERE id=1"
					}
					fixture.LockExec(t, ctx, holder, hold, 1)
					q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=100 WHEN NOT MATCHED THEN INSERT VALUES(s.id,99)"
					var held *sql.Stmt
					var err error
					if prepared {
						held, err = worker.PrepareContext(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
					}
					var await func() fixture.RelationOutcome
					if b.PG {
						await = fixture.LockOperation(t, ctx, func(c context.Context) fixture.RelationOutcome { return mergeUpdateOperation(c, worker, held, q) })
						fixture.RowWait(t, ctx, wp, hp)
					} else {
						mergeUpdateNativeTimeout(t, ctx, worker, held, q, true)
						fixture.RunStep(t, ctx, worker, b.Recorder, fixture.Select("SELECT id,v FROM t ORDER BY id,v", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(21)}}))
						await = func() fixture.RelationOutcome { return mergeUpdateOperation(ctx, worker, held, q) }
					}
					end := "ROLLBACK"
					if commit {
						end = "COMMIT"
					}
					fixture.LockExec(t, ctx, holder, end, 0)
					out := await()
					if commit {
						if out.Result != nil {
							t.Fatal("serialization returned result")
						}
						action := "update"
						if deleted {
							action = "delete"
						}
						d := fixture.ControlsReferenceDiagnostic("40001", "could not serialize access due to concurrent "+action)
						fixture.AssertDiagnostic(t, out.Err, &d)
						_, err = worker.ExecContext(ctx, "UPDATE t SET v=0")
						aborted := fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
						fixture.AssertDiagnostic(t, err, &aborted)
					} else {
						mergeInsertAffected(t, b, out.Result, out.Err, 1)
						fixture.RunStep(t, ctx, worker, b.Recorder, fixture.Select("SELECT id,v FROM t ORDER BY id,v", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(100)}, {int64(2), int64(21)}}))
					}
					fixture.LockExec(t, ctx, worker, "ROLLBACK", 0)
					want := [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}
					if commit {
						if deleted {
							want = want[1:]
						} else {
							want[0][1] = int64(11)
						}
					}
					run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
				})
			})
		}
	}
}

func TestReferenceMergeUpdateRowLocks(t *testing.T) {
	for _, tc := range []struct {
		name, arms string
		blocked    bool
	}{
		{"nonkey", "WHEN MATCHED THEN UPDATE SET v=t.v+1", false},
		{"sameKey", "WHEN MATCHED THEN UPDATE SET id=t.id", false},
		{"changedKey", "WHEN MATCHED THEN UPDATE SET id=t.id+1", true},
		{"unselectedKeyArm", "WHEN MATCHED AND FALSE THEN UPDATE SET id=t.id WHEN MATCHED THEN UPDATE SET v=t.v+1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {

				ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
				defer cancel()
				holder, worker := fixture.LockConnections(t, ctx, b.DB)
				run := fixture.MergeRun(t, p, b, false)
				run(fixture.Exec("CREATE TABLE t(id INT NOT NULL UNIQUE,v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
				var hp, wp int
				if b.PG {
					hp, wp = fixture.LockPID(t, ctx, holder), fixture.LockPID(t, ctx, worker)
				}
				fixture.LockExec(t, ctx, holder, "BEGIN", 0)
				rows, err := holder.QueryContext(ctx, "SELECT id FROM t FOR KEY SHARE")
				if err != nil {
					t.Fatal(err)
				}
				for rows.Next() {
					var id int64
					if err = rows.Scan(&id); err != nil {
						t.Fatal(err)
					}
				}
				if err = rows.Close(); err != nil {
					t.Fatal(err)
				}
				q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id " + tc.arms
				var held *sql.Stmt
				if prepared {
					held, err = worker.PrepareContext(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
				}
				var await func() fixture.RelationOutcome
				if b.PG {
					await = fixture.LockOperation(t, ctx, func(c context.Context) fixture.RelationOutcome { return mergeUpdateOperation(c, worker, held, q) })
					if tc.blocked {
						fixture.RowWait(t, ctx, wp, hp)
						fixture.LockExec(t, ctx, holder, "ROLLBACK", 0)
					}
				} else {
					if tc.blocked {
						mergeUpdateNativeTimeout(t, ctx, worker, held, q, false)
						run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
						fixture.LockExec(t, ctx, holder, "ROLLBACK", 0)
					}
					await = func() fixture.RelationOutcome { return mergeUpdateOperation(ctx, worker, held, q) }
				}
				out := await()
				mergeInsertAffected(t, b, out.Result, out.Err, 1)
				if !tc.blocked {
					fixture.LockExec(t, ctx, holder, "ROLLBACK", 0)
				}
				id, v := int64(1), int64(10)
				if tc.name == "changedKey" {
					id = 2
				}
				if tc.name == "nonkey" || tc.name == "unselectedKeyArm" {
					v = 11
				}
				run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{id, v}}))
			})
		})
	}
}

// REL_18_0 nodeModifyTable.c:3381-3393 uses ExecUpdateLockMode only after
// TM_Updated; heapam.c:3429-3450 chooses the initial mode from actual key changes.
// A compatible writer forces that follow-chain branch while KEY SHARE survives.
func TestReferenceMergeUpdateFollowChainRowModes(t *testing.T) {
	for _, tc := range []struct {
		name, arms string
		blocked    bool
		v          int64
	}{
		{"nonkey", "WHEN MATCHED THEN UPDATE SET v=t.v+1", false, 21},
		{"sameKey", "WHEN MATCHED THEN UPDATE SET id=t.id", true, 20},
		{"unselectedKeyArm", "WHEN MATCHED AND FALSE THEN UPDATE SET id=t.id WHEN MATCHED THEN UPDATE SET v=t.v+1", true, 21},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {

				ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
				defer cancel()
				holder, worker := fixture.LockConnections(t, ctx, b.DB)
				writer, err := b.DB.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
					defer stop()
					writer.ExecContext(cleanup, "ROLLBACK")
					writer.Close()
				}()
				run := fixture.MergeRun(t, p, b, false)
				run(fixture.Exec("CREATE TABLE t(id INT NOT NULL UNIQUE,v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
				var hp, wp, xp int
				if b.PG {
					hp, wp, xp = fixture.LockPID(t, ctx, holder), fixture.LockPID(t, ctx, worker), fixture.LockPID(t, ctx, writer)
				}
				fixture.LockExec(t, ctx, holder, "BEGIN", 0)
				fixture.RunStep(t, ctx, holder, b.Recorder, fixture.Select("SELECT id FROM t FOR KEY SHARE", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id " + tc.arms
				var held *sql.Stmt
				if prepared {
					held, err = worker.PrepareContext(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
				}
				fixture.LockExec(t, ctx, writer, "BEGIN", 0)
				fixture.LockExec(t, ctx, writer, "UPDATE t SET v=20 WHERE id=1", 1)
				var await func() fixture.RelationOutcome
				if b.PG {
					await = fixture.LockOperation(t, ctx, func(c context.Context) fixture.RelationOutcome { return mergeUpdateOperation(c, worker, held, q) })
					fixture.RowWait(t, ctx, wp, xp)
					fixture.LockExec(t, ctx, writer, "COMMIT", 0)
					// Pinned follow-chain union waits on KEY SHARE; nonkey completes with A alive.
					if tc.blocked {
						fixture.RowWait(t, ctx, wp, hp)
						fixture.LockExec(t, ctx, holder, "ROLLBACK", 0)
					}
				} else {
					mergeUpdateNativeTimeout(t, ctx, worker, held, q, false)
					run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
					fixture.LockExec(t, ctx, writer, "COMMIT", 0)
					// Fresh retry uses actual initial key mode; exact follow-chain modes are
					// independently exercised by synchronized manager-pending package tests.
					await = func() fixture.RelationOutcome { return mergeUpdateOperation(ctx, worker, held, q) }
				}
				out := await()
				mergeInsertAffected(t, b, out.Result, out.Err, 1)
				if !b.PG || !tc.blocked {
					fixture.LockExec(t, ctx, holder, "ROLLBACK", 0)
				}
				run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), tc.v}}))
			})
		})
	}
}
