package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDeleteCurrentOfCancellationAndFailure(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			t.Run("returning_failure", func(t *testing.T) {
				currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := updateFromRun(t, p, b, c)
					currentOfSeed(run)
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1 FOR UPDATE", 0))
					run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
					run(fixture.Exec("INSERT INTO t VALUES(4,'prior')", 1))
					run(fixture.Exec("SAVEPOINT before_failure", 0))
					s := currentOfError("DELETE FROM t WHERE CURRENT OF c RETURNING id,10/(id-id)", "22012", "division by zero", prepared)
					s.SelectRows = true
					run(s)
					run(fixture.Exec("ROLLBACK TO before_failure", 0))
					fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2, 3, 4)
					run(fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 1))
					fixture.DeleteReferenceRows(t, p, c, b.Recorder, 2, 3, 4)
					run(fixture.Exec("ROLLBACK", 0))
					fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2, 3)
				})
			})
			t.Run("native_precancel", func(t *testing.T) {
				currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					if b.PG {
						return
					} // pq fast utility cancellation is not the native owner contract.
					run := updateFromRun(t, p, b, c)
					currentOfSeed(run)
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1 FOR UPDATE", 0))
					run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
					q := "DELETE FROM t WHERE CURRENT OF c RETURNING id"
					var stmt *sql.Stmt
					var err error
					if prepared {
						stmt, err = c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					var rows *sql.Rows
					if prepared {
						rows, err = stmt.QueryContext(ctx)
					} else {
						rows, err = c.QueryContext(ctx, q)
					}
					if rows != nil {
						rows.Close()
						t.Fatal("pre-cancel exposed RETURNING")
					}
					currentOfOrdinaryCancel(t, err, context.Canceled)
					fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2, 3)
					run(fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 1))
					run(fixture.Exec("ROLLBACK", 0))
					fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2, 3)
				})
			})
			t.Run("admitted_wait", func(t *testing.T) {
				currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := updateFromRun(t, p, b, c)
					currentOfSeed(run)
					holder, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Close()
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1", 0))
					run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
					run(fixture.Exec("INSERT INTO t VALUES(4,'prior')", 1))
					run(fixture.Exec("SAVEPOINT before_cancel", 0))
					h, err := holder.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer h.Rollback()
					fixture.LockExec(t, p.Ctx, h, "UPDATE t SET v='held' WHERE id=1", 1)
					q := "DELETE FROM t WHERE CURRENT OF c RETURNING id,v"
					var stmt *sql.Stmt
					if prepared {
						stmt, err = c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					var pid, hpid int
					var tuple string
					if b.PG {
						pid = fixture.LockPID(t, p.Ctx, c)
						hpid = fixture.LockPID(t, p.Ctx, holder)
						if err = fixture.Admin.QueryRowContext(p.Ctx, "SELECT ctid::text FROM t WHERE id=1").Scan(&tuple); err != nil {
							t.Fatal(err)
						}
					}
					ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
					defer cancel()
					waitCtx := ctx
					var stop context.CancelFunc
					if !b.PG {
						waitCtx, stop = context.WithTimeout(ctx, 50*time.Millisecond)
						defer stop()
					}
					await := fixture.LockOperation(t, ctx, func(context.Context) fixture.RelationOutcome {
						var rows *sql.Rows
						var e error
						if prepared {
							rows, e = stmt.QueryContext(waitCtx)
						} else {
							rows, e = c.QueryContext(waitCtx, q)
						}
						return fixture.RelationOutcome{Rows: rows, Err: e}
					})
					if b.PG {
						currentOfWaitRow(t, ctx, pid, hpid)
						var signaled bool
						if err = fixture.Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
							t.Fatal("admin cancel failed", signaled, err)
						}
					}
					o := await()
					if o.Rows != nil {
						o.Rows.Close()
						t.Fatal("canceled delete exposed partial RETURNING")
					}
					if b.PG {
						fixture.AssertDiagnostic(t, o.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request", Context: "while deleting tuple " + tuple + ` in relation "t"`})
					} else {
						currentOfOrdinaryCancel(t, o.Err, context.DeadlineExceeded)
					}
					fixture.DeleteReferenceRows(t, p, holder, b.Recorder, 1, 2, 3)
					run(fixture.Exec("ROLLBACK TO before_cancel", 0))
					fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2, 3, 4)
					if err = h.Rollback(); err != nil {
						t.Fatal(err)
					}
					run(fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 1))
					fixture.DeleteReferenceRows(t, p, c, b.Recorder, 2, 3, 4)
					run(fixture.Exec("ROLLBACK", 0))
					fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2, 3)
				})
			})
		})
	}
}
func currentOfOrdinaryCancel(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("cancel got %v want %v", err, want)
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		t.Fatal("ordinary context cancellation gained SQLSTATE", err)
	}
}
