package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterConstraintCancellation(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("held_%t/transaction_%t", held, explicit), func(t *testing.T) {
				constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := enforcementRun(t, p, b, c)
					constraintModeGraph(run)
					q := "ALTER TABLE t ALTER CONSTRAINT nn NO INHERIT"
					var stmt *sql.Stmt
					if held {
						var err error
						stmt, err = c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					holder, observer := fixture.LockConnections(t, p.Ctx, b.DB)
					before := constraintModeSnapshot(t, p, b, observer)
					waiterPID, holderPID := 0, 0
					var oid int64
					if b.PG {
						waiterPID = fixture.LockPID(t, p.Ctx, c)
						holderPID = fixture.LockPID(t, p.Ctx, holder)
						oid = fixture.LockOID(t, p.Ctx, "t")
					}
					block, err := holder.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer block.Rollback()
					fixture.RunStep(t, p.Ctx, block, b.Recorder, fixture.Exec("LOCK TABLE ONLY t IN ACCESS SHARE MODE", 0))
					if explicit {
						run(fixture.Exec("BEGIN", 0))
						defer c.ExecContext(p.Ctx, "ROLLBACK")
					}
					ctx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					finished := make(chan struct{})
					await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						var result sql.Result
						var err error
						if held {
							result, err = stmt.ExecContext(ctx)
						} else {
							result, err = c.ExecContext(ctx, q)
						}
						close(finished)
						return fixture.RelationOutcome{Result: result, Err: err}
					})
					fixture.RelationLockPending(t, p, b, waiterPID, holderPID, oid, "AccessExclusiveLock", finished)
					fixture.RunStep(t, p.Ctx, observer, b.Recorder, constraintModeNN("t", "nn", false, true, 0, true))
					if b.PG {
						var ok bool
						if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", waiterPID).Scan(&ok); err != nil || !ok {
							t.Fatal(ok, err)
						}
					} else {
						cancel()
					}
					out := await()
					if out.Result != nil {
						t.Fatal("canceled ALTER returned Result")
					}
					if b.PG {
						want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
						fixture.AssertDiagnostic(t, out.Err, &want)
					} else if !errors.Is(out.Err, context.Canceled) {
						t.Fatal(out.Err)
					}
					if explicit {
						run(fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
						run(fixture.Exec("ROLLBACK", 0))
					}
					if err := block.Commit(); err != nil {
						t.Fatal(err)
					}
					constraintModeUnchanged(t, p, b, observer, before)
					if held {
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 0))
					} else {
						run(fixture.Exec(q, 0))
					}
					run(constraintModeNN("t", "nn", true, true, 0, true))
					run(constraintModeNN("child", "nn", false, true, 0, true))
					run(constraintModeNN("grandchild", "nn", false, false, 1, true))
				})
			})
		}
	}
	t.Run("native_pre_canceled_context", func(t *testing.T) {
		constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			if b.PG {
				return
			} // A pre-canceled pq call has no SQL diagnostic contract.
			run := enforcementRun(t, p, b, c)
			constraintModeGraph(run)
			before := constraintModeSnapshot(t, p, b, c)
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			result, err := c.ExecContext(ctx, "ALTER TABLE t ALTER CONSTRAINT nn NO INHERIT")
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(result, err)
			}
			constraintModeUnchanged(t, p, b, c, before)
		})
	})
}
