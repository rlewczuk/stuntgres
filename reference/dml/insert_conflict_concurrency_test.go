package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceInsertConflictContention(t *testing.T) {
	for _, op := range []string{"insert", "delete", "update"} {
		for _, commit := range []bool{false, true} {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/commit=%t/held=%t", op, commit, held), func(t *testing.T) {
					insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						a, c := fixture.LockConnections(t, p.Ctx, b.DB)
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
						run(fixture.Exec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE)", 0))
						if op != "insert" {
							run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
						}
						q := "INSERT INTO t VALUES(1) ON CONFLICT(k) DO NOTHING"
						var x fixture.Executor = c
						if held {
							stmt, err := c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
						}
						run(fixture.Exec("BEGIN", 0))
						switch op {
						case "insert":
							run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
						case "delete":
							run(fixture.Exec("DELETE FROM t", 1))
						case "update":
							run(fixture.Exec("UPDATE t SET k=2", 1))
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO t VALUES(9) ON CONFLICT DO NOTHING", 1))
						rows := [][]any{{int64(9)}}
						if op != "insert" {
							rows = [][]any{{int64(1)}, {int64(9)}}
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT k FROM t ORDER BY k", []string{"k"}, []string{"INT4"}, rows))
						if !b.PG {
							fixture.LockExec(t, p.Ctx, c, "SET lock_timeout='10ms'", 0)
							want := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
							fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.ErrorDiagnostic(q, want))
							fixture.LockExec(t, p.Ctx, c, "SET lock_timeout=0", 0)
						}
						var ap, cp int
						if b.PG {
							ap, cp = fixture.RowPIDs(t, p.Ctx, a, c)
						}
						done := fixture.RowAsync(t, p.Ctx, x, q, false, false)
						if b.PG {
							fixture.RowWait(t, p.Ctx, cp, ap)
						}
						end := "ROLLBACK"
						if commit {
							end = "COMMIT"
						}
						run(fixture.Exec(end, 0))
						result := fixture.LockResult(t, done)
						fixture.State(t, result.Err, "")
						count := int64(1)
						if (op == "insert") == commit {
							count = 0
						}
						fixture.LockAffected(t, result.Result, count)
						final := [][]any{{int64(1)}, {int64(9)}}
						if op == "update" && commit {
							final = [][]any{{int64(1)}, {int64(2)}, {int64(9)}}
						}
						run(fixture.Select("SELECT k FROM t ORDER BY k", []string{"k"}, []string{"INT4"}, final))
					})
				})
			}
		}
	}
}
func TestReferenceInsertConflictConcurrentSameKey(t *testing.T) {
	insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, c := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(k INT UNIQUE)", 0)
		q := "INSERT INTO t VALUES(1) ON CONFLICT DO NOTHING"
		sa, err := a.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer sa.Close()
		sc, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer sc.Close()
		start := make(chan struct{})
		done := make(chan fixture.RelationOutcome, 2)
		ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
		defer cancel()
		for _, stmt := range []*sql.Stmt{sa, sc} {
			go func(stmt *sql.Stmt) {
				<-start
				r, e := stmt.ExecContext(ctx)
				done <- fixture.RelationOutcome{Result: r, Err: e}
			}(stmt)
		}
		remaining := 2
		t.Cleanup(func() {
			cancel()
			for remaining > 0 {
				select {
				case <-done:
					remaining--
				case <-time.After(5 * time.Second):
					t.Error("same-key worker not joined")
					return
				}
			}
		})
		close(start)
		var total int64
		for remaining > 0 {
			select {
			case r := <-done:
				remaining--
				if r.Err != nil {
					t.Fatal(r.Err)
				}
				n, e := r.Result.RowsAffected()
				if e != nil {
					t.Fatal(e)
				}
				total += n
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		if total != 1 {
			t.Fatal("affected sum", total)
		}
		fixture.RunStep(t, p.Ctx, a, b.Recorder, insertConflictRows("SELECT k FROM t", []any{int64(1)}))
	})
}
func TestReferenceInsertConflictSnapshotVisibility(t *testing.T) {
	for _, level := range []string{"READ COMMITTED", "REPEATABLE READ", "SERIALIZABLE"} {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/held=%t", level, held), func(t *testing.T) {
				insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, c := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE)", 0))
					query := "INSERT INTO t VALUES(1) ON CONFLICT(k) DO NOTHING"
					var x fixture.Executor = a
					if held {
						stmt, err := a.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						x = fixture.HeldStatement{Executor: a, Statement: stmt, SQL: query}
					}
					run(fixture.Exec("BEGIN ISOLATION LEVEL "+level, 0))
					run(insertConflictRows("SELECT k FROM t"))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO t VALUES(1)", 1))
					if level == "READ COMMITTED" {
						fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(query, 0))
					} else {
						run(insertConflictRows("SELECT k FROM t"))
						fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("40001", "could not serialize access due to concurrent update")))
					}
					run(fixture.Exec("ROLLBACK", 0))
					run(insertConflictRows("SELECT k FROM t", []any{int64(1)}))
					run(fixture.Exec("BEGIN ISOLATION LEVEL "+level, 0))
					run(fixture.Exec("INSERT INTO t VALUES(2)", 1))
					run(fixture.Exec("INSERT INTO t VALUES(2),(3),(3) ON CONFLICT DO NOTHING", 1))
					run(fixture.Exec("COMMIT", 0))
					run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.UniqueReferenceDuplicate("t", "u", "Key (k)=(1) already exists.")))
					run(insertConflictRows("SELECT k FROM t ORDER BY k", []any{int64(1)}, []any{int64(2)}, []any{int64(3)}))
				})
			})
		}
	}
}
func TestReferenceInsertConflictCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, c := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(k INT UNIQUE)", 0))
				fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
				holderReleased := false
				defer func() {
					if holderReleased {
						return
					}
					cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					if _, err := a.ExecContext(cleanup, "ROLLBACK"); err != nil {
						t.Error(err)
					}
				}()
				fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(2)", 1)
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SAVEPOINT recover", 0))
				query := "INSERT INTO t VALUES(1),(2) ON CONFLICT DO NOTHING"
				var x fixture.Executor = c
				if prepared {
					stmt, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				if !b.PG {
					cancel()
					ctx, cancel = context.WithTimeout(p.Ctx, 100*time.Millisecond)
				}
				defer cancel()
				waitContext, stopWait := context.WithTimeout(p.Ctx, 5*time.Second)
				defer stopWait()
				var ap, cp int
				if b.PG {
					ap, cp = fixture.RowPIDs(t, p.Ctx, a, c)
				}
				done := fixture.RowAsync(t, ctx, x, query, false, false)
				if b.PG {
					fixture.RowWait(t, waitContext, cp, ap)
					var canceled bool
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", cp).Scan(&canceled); err != nil || !canceled {
						t.Fatal(canceled, err)
					}
				}
				result := fixture.LockResult(t, done)
				if result.Err == nil || result.Result != nil {
					t.Fatal("canceled command exposed success", result)
				}
				if b.PG {
					want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
					want.Context = "while inserting index tuple (0,1) in relation \"t\""
					fixture.AssertDiagnostic(t, result.Err, &want)
				} else {
					if !errors.Is(result.Err, context.DeadlineExceeded) {
						t.Fatal(result.Err)
					}
					var state interface{ SQLState() string }
					if errors.As(result.Err, &state) {
						t.Fatal("native caller cancellation fabricated SQLSTATE", result.Err)
					}
				}
				run(fixture.Exec("ROLLBACK TO SAVEPOINT recover", 0))
				run(insertConflictRows("SELECT k FROM t"))
				fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
				holderReleased = true
				fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(query, 2))
				run(fixture.Exec("COMMIT", 0))
				run(insertConflictRows("SELECT k FROM t ORDER BY k", []any{int64(1)}, []any{int64(2)}))
			})
		})
	}
}
func TestReferenceInsertConflictOmittedReservations(t *testing.T) {
	for _, op := range []string{"DELETE FROM t WHERE k=1", "UPDATE t SET k=3 WHERE k=1"} {
		t.Run(op, func(t *testing.T) {
			insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, c := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(k INT UNIQUE)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("INSERT INTO t VALUES(2) ON CONFLICT DO NOTHING", 1))
				run(fixture.Exec("INSERT INTO t VALUES(1),(2) ON CONFLICT DO NOTHING", 0))
				fixture.LockExec(t, p.Ctx, c, "SET lock_timeout='10ms'", 0)
				fixture.LockExec(t, p.Ctx, c, op, 1)
				want := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
				s := fixture.ErrorDiagnostic("INSERT INTO t VALUES(2) ON CONFLICT DO NOTHING", want)
				pgWant := want
				pgWant.Context = "while inserting index tuple (0,2) in relation \"t\""
				s.PostgresDiagnostic = &pgWant
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				run(fixture.Exec("COMMIT", 0))
				fixture.LockExec(t, p.Ctx, c, "SET lock_timeout=0", 0)
			})
		})
	}
	t.Run("row_lock", func(t *testing.T) {
		insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a, c := fixture.LockConnections(t, p.Ctx, b.DB)
			fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(k INT UNIQUE)", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1)", 1)
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT k FROM t FOR UPDATE", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
			fixture.LockExec(t, p.Ctx, c, "SET lock_timeout='10ms'", 0)
			fixture.LockExec(t, p.Ctx, c, "INSERT INTO t VALUES(1) ON CONFLICT DO NOTHING", 0)
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		})
	})
}
