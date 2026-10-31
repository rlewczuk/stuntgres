package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func clusterCommandCancellationScenario(t *testing.T) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				clusterCommandSeed(t, p, b, c)
				persistenceExec(t, p, b, c, "ALTER TABLE t CLUSTER ON a")
				holder, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Close()
				observer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer observer.Close()
				q := "CLUSTER t USING b"
				var held *sql.Stmt
				if prepared {
					held = clusterCommandHeld(t, p, b, c, q)
				}
				workerPID, holderPID := 0, 0
				var oid int64
				if b.PG {
					workerPID, holderPID = fixture.LockPID(t, p.Ctx, c), fixture.LockPID(t, p.Ctx, holder)
					oid = fixture.LockOID(t, p.Ctx, "t")
				}
				tx, err := holder.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				persistenceExec(t, p, b, tx, "LOCK TABLE t IN ACCESS SHARE MODE")
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				finished := make(chan struct{})
				join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(finished)
					var result sql.Result
					var err error
					if prepared {
						result, err = held.ExecContext(ctx)
					} else {
						result, err = c.ExecContext(ctx, q)
					}
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				fixture.RelationLockPending(t, p, b, workerPID, holderPID, oid, "AccessExclusiveLock", finished)
				persistenceRun(t, p, b, observer, clusterCommandBits(true, false))
				if b.PG {
					var ok bool
					if err = observer.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", workerPID).Scan(&ok); err != nil || !ok {
						t.Fatal(ok, err)
					}
				} else {
					cancel()
				}
				out := join()
				if out.Result != nil || out.Rows != nil {
					t.Fatal("canceled CLUSTER exposed result")
				}
				if b.PG {
					want := persistenceDiagnostic("57014", "canceling statement due to user request")
					fixture.AssertDiagnostic(t, out.Err, &want)
				} else if !errors.Is(out.Err, context.Canceled) {
					t.Fatal(out.Err)
				}
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				persistenceRun(t, p, b, observer, clusterCommandBits(true, false))
				persistenceRun(t, p, b, observer, clusterCommandRows())
				persistenceRun(t, p, b, observer, clusterCommandProgressIdle())
				persistenceExec(t, p, b, observer, q)
				persistenceRun(t, p, b, observer, clusterCommandBits(false, true))
				persistenceRun(t, p, b, observer, clusterCommandRows())
			})
		})
	}
}

func TestReferenceClusterLocksCancellation(t *testing.T) {
	t.Run("cancellation", clusterCommandCancellationScenario)
	for _, tc := range []struct{ name, setting, code, message string }{
		{"lock_timeout", "SET lock_timeout='10ms'", "55P03", "canceling statement due to lock timeout"},
		{"statement_timeout", "SET statement_timeout='100ms'", "57014", "canceling statement due to statement timeout"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "_direct", true: "_held"}[prepared], func(t *testing.T) {
				persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					clusterCommandSeed(t, p, b, c)
					persistenceExec(t, p, b, c, "ALTER TABLE t CLUSTER ON a")
					holder, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Close()
					tx, err := holder.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					persistenceExec(t, p, b, tx, "LOCK TABLE t IN ACCESS SHARE MODE")
					var held *sql.Stmt
					if prepared {
						held = clusterCommandHeld(t, p, b, c, "CLUSTER t USING b")
					}
					persistenceExec(t, p, b, c, tc.setting)
					want := persistenceDiagnostic(tc.code, tc.message)
					if prepared {
						_, err = held.ExecContext(p.Ctx)
						fixture.AssertDiagnostic(t, err, &want)
					} else {
						persistenceRun(t, p, b, c, fixture.ErrorDiagnostic("CLUSTER t USING b", want))
					}
					if err = tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					persistenceRun(t, p, b, holder, clusterCommandBits(true, false))
					persistenceRun(t, p, b, holder, clusterCommandRows())
					persistenceRun(t, p, b, holder, clusterCommandProgressIdle())
				})
			})
		}
	}
	for _, end := range []string{"COMMIT", "ROLLBACK", "ROLLBACK TO s"} {
		t.Run("reader_wait_"+end, func(t *testing.T) {
			persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				clusterCommandSeed(t, p, b, c)
				reader, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				workerPID, holderPID := 0, 0
				var oid int64
				if b.PG {
					workerPID, holderPID = fixture.LockPID(t, p.Ctx, reader), fixture.LockPID(t, p.Ctx, c)
					oid = fixture.LockOID(t, p.Ctx, "t")
				}
				for _, q := range []string{"BEGIN", "SAVEPOINT s", "CLUSTER t USING a"} {
					persistenceExec(t, p, b, c, q)
				}
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				finished := make(chan struct{})
				join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(finished)
					rows, err := reader.QueryContext(ctx, "SELECT id FROM t ORDER BY id")
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				fixture.RelationLockPending(t, p, b, workerPID, holderPID, oid, "AccessShareLock", finished)
				persistenceExec(t, p, b, c, end)
				out := join()
				if out.Err != nil {
					t.Fatal(out.Err)
				}
				fixture.StringCoreRetainedRows(t, out.Rows, nil, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}}, false)
			})
		})
	}
}
