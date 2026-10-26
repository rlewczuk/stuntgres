package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterDropColumnRoutineAdmission(t *testing.T) {
	for _, resource := range []string{"relation", "routine"} {
		for _, held := range []bool{false, true} {
			name := resource + "/direct"
			if held {
				name = resource + "/held"
			}
			t.Run(name, func(t *testing.T) {
				dcrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
					dcrSetup(t, p, b, c)
					before := dcrRows(t, p, c, dcrSnapshotSQL)
					blocker, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer blocker.Close()
					_, worker, _ := fixture.NoticeReferencePool(t, p, b.PG, notices)
					defer worker.Close()
					ownerPID, workerPID := 0, 0
					if b.PG {
						ownerPID = fixture.LockPID(t, p.Ctx, blocker)
						workerPID = fixture.LockPID(t, p.Ctx, worker)
					}
					var routine int64
					if err := c.QueryRowContext(p.Ctx, "SELECT 'dcr_body()'::regprocedure::oid::bigint").Scan(&routine); err != nil {
						t.Fatal(err)
					}
					dcrExec(t, p, b, blocker, "BEGIN")
					defer blocker.ExecContext(p.Ctx, "ROLLBACK")
					if resource == "relation" {
						dcrExec(t, p, b, blocker, "LOCK TABLE dcr_t IN ACCESS SHARE MODE")
					} else {
						// Creating a private standard dependent retains provider AS,
						// without retaining any lock on the column's relation.
						dcrExec(t, p, b, blocker, "CREATE FUNCTION dcr_private() RETURNS integer LANGUAGE SQL RETURN dcr_body()")
					}
					q := "ALTER TABLE dcr_t DROP a CASCADE"
					var x fixture.Executor = worker
					if held {
						stmt, err := worker.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						x = fixture.HeldStatement{Executor: worker, Statement: stmt, SQL: q}
					}
					ctx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					finished := make(chan struct{})
					join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(finished)
						r, err := x.ExecContext(ctx, q)
						return fixture.RelationOutcome{Result: r, Err: err}
					})
					joined := false
					defer func() {
						cancel()
						if !joined {
							join()
						}
					}()
					if !b.PG {
						fixture.RelationLockPending(t, p, b, 0, 0, 0, "", finished)
					} else if resource == "relation" {
						fixture.LockWaitBehind(t, p.Ctx, workerPID, ownerPID, fixture.LockOID(t, p.Ctx, "dcr_t"), "AccessExclusiveLock", finished)
					} else {
						dcrRoutineWait(t, p, workerPID, ownerPID, routine, finished)
					}
					if b.PG {
						var canceled bool
						if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", workerPID).Scan(&canceled); err != nil || !canceled {
							t.Fatal(canceled, err)
						}
					} else {
						cancel()
					}
					out := join()
					joined = true
					if b.PG {
						want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
						fixture.AssertDiagnostic(t, out.Err, &want)
					} else if !errors.Is(out.Err, context.Canceled) {
						t.Fatal("ordinary native cancellation", out.Err)
					}
					dcrExec(t, p, b, blocker, "ROLLBACK")
					dcrUnchanged(t, p, b, c, before)
					dcrNotices(t, notices)
					// Server cancel leaves the same physical PostgreSQL owner usable.
					dcrRun(t, p, b, worker, fixture.Select("SELECT 1 AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
					dcrExec(t, p, b, c, "ALTER TABLE dcr_t DROP a CASCADE")
					dcrNotices(t, notices, "function dcr_body()")
					dcrInventory(t, p, b, c)
					dcrRun(t, p, b, c, fixture.Select("SELECT * FROM dcr_t", []string{"b", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(8), int64(9)}}))
				})
			})
		}
	}
}

func dcrRoutineWait(t *testing.T, p *fixture.Pair, waiter, holder int, object int64, finished <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	for {
		select {
		case <-finished:
			t.Fatal("column retirement completed before routine admission")
		default:
		}
		var pending bool
		err := fixture.Admin.QueryRowContext(ctx, "SELECT $2=ANY(pg_blocking_pids($1)) AND EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='object' AND classid=1255 AND objid=$3 AND mode='AccessExclusiveLock' AND NOT granted)", waiter, holder, object).Scan(&pending)
		if err != nil {
			t.Fatal("routine retirement wait", err)
		}
		if pending {
			return
		}
		runtime.Gosched()
	}
}
