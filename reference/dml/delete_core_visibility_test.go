package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Untagged replacement for the formerly reference-tagged visibility definition.
// The original retained prepared/repeat/physical-bag/commit/rollback assertions
// remain shared; prior-own lineage adds private survivor and savepoint witnesses.
func TestReferenceDeleteCoreVisibility(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, false)
				run(fixture.Exec("CREATE TABLE t(id INT NOT NULL)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1),(2),(2)", 3))
				owner, peer := fixture.LockConnections(t, p.Ctx, b.DB)
				tx, err := owner.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				q := "DELETE FROM t WHERE id=$1"
				var held *sql.Stmt
				if prepared {
					held, err = tx.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
				}
				for _, want := range []int64{1, 0} {
					var result sql.Result
					if held != nil {
						result, err = held.ExecContext(p.Ctx, 1)
					} else {
						result, err = tx.ExecContext(p.Ctx, q, 1)
					}
					fixture.InsertSelectAffected(t, result, err, want)
				}
				fixture.DeleteReferenceRows(t, p, tx, b.Recorder, 2, 2)
				fixture.DeleteReferenceRows(t, p, peer, b.Recorder, 1, 2, 2)
				fixture.LockExec(t, p.Ctx, tx, "INSERT INTO t VALUES(7)", 1)
				step := fixture.Exec("DELETE FROM t WHERE id=7", 1)
				step.Prepared = prepared
				fixture.InsertSelectRun(t, p.Ctx, tx, b.Recorder, step)
				fixture.DeleteReferenceRows(t, p, tx, b.Recorder, 2, 2)
				step = fixture.Exec("DELETE FROM t WHERE id=2", 2)
				step.Prepared = prepared
				fixture.InsertSelectRun(t, p.Ctx, tx, b.Recorder, step)
				fixture.LockExec(t, p.Ctx, tx, "INSERT INTO t VALUES(2),(2)", 2)
				fixture.DeleteReferenceRows(t, p, tx, b.Recorder, 2, 2)
				fixture.DeleteReferenceRows(t, p, peer, b.Recorder, 1, 2, 2)
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				if commit {
					fixture.DeleteReferenceRows(t, p, peer, b.Recorder, 2, 2)
				} else {
					fixture.DeleteReferenceRows(t, p, peer, b.Recorder, 1, 2, 2)
				}
			})
		})
	}
	for _, lane := range []string{"priorOwnInsert", "sameBaseUpdate"} {
		t.Run(lane, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, false)
				run(fixture.Exec("CREATE TABLE t(id INT NOT NULL,v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
				owner, peer := fixture.LockConnections(t, p.Ctx, b.DB)
				exec := func(q string, n int64) {
					step := fixture.Exec(q, n)
					fixture.InsertSelectRun(t, p.Ctx, owner, b.Recorder, step)
				}
				read := func(x fixture.Executor, rows [][]any) {
					fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, rows))
				}
				original := [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}
				exec("BEGIN", 0)
				defer owner.ExecContext(p.Ctx, "ROLLBACK")
				id := int64(7)
				ancestor := [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(7), int64(70)}}
				survivor := original
				if lane == "priorOwnInsert" {
					exec("INSERT INTO t VALUES(7,70)", 1)
				} else {
					id = 1
					exec("UPDATE t SET v=11 WHERE id=1", 1)
					ancestor = [][]any{{int64(1), int64(11)}, {int64(2), int64(20)}}
					survivor = [][]any{{int64(2), int64(20)}}
				}
				read(owner, ancestor)
				read(peer, original)
				exec("SAVEPOINT keep", 0)
				q := "DELETE FROM t WHERE id=$1"
				var held *sql.Stmt
				var err error
				if prepared {
					held, err = owner.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
				}
				deleteRow := func(n int64) {
					var result sql.Result
					if held != nil {
						result, err = held.ExecContext(p.Ctx, id)
					} else {
						result, err = owner.ExecContext(p.Ctx, q, id)
					}
					fixture.InsertSelectAffected(t, result, err, n)
				}
				deleteRow(1)
				read(owner, survivor)
				read(peer, original)
				deleteRow(0)
				read(owner, survivor)
				read(peer, original)
				exec("ROLLBACK TO keep", 0)
				read(owner, ancestor)
				read(peer, original)
				// The same retained handle remains healthy after restoring the ancestor.
				deleteRow(1)
				read(owner, survivor)
				deleteRow(0)
				read(owner, survivor)
				exec("ROLLBACK", 0)
				read(owner, original)
				read(peer, original)
			})
		})
	}
}
