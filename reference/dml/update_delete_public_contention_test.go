package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceUpdateCoreDeleteRowContention(t *testing.T) {
	for _, lane := range []string{"delete_blocks_update", "update_changes_predicate", "update_latest_delete"} {
		for _, commit := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(lane+map[bool]string{false: "/rollback", true: "/commit"}[commit]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, backend := range p.Backends() {
						t.Run(backend.Name, func(t *testing.T) {
							ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
							defer cancel()
							db := backend.DB
							holder, worker := fixture.LockConnections(t, ctx, db)
							peer, err := db.Conn(ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer peer.Close()
							exec := func(x fixture.Executor, q string, n int64) { fixture.UpdateCoreExec(t, ctx, x, q, false, n) }
							rows := func(q string, want [][]any) {
								step := fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, want)
								step.Bag = true
								fixture.RunStep(t, ctx, peer, backend.Recorder, step)
							}
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
							rows("SELECT id,v FROM t", [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}})
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
							rows("SELECT id,v FROM t", values)
						})
					}
				})
			}
		}
	}
}
