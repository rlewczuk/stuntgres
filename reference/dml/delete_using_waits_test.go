package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteUsingWaitTimeout(t *testing.T) {
	for _, wait := range []string{"target", "source"} {
		for _, explicit := range []bool{false, true} {
			for _, terminal := range []string{"timeout", "cancel"} {
				t.Run(wait+map[bool]string{false: "/implicit", true: "/explicit"}[explicit]+"/"+terminal, func(t *testing.T) {
					deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						run := updateFromRun(t, p, b, c)
						run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
						run(fixture.Exec("CREATE TABLE s(k INT)", 0))
						run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
						run(fixture.Exec("INSERT INTO s VALUES(1),(2)", 2))
						// The worker owns the interrupted statement; c checks independently.
						worker, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer worker.Close()
						work := updateFromRun(t, p, b, worker)
						q := "DELETE FROM t d USING s WHERE d.id=s.k RETURNING d.id"
						stmt, err := worker.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
						if terminal == "cancel" {
							want.Message = "canceling statement due to user request"
						}
						pid := 0
						if b.PG {
							pid = fixture.LockPID(t, p.Ctx, worker)
							if wait == "target" {
								// Capture the physical tuple independently before mutation;
								// never manufacture native physical Context or copy the error.
								var tuple string
								if err := c.QueryRowContext(p.Ctx, "SELECT ctid::text FROM t WHERE id=2").Scan(&tuple); err != nil {
									t.Fatal(err)
								}
								want.Context = fmt.Sprintf("while deleting tuple %s in relation \"t\"", tuple)
							}
						}
						holder, err := b.DB.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						holderRun := updateFromRun(t, p, b, holder)
						if wait == "target" {
							holderRun(fixture.Exec("DELETE FROM t WHERE id=2", 1))
						} else {
							holderRun(fixture.Exec("ALTER TABLE s ALTER COLUMN k SET NOT NULL", 0))
						}
						if explicit {
							work(fixture.Exec("BEGIN", 0))
							work(fixture.Exec("INSERT INTO t VALUES(7,70)", 1))
							defer worker.ExecContext(p.Ctx, "ROLLBACK")
						}
						// Setup and Prepare finish at timeout 0; a statement timer also
						// counts parse and plan time.
						if terminal == "timeout" {
							work(fixture.Exec("SET statement_timeout='100ms'", 0))
						}
						ctx, cancel := context.WithCancel(p.Ctx)
						defer cancel()
						completed := make(chan struct{})
						join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
							defer close(completed)
							result, err := stmt.ExecContext(ctx)
							return fixture.RelationOutcome{Result: result, Err: err}
						})
						// The timer's exact outcome proves the wait; never race an observer
						// against it. Cancellation interrupts an admitted server wait.
						if terminal == "cancel" {
							if b.PG {
								updateFromPGWait(t, p.Ctx, pid)
								var canceled bool
								if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
									t.Fatal("cancel", canceled, err)
								}
							} else {
								fixture.RelationLockPending(t, p, b, 0, 0, 0, "", completed)
								cancel()
							}
						}
						o := join()
						if o.Result != nil || o.Rows != nil {
							t.Fatal("interruption exposed successful output", o)
						}
						if terminal == "cancel" && !b.PG {
							if !errors.Is(o.Err, context.Canceled) {
								t.Fatal("native cancellation", o.Err)
							}
						} else {
							fixture.AssertDiagnostic(t, o.Err, &want)
						}
						// The canceled owner recovers itself. Once a statement timer is
						// armed even RESET can time out, so that owner is disposed (its
						// transaction rolls back) and c checks the outcome instead.
						survivor := work
						if terminal == "timeout" {
							if err := worker.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
								t.Fatal("disposing timer-armed owner", err)
							}
							survivor = run
						} else if explicit {
							work(fixture.ErrorDiagnostic("SELECT id FROM t", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
							work(fixture.Exec("ROLLBACK", 0))
						}
						if err := holder.Rollback(); err != nil {
							t.Fatal(err)
						}
						survivor(deleteUsingTargets([][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
						survivor(fixture.Select("SELECT k FROM s ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
						survivor(fixture.Exec("DELETE FROM t d USING s WHERE d.id=s.k", 2))
						survivor(deleteUsingTargets(nil))
					})
				})
			}
		}
	}
}
