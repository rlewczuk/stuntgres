package reference_test

import (
	"context"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// This observes a real PostgreSQL queue only. Native callers need a separate
// public admission barrier or a genuine engine-manager mechanism witness;
// starting RowAsync alone never proves native capture or pending acquisition.
func conflictUpdateWait(t *testing.T, p *fixture.Pair, b fixture.Backend, waiter, holder int) {
	t.Helper()
	if b.PG {
		ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
		defer cancel()
		fixture.RowWait(t, ctx, waiter, holder)
	}
}
func TestReferenceInsertConflictUpdateContention(t *testing.T) {
	for _, op := range []string{"payload", "delete", "key", "predicate_false", "predicate_true"} {
		for _, commit := range []bool{false, true} {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/commit=%t/held=%t", op, commit, held), func(t *testing.T) {
					insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						a, c := fixture.LockConnections(t, p.Ctx, b.DB)
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
						run(fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0))
						run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
						q := "INSERT INTO t VALUES(1,1) ON CONFLICT(k) DO UPDATE SET v=t.v+excluded.v"
						if op == "predicate_false" {
							q += " WHERE t.v<15"
						}
						if op == "predicate_true" {
							q += " WHERE t.v>15"
						}
						var x fixture.Executor = c
						if held {
							stmt, e := c.PrepareContext(p.Ctx, q)
							if e != nil {
								t.Fatal(e)
							}
							defer stmt.Close()
							x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
						}
						run(fixture.Exec("BEGIN", 0))
						change := "UPDATE t SET v=20"
						if op == "delete" {
							change = "DELETE FROM t"
						}
						if op == "key" {
							change = "UPDATE t SET k=2"
						}
						run(fixture.Exec(change, 1))
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO t VALUES(9,9)", 1))
						fixture.RunStep(t, p.Ctx, c, b.Recorder, conflictUpdateRows([]any{int64(1), int64(10)}, []any{int64(9), int64(9)}))
						ap, cp := 0, 0
						if b.PG {
							ap, cp = fixture.RowPIDs(t, p.Ctx, a, c)
						}
						done := fixture.RowAsync(t, p.Ctx, x, q, false, false)
						conflictUpdateWait(t, p, b, cp, ap)
						end := "ROLLBACK"
						if commit {
							end = "COMMIT"
						}
						run(fixture.Exec(end, 0))
						r := fixture.LockResult(t, done)
						fixture.State(t, r.Err, "")
						count := int64(1)
						v := int64(11)
						if commit {
							v = 21
						}
						if op == "predicate_false" && commit {
							count = 0
							v = 20
						}
						if op == "predicate_true" && !commit {
							count = 0
							v = 10
						}
						rows := [][]any{{int64(1), v}, {int64(9), int64(9)}}
						if commit && op == "delete" {
							rows = [][]any{{int64(1), int64(1)}, {int64(9), int64(9)}}
						}
						if commit && op == "key" {
							rows = [][]any{{int64(1), int64(1)}, {int64(2), int64(10)}, {int64(9), int64(9)}}
						}
						fixture.LockAffected(t, r.Result, count)
						run(conflictUpdateRows(rows...))
					})
				})
			}
		}
	}
}
func TestReferenceInsertConflictUpdateVisibility(t *testing.T) {
	for _, level := range []string{"READ UNCOMMITTED", "READ COMMITTED", "REPEATABLE READ", "SERIALIZABLE"} {
		for _, condition := range []string{"TRUE", "FALSE", "NULL"} {
			t.Run(level+condition, func(t *testing.T) {
				insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, c := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0))
					run(fixture.Exec("BEGIN ISOLATION LEVEL "+level, 0))
					run(conflictUpdateRows())
					fixture.LockExec(t, p.Ctx, c, "INSERT INTO t VALUES(1,10)", 1)
					q := "INSERT INTO t VALUES(1,1) ON CONFLICT(k) DO UPDATE SET v=t.v+excluded.v WHERE " + condition
					if level == "READ COMMITTED" || level == "READ UNCOMMITTED" {
						n := int64(0)
						if condition == "TRUE" {
							n = 1
						}
						run(fixture.Exec(q, n))
					} else {
						run(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("40001", "could not serialize access due to concurrent update")))
					}
					run(fixture.Exec("ROLLBACK", 0))
					run(conflictUpdateRows([]any{int64(1), int64(10)}))
				})
			})
		}
	}
	// The source statement snapshot cannot see the row whose actual identity is updated.
	t.Run("invisible_source", func(t *testing.T) {
		insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a, c := fixture.LockConnections(t, p.Ctx, b.DB)
			workerCtx, cancelWorker := context.WithCancel(p.Ctx)
			var done <-chan fixture.RelationOutcome
			joined := false
			defer func() {
				cancelWorker()
				cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
				if _, err := a.ExecContext(cleanupCtx, "ROLLBACK"); err != nil {
					t.Error("release source holder", err)
				}
				stop()
				if done != nil && !joined {
					select {
					case outcome := <-done:
						if outcome.Rows != nil {
							outcome.Rows.Close()
						}
						if outcome.Stmt != nil {
							outcome.Stmt.Close()
						}
						joined = true
					case <-time.After(5 * time.Second):
						t.Error("invisible-source worker did not join")
					}
				}
				// CHECK evaluation may acquire the same session marker again
				// for the replacement row. Release every count before the
				// fixture's retained-connection teardown, including failures.
				for _, connection := range []fixture.Executor{a, c} {
					cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
					if _, err := connection.ExecContext(cleanupCtx, "SELECT pg_advisory_unlock_all()"); err != nil {
						t.Error("release admission marker", err)
					}
					stop()
				}
			}()
			fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(k INT UNIQUE,v INT CHECK(v=10 OR pg_advisory_lock(71931) IS NOT NULL))", 0)
			fixture.LockExec(t, p.Ctx, a, "CREATE TABLE s(k INT,v INT)", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO s VALUES(1,1)", 1)
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,10)", 1)
			// CHECK evaluation order does not promise short-circuiting.
			// Clear seed admission counts before observing worker ownership.
			if _, err := a.ExecContext(p.Ctx, "SELECT pg_advisory_unlock_all()"); err != nil {
				t.Fatal("reset seed admission marker", err)
			}
			var available, unlocked bool
			if err := c.QueryRowContext(p.Ctx, "SELECT pg_try_advisory_lock(71931)").Scan(&available); err != nil || !available {
				t.Fatal("holder unexpectedly owns source admission marker", available, err)
			}
			if err := c.QueryRowContext(p.Ctx, "SELECT pg_advisory_unlock(71931)").Scan(&unlocked); err != nil || !unlocked {
				t.Fatal("release initial marker probe", unlocked, err)
			}
			q := "INSERT INTO t SELECT s.k,s.v FROM s LEFT JOIN t ON t.k=s.k WHERE t.k IS NULL ON CONFLICT(k) DO UPDATE SET v=t.v+excluded.v"
			done = fixture.RowAsync(t, workerCtx, c, q, true, false)
			markerCtx, stop := context.WithTimeout(p.Ctx, 5*time.Second)
			defer stop()
			for {
				var acquired bool
				if err := a.QueryRowContext(markerCtx, "SELECT pg_try_advisory_lock(71931)").Scan(&acquired); err != nil {
					t.Fatal("observe materialized source admission", err)
				}
				if !acquired {
					break
				}
				var unlocked bool
				if err := a.QueryRowContext(markerCtx, "SELECT pg_advisory_unlock(71931)").Scan(&unlocked); err != nil || !unlocked {
					t.Fatal("release observer marker probe", unlocked, err)
				}
				runtime.Gosched()
			}
			// Proposal CHECK validation occurs after the LEFT JOIN source
			// has been materialized and before conflict-key admission.
			// Marker ownership proves capture while t's row is invisible;
			// it does not claim to observe a native UniqueKey queue.
			fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
			r := fixture.LockResult(t, done)
			joined = true
			fixture.State(t, r.Err, "")
			fixture.LockAffected(t, r.Result, 1)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, conflictUpdateRows([]any{int64(1), int64(11)}))
		})
	})
	for _, level := range []string{"REPEATABLE READ", "SERIALIZABLE"} {
		for _, op := range []string{"UPDATE t SET v=20", "DELETE FROM t"} {
			t.Run(level+op, func(t *testing.T) {
				insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, c := fixture.LockConnections(t, p.Ctx, b.DB)
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(k INT UNIQUE,v INT)", 0)
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,10)", 1)
					fixture.LockExec(t, p.Ctx, c, "BEGIN ISOLATION LEVEL "+level, 0)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, conflictUpdateRows([]any{int64(1), int64(10)}))
					fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
					fixture.LockExec(t, p.Ctx, a, op, 1)
					ap, cp := 0, 0
					if b.PG {
						ap, cp = fixture.RowPIDs(t, p.Ctx, a, c)
					}
					done := fixture.RowAsync(t, p.Ctx, c, "INSERT INTO t VALUES(1,1) ON CONFLICT(k) DO UPDATE SET v=excluded.v WHERE FALSE", true, false)
					conflictUpdateWait(t, p, b, cp, ap)
					fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
					r := fixture.LockResult(t, done)
					if op == "DELETE FROM t" {
						// Index arbitration resumes after deletion and finds no hit:
						// this is a fresh insert, not TM_Deleted of a selected row.
						fixture.State(t, r.Err, "")
						fixture.LockAffected(t, r.Result, 1)
						// The retained snapshot still sees the deleted prior identity,
						// while own writes expose the newly inserted distinct identity.
						s := conflictUpdateRows([]any{int64(1), int64(10)}, []any{int64(1), int64(1)})
						s.Bag = true
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					} else {
						d := fixture.ControlsReferenceDiagnostic("40001", "could not serialize access due to concurrent update")
						fixture.AssertDiagnostic(t, r.Err, &d)
						if r.Result != nil {
							t.Fatal("serialization failure result", r.Result)
						}
					}
					fixture.LockExec(t, p.Ctx, c, "ROLLBACK", 0)
					if op == "DELETE FROM t" {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, conflictUpdateRows())
					}
				})
			})
		}
	}
	for _, level := range []string{"REPEATABLE READ", "SERIALIZABLE"} {
		for _, condition := range []string{"FALSE", "NULL"} {
			t.Run("selected_row_delete/"+level+"/"+condition, func(t *testing.T) {
				insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					// Root-authorized release interleaving witness: PostgreSQL exposes
					// real pending row acquisition through RowWait. Native has no
					// public pending-row observer; TestInsertConflictUpdateRowRecheck
					// must prove this exact ordering against its genuine manager.
					// Do not replace either barrier with sleep/start-channel timing.
					if !b.PG {
						return
					}
					a, c := fixture.LockConnections(t, p.Ctx, b.DB)
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(k INT UNIQUE,v INT)", 0)
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,10)", 1)
					fixture.LockExec(t, p.Ctx, c, "BEGIN ISOLATION LEVEL "+level, 0)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, conflictUpdateRows([]any{int64(1), int64(10)}))
					fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
					fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT v FROM t FOR UPDATE", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
					ap, cp := fixture.RowPIDs(t, p.Ctx, a, c)
					q := "INSERT INTO t VALUES(2,2),(1,1) ON CONFLICT(k) DO UPDATE SET v=excluded.v WHERE " + condition
					done := fixture.RowAsync(t, p.Ctx, c, q, true, false)
					conflictUpdateWait(t, p, b, cp, ap)
					// The hit has now been selected and is waiting on its actual row,
					// unlike the preceding deletion-before-index-probe cases.
					fixture.LockExec(t, p.Ctx, a, "DELETE FROM t WHERE k=1", 1)
					fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
					r := fixture.LockResult(t, done)
					d := fixture.ControlsReferenceDiagnostic("40001", "could not serialize access due to concurrent delete")
					fixture.AssertDiagnostic(t, r.Err, &d)
					if r.Result != nil {
						t.Fatal("selected-row deletion exposed success", r.Result)
					}
					fixture.LockExec(t, p.Ctx, c, "ROLLBACK", 0)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, conflictUpdateRows())
				})
			})
		}
	}
}
func TestReferenceInsertConflictUpdateRetainedLocks(t *testing.T) {
	for _, condition := range []string{"FALSE", "NULL"} {
		for _, set := range []string{"v=2", "k=t.k"} {
			t.Run(condition+set, func(t *testing.T) {
				insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, c := fixture.LockConnections(t, p.Ctx, b.DB)
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(k INT UNIQUE,v INT)", 0)
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,10)", 1)
					fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
					fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT v FROM t FOR UPDATE", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
					ap, cp := 0, 0
					if b.PG {
						ap, cp = fixture.RowPIDs(t, p.Ctx, a, c)
					}
					fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
					q := "INSERT INTO t VALUES(1,2) ON CONFLICT(k) DO UPDATE SET " + set + " WHERE " + condition
					done := fixture.RowAsync(t, p.Ctx, c, q, false, false)
					conflictUpdateWait(t, p, b, cp, ap)
					fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
					r := fixture.LockResult(t, done)
					fixture.State(t, r.Err, "")
					fixture.LockAffected(t, r.Result, 0)
					fixture.RunStep(t, p.Ctx, a, b.Recorder, conflictUpdateRows([]any{int64(1), int64(10)}))
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(9,9)", 1)
					blocked := fixture.RowAsync(t, p.Ctx, a, "UPDATE t SET v=11 WHERE k=1", false, false)
					conflictUpdateWait(t, p, b, ap, cp)
					fixture.LockExec(t, p.Ctx, c, "COMMIT", 0)
					r = fixture.LockResult(t, blocked)
					fixture.State(t, r.Err, "")
					fixture.LockAffected(t, r.Result, 1)
				})
			})
		}
	}
	for _, cse := range []struct {
		name, index, set string
		blocks           bool
	}{
		{"payload", "CREATE UNIQUE INDEX ui ON t(k)", "v=2", false},
		{"key_self", "CREATE UNIQUE INDEX ui ON t(k)", "k=t.k", true},
		{"partial_key", "CREATE UNIQUE INDEX ui ON t(k) WHERE v>0", "k=t.k", false},
		{"expression_key", "CREATE UNIQUE INDEX CONCURRENTLY ui ON t((k+1))", "k=t.k", false},
		{"include", "CREATE UNIQUE INDEX ui ON t(k) INCLUDE(v)", "v=2", false},
	} {
		t.Run(cse.name, func(t *testing.T) {
			insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, c := fixture.LockConnections(t, p.Ctx, b.DB)
				fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(k INT,v INT)", 0)
				fixture.LockExec(t, p.Ctx, a, cse.index, 0)
				fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,10)", 1)
				target := "(k)"
				if cse.name == "partial_key" {
					target = "(k) WHERE v>0"
				}
				if cse.name == "expression_key" {
					target = "((k+1))"
				}
				fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
				fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT v FROM t FOR KEY SHARE", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
				q := "INSERT INTO t VALUES(1,2) ON CONFLICT " + target + " DO UPDATE SET " + cse.set + " WHERE FALSE"
				if !cse.blocks {
					fixture.LockExec(t, p.Ctx, c, q, 0)
					fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
					return
				}
				ap, cp := 0, 0
				if b.PG {
					ap, cp = fixture.RowPIDs(t, p.Ctx, a, c)
				}
				done := fixture.RowAsync(t, p.Ctx, c, q, false, false)
				conflictUpdateWait(t, p, b, cp, ap)
				fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
				r := fixture.LockResult(t, done)
				fixture.State(t, r.Err, "")
				fixture.LockAffected(t, r.Result, 0)
			})
		})
	}
}
func TestReferenceInsertConflictUpdateLockOrdering(t *testing.T) {
	// Ordinary UPDATE holds row first. An upsert must not retain its candidate-new
	// unique claim while waiting for that row, or this holder cannot complete.
	insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, c := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(k INT UNIQUE,v INT)", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,0)", 1)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT v FROM t FOR UPDATE", []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}}))
		ap, cp := 0, 0
		if b.PG {
			ap, cp = fixture.RowPIDs(t, p.Ctx, a, c)
		}
		done := fixture.RowAsync(t, p.Ctx, c, "INSERT INTO t VALUES(1,1) ON CONFLICT(k) DO UPDATE SET v=t.v+excluded.v", true, false)
		conflictUpdateWait(t, p, b, cp, ap)
		fixture.LockExec(t, p.Ctx, a, "UPDATE t SET v=v+1", 1)
		fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
		r := fixture.LockResult(t, done)
		fixture.State(t, r.Err, "")
		fixture.LockAffected(t, r.Result, 1)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, conflictUpdateRows([]any{int64(1), int64(2)}))
	})
	t.Run("concurrent_increments", func(t *testing.T) {
		insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a, c := fixture.LockConnections(t, p.Ctx, b.DB)
			fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(k INT UNIQUE,v INT)", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,0)", 1)
			q := "INSERT INTO t VALUES(1,1) ON CONFLICT(k) DO UPDATE SET v=t.v+excluded.v"
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			fixture.RowAffected(t, p.Ctx, a, q, true, 1)
			ap, cp := 0, 0
			if b.PG {
				ap, cp = fixture.RowPIDs(t, p.Ctx, a, c)
			}
			done := fixture.RowAsync(t, p.Ctx, c, q, true, false)
			conflictUpdateWait(t, p, b, cp, ap)
			fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
			r := fixture.LockResult(t, done)
			fixture.State(t, r.Err, "")
			fixture.LockAffected(t, r.Result, 1)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, conflictUpdateRows([]any{int64(1), int64(2)}))
		})
	})
	t.Run("genuine_multirow_deadlock", func(t *testing.T) {
		insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a, c := fixture.LockConnections(t, p.Ctx, b.DB)
			fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(k INT UNIQUE,v INT)", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", 2)
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,1) ON CONFLICT(k) DO UPDATE SET v=t.v+excluded.v", 1)
			fixture.LockExec(t, p.Ctx, c, "INSERT INTO t VALUES(2,1) ON CONFLICT(k) DO UPDATE SET v=t.v+excluded.v", 1)
			ap, cp := 0, 0
			if b.PG {
				ap, cp = fixture.RowPIDs(t, p.Ctx, a, c)
			}
			var ax, cx string
			if b.PG {
				if e := a.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id()::text").Scan(&ax); e != nil {
					t.Fatal(e)
				}
				if e := c.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id()::text").Scan(&cx); e != nil {
					t.Fatal(e)
				}
			}
			da := fixture.RowAsync(t, p.Ctx, a, "INSERT INTO t VALUES(2,1) ON CONFLICT(k) DO UPDATE SET v=t.v+excluded.v", false, false)
			conflictUpdateWait(t, p, b, ap, cp)
			dc := fixture.RowAsync(t, p.Ctx, c, "INSERT INTO t VALUES(1,1) ON CONFLICT(k) DO UPDATE SET v=t.v+excluded.v", false, false)
			ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
			defer cancel()
			var ar, cr fixture.RelationOutcome
			gotA, gotC := false, false
			for !gotA || !gotC {
				select {
				case r := <-da:
					ar = r
					gotA = true
					da = nil
					end := "COMMIT"
					if r.Err != nil {
						end = "ROLLBACK"
					}
					fixture.LockExec(t, p.Ctx, a, end, 0)
				case r := <-dc:
					cr = r
					gotC = true
					dc = nil
					end := "COMMIT"
					if r.Err != nil {
						end = "ROLLBACK"
					}
					fixture.LockExec(t, p.Ctx, c, end, 0)
				case <-ctx.Done():
					t.Fatal("deadlock workers did not finish", ctx.Err())
				}
			}
			if (ar.Err == nil) == (cr.Err == nil) {
				t.Fatal("need one victim and one survivor", ar.Err, cr.Err)
			}
			victim, survivor := ar, cr
			if cr.Err != nil {
				victim, survivor = cr, ar
			}
			fixture.State(t, victim.Err, "40P01")
			if victim.Result != nil {
				t.Fatal("deadlock victim result", victim.Result)
			}
			fixture.LockAffected(t, survivor.Result, 1)
			if b.PG {
				want := fixture.ControlsReferenceDiagnostic("40P01", "deadlock detected")
				want.Hint = "See server log for query details."
				vp, bp, vx, bx, tuple := ap, cp, cx, ax, "(0,2)"
				if cr.Err != nil {
					vp, bp, vx, bx, tuple = cp, ap, ax, cx, "(0,1)"
				}
				want.Detail = fmt.Sprintf("Process %d waits for ShareLock on transaction %s; blocked by process %d.\nProcess %d waits for ShareLock on transaction %s; blocked by process %d.", vp, vx, bp, bp, bx, vp)
				want.Context = "while inserting index tuple " + tuple + " in relation \"t\""
				fixture.AssertDiagnostic(t, victim.Err, &want)
			} else {
				conflictUpdateNativeDeadlock(t, victim.Err)
			}
			fixture.RunStep(t, p.Ctx, a, b.Recorder, conflictUpdateRows([]any{int64(1), int64(11)}, []any{int64(2), int64(21)}))
		})
	})
}

func conflictUpdateNativeDeadlock(t *testing.T, err error) {
	t.Helper()
	d := fixture.ExtractDiagnostic(t, err)
	edges := strings.Split(d.Detail, "\n")
	if len(edges) != 2 {
		t.Fatal("need closed two-edge cycle", d)
	}
	pattern := regexp.MustCompile(`^Transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\) waits for (NoKeyUpdate|UniqueKey) on relation ([1-9][0-9]*) \(class 1259, sub-ID 0, epoch ([1-9][0-9]*)\)( row [1-9][0-9]*| unique key); blocked by transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\)\.$`)
	a, c := pattern.FindStringSubmatch(edges[0]), pattern.FindStringSubmatch(edges[1])
	if a == nil || c == nil || a[1] == c[1] || a[2] == c[2] || a[1] != c[8] || c[1] != a[8] || a[2] != c[9] || c[2] != a[9] || a[3] != c[10] || c[3] != a[10] || a[3] != a[6] || c[3] != c[6] {
		t.Fatal("not actual closed cycle", d)
	}
	for _, edge := range [][]string{a, c} {
		if (edge[4] == "UniqueKey") != (edge[7] == " unique key") {
			t.Fatal("mode/resource mismatch", d)
		}
	}
	d.Detail = ""
	want := fixture.ControlsReferenceDiagnostic("40P01", "deadlock detected")
	want.Hint = "See server log for query details."
	// Only opaque identities vary; the closed edges and every other portable
	// field have independently specified expectations.
	if d != want {
		t.Fatalf("deadlock diagnostic got %#v want %#v", d, want)
	}
}
