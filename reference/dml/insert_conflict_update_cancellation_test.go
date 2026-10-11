package reference_test

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceInsertConflictUpdateCancellation(t *testing.T) {
	for _, wait := range []string{"row", "key"} {
		for _, mode := range []string{"caller", "lock_timeout", "statement_timeout"} {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/held=%t", wait, mode, held), func(t *testing.T) {
					insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						a, c := fixture.LockConnections(t, p.Ctx, b.DB)
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
						run(fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0))
						run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
						if wait == "row" {
							run(fixture.Exec("INSERT INTO t VALUES(3,30)", 1))
						}
						fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
						if wait == "key" {
							fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(3,30)", 1)
						} else {
							fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT v FROM t WHERE k=3 FOR UPDATE", []string{"v"}, []string{"INT4"}, [][]any{{int64(30)}}))
						}
						released := false
						defer func() {
							if !released {
								ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
								defer stop()
								if _, e := a.ExecContext(ctx, "ROLLBACK"); e != nil {
									t.Error(e)
								}
							}
						}()
						if mode != "caller" {
							run(fixture.Exec("SET "+mode+"='1s'", 0))
						}
						run(fixture.Exec("BEGIN", 0))
						run(fixture.Exec("INSERT INTO t VALUES(8,80)", 1))
						run(fixture.Exec("SAVEPOINT recover", 0))
						q := "INSERT INTO t VALUES(2,2),(1,20),(3,3) ON CONFLICT(k) DO UPDATE SET v=excluded.v"
						var x fixture.Executor = c
						if held {
							stmt, e := c.PrepareContext(p.Ctx, q)
							if e != nil {
								t.Fatal(e)
							}
							defer stmt.Close()
							x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
						}
						ctx, cancel := context.WithCancel(p.Ctx)
						defer cancel()
						if mode == "caller" && !b.PG {
							cancel()
							ctx, cancel = context.WithTimeout(p.Ctx, 100*time.Millisecond)
							defer cancel()
						}
						ap, cp := 0, 0
						if b.PG {
							ap, cp = fixture.RowPIDs(t, p.Ctx, a, c)
						}
						done := fixture.RowAsync(t, ctx, x, q, false, false)
						conflictUpdateWait(t, p, b, cp, ap)
						if mode == "caller" && b.PG {
							var canceled bool
							if e := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", cp).Scan(&canceled); e != nil || !canceled {
								t.Fatal(canceled, e)
							}
						}
						r := fixture.LockResult(t, done)
						if r.Err == nil || r.Result != nil {
							t.Fatal("failed command exposed result", r)
						}
						if mode == "caller" && !b.PG {
							if !errors.Is(r.Err, context.DeadlineExceeded) {
								t.Fatal(r.Err)
							}
							var state interface{ SQLState() string }
							if errors.As(r.Err, &state) {
								t.Fatal("fabricated caller SQLSTATE", r.Err)
							}
						} else {
							code, msg := "57014", "canceling statement due to user request"
							if mode == "lock_timeout" {
								code, msg = "55P03", "canceling statement due to lock timeout"
							}
							if mode == "statement_timeout" {
								msg = "canceling statement due to statement timeout"
							}
							d := fixture.ControlsReferenceDiagnostic(code, msg)
							if b.PG {
								if wait == "row" {
									d.Context = "while locking tuple (0,2) in relation \"t\""
								} else {
									d.Context = "while inserting index tuple (0,2) in relation \"t\""
								}
							}
							fixture.AssertDiagnostic(t, r.Err, &d)
						}
						// Root's pinned PostgreSQL observations distinguish actual row
						// locking from selected-index arbitration contexts above.
						s := fixture.ErrorDiagnostic("SELECT k FROM t", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
						run(s)
						run(fixture.Exec("ROLLBACK TO SAVEPOINT recover", 0))
						rows := [][]any{{int64(1), int64(10)}, {int64(8), int64(80)}}
						if wait == "row" {
							rows = [][]any{{int64(1), int64(10)}, {int64(3), int64(30)}, {int64(8), int64(80)}}
						}
						run(conflictUpdateRows(rows...))
						fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
						released = true
						if mode != "caller" {
							run(fixture.Exec("SET "+mode+"=0", 0))
						}
						fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 3))
						run(fixture.Exec("COMMIT", 0))
						run(conflictUpdateRows([]any{int64(1), int64(20)}, []any{int64(2), int64(2)}, []any{int64(3), int64(3)}, []any{int64(8), int64(80)}))
					})
				})
			}
		}
	}
}
