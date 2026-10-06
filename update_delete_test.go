package stuntgres_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
	"time"
)

func updateDeleteRows(t *testing.T, ctx context.Context, x transactionExecutor, q string, want [][]any) {
	t.Helper()
	rows, err := x.QueryContext(ctx, q)
	queryCoreRows(t, rows, err, []string{"id", "v"}, []string{"INT4", "INT4"}, want, true)
}

func TestUpdateCoreDeleteComposition(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			for _, deleteFirst := range []bool{false, true} {
				name := map[bool]string{false: "direct", true: "prepared"}[prepared] + map[bool]string{false: "/rollback", true: "/commit"}[commit] + map[bool]string{false: "/update_first", true: "/delete_first"}[deleteFirst]
				t.Run(name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					_, owner, peer := transactionConnections(t)
					exec := func(x transactionExecutor, q string, n int64) { transactionAffected(t, ctx, x, q, prepared, n) }
					exec(owner, "CREATE TABLE t(id INT,v INT)", 0)
					exec(owner, "CREATE TABLE copied(id INT,v INT)", 0)
					exec(owner, "INSERT INTO t VALUES(1,10),(2,20),(2,20)", 3)
					original := [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(2), int64(20)}}
					tx, err := owner.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					survivors := [][]any{{int64(2), int64(20)}, {int64(2), int64(20)}}
					if deleteFirst {
						exec(tx, "DELETE FROM t WHERE id=2", 2)
						exec(tx, "INSERT INTO copied SELECT id,v FROM t", 1)
						updateDeleteRows(t, ctx, tx, "SELECT id,v FROM copied", [][]any{{int64(1), int64(10)}})
						exec(tx, "UPDATE copied SET v=11", 1)
						exec(tx, "DELETE FROM copied WHERE v=11", 1)
						survivors = [][]any{{int64(1), int64(10)}}
					} else {
						exec(tx, "UPDATE t SET v=30 WHERE id=1", 1)
						exec(tx, "INSERT INTO t SELECT id,v FROM t WHERE id=1", 1)
						updateDeleteRows(t, ctx, tx, "SELECT id,v FROM t", [][]any{{int64(1), int64(30)}, {int64(1), int64(30)}, {int64(2), int64(20)}, {int64(2), int64(20)}})
						updateDeleteRows(t, ctx, peer, "SELECT id,v FROM t", original)
						exec(tx, "DELETE FROM t WHERE id=1", 2)
						exec(tx, "UPDATE t SET v=40 WHERE id=1", 0)
						exec(tx, "INSERT INTO t VALUES(3,30)", 1)
						exec(tx, "UPDATE t SET v=40 WHERE id=3", 1)
						exec(tx, "DELETE FROM t WHERE id=3 AND v=40", 1)
						exec(tx, "INSERT INTO copied SELECT id,v FROM t WHERE id=2", 2)
						updateDeleteRows(t, ctx, tx, "SELECT id,v FROM copied", survivors)
						exec(tx, "DELETE FROM copied WHERE v=20", 2)
						exec(tx, "UPDATE copied SET v=7", 0)
					}
					updateDeleteRows(t, ctx, tx, "SELECT id,v FROM t", survivors)
					updateDeleteRows(t, ctx, tx, "SELECT id,v FROM copied", [][]any{})
					updateDeleteRows(t, ctx, peer, "SELECT id,v FROM t", original)
					updateDeleteRows(t, ctx, peer, "SELECT id,v FROM copied", [][]any{})
					if commit {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
						survivors = original
					}
					if err != nil {
						t.Fatal(err)
					}
					updateDeleteRows(t, ctx, peer, "SELECT id,v FROM t", survivors)
					updateDeleteRows(t, ctx, peer, "SELECT id,v FROM copied", [][]any{})
				})
			}
		}
	}
}

func TestUpdateCoreDeleteCancellation(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "delete_wait", true: "update_wait"}[reverse]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				db, holder, owner := transactionConnections(t)
				peer, err := db.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				exec := func(x transactionExecutor, q string, n int64) { transactionAffected(t, ctx, x, q, false, n) }
				exec(peer, "CREATE TABLE t(id INT,v INT)", 0)
				exec(peer, "INSERT INTO t VALUES(1,10),(2,20),(3,30)", 3)
				q := "DELETE FROM t WHERE id=3"
				hold := "UPDATE t SET v=99 WHERE id=3"
				if reverse {
					q = "UPDATE t SET v=33 WHERE id=3"
					hold = "DELETE FROM t WHERE id=3"
				}
				var stmt *sql.Stmt
				if prepared {
					stmt, err = owner.PrepareContext(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				h, err := holder.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer h.Rollback()
				exec(h, hold, 1)
				exec(owner, "BEGIN", 0)
				defer func() {
					cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					owner.ExecContext(cleanup, "ROLLBACK")
				}()
				if reverse {
					exec(owner, "DELETE FROM t WHERE id=1", 1)
					exec(owner, "UPDATE t SET v=22 WHERE id=2", 1)
				} else {
					exec(owner, "UPDATE t SET v=20 WHERE id=1", 1)
					exec(owner, "DELETE FROM t WHERE id=2", 1)
				}
				wait, stop := context.WithTimeout(ctx, 25*time.Millisecond)
				var result sql.Result
				if prepared {
					result, err = stmt.ExecContext(wait)
				} else {
					result, err = owner.ExecContext(wait, q)
				}
				stop()
				if result != nil || !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(result, err)
				}
				_, err = owner.ExecContext(ctx, "UPDATE t SET v=0")
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
				updateDeleteRows(t, ctx, peer, "SELECT id,v FROM t", [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}})
				want := [][]any{{int64(1), int64(11)}, {int64(3), int64(30)}}
				if reverse {
					exec(peer, "DELETE FROM t WHERE id=1", 1)
					exec(peer, "UPDATE t SET v=21 WHERE id=2", 1)
					want = [][]any{{int64(2), int64(21)}, {int64(3), int64(30)}}
				} else {
					exec(peer, "UPDATE t SET v=11 WHERE id=1", 1)
					exec(peer, "DELETE FROM t WHERE id=2", 1)
				}
				exec(owner, "ROLLBACK", 0)
				if err = h.Rollback(); err != nil {
					t.Fatal(err)
				}
				updateDeleteRows(t, ctx, peer, "SELECT id,v FROM t", want)
			})
		}
	}
}

func TestUpdateCoreDeleteRowContention(t *testing.T) {
	for _, lane := range []string{"delete_blocks_update", "update_changes_predicate", "update_latest_delete"} {
		for _, commit := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(lane+map[bool]string{false: "/rollback", true: "/commit"}[commit]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					db, holder, worker := transactionConnections(t)
					peer, err := db.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					exec := func(x transactionExecutor, q string, n int64) { transactionAffected(t, ctx, x, q, false, n) }
					exec(peer, "CREATE TABLE t(id INT,v INT)", 0)
					exec(peer, "INSERT INTO t VALUES(1,10),(2,20)", 2)
					q, hold := "UPDATE t SET v=30 WHERE id=1", "DELETE FROM t WHERE id=1"
					if lane != "delete_blocks_update" {
						q = "DELETE FROM t WHERE v=10"
						hold = "UPDATE t SET v=20 WHERE id=1"
					}
					if lane == "update_latest_delete" {
						q = "DELETE FROM t WHERE id=1"
					}
					var stmt *sql.Stmt
					if prepared {
						stmt, err = worker.PrepareContext(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					h, err := holder.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer h.Rollback()
					exec(h, hold, 1)
					w, err := worker.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer w.Rollback()
					type outcome struct {
						result sql.Result
						err    error
					}
					done := make(chan outcome, 1)
					joined := false
					go func() {
						var r sql.Result
						var err error
						if prepared {
							r, err = w.StmtContext(ctx, stmt).ExecContext(ctx)
						} else {
							r, err = w.ExecContext(ctx, q)
						}
						done <- outcome{r, err}
					}()
					defer func() {
						cancel()
						if !joined {
							select {
							case <-done:
							case <-time.After(5 * time.Second):
								t.Error("mixed public DML worker not joined")
							}
						}
					}()
					// Public effect witness only. Actual queue/captured-view evidence is supplied
					// independently by the engine manager and PostgreSQL blocker oracle tests.
					updateDeleteRows(t, ctx, peer, "SELECT id,v FROM t", [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}})
					exec(peer, "UPDATE t SET v=21 WHERE id=2", 1)
					if commit {
						err = h.Commit()
					} else {
						err = h.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					var got outcome
					select {
					case got = <-done:
						joined = true
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
					want := int64(1)
					if commit && lane != "update_latest_delete" {
						want = 0
					}
					if got.err != nil || got.result == nil {
						t.Fatal(got.result, got.err)
					}
					if n, err := got.result.RowsAffected(); err != nil || n != want {
						t.Fatal(n, err, want)
					}
					if err = w.Commit(); err != nil {
						t.Fatal(err)
					}
					values := [][]any{{int64(2), int64(21)}}
					if lane == "delete_blocks_update" && !commit {
						values = [][]any{{int64(1), int64(30)}, {int64(2), int64(21)}}
					}
					if lane == "update_changes_predicate" && commit {
						values = [][]any{{int64(1), int64(20)}, {int64(2), int64(21)}}
					}
					updateDeleteRows(t, ctx, peer, "SELECT id,v FROM t", values)
				})
			}
		}
	}
}
