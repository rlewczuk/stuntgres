package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestReferenceInsertConflictContention(t *testing.T) {
	for _, op := range []string{"insert", "delete", "update"} {
		for _, commit := range []bool{false, true} {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/commit=%t/held=%t", op, commit, held), func(t *testing.T) {
					insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
						a, c := referenceLockConnections(t, p.ctx, b.db)
						run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
						run(referenceExec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE)", 0))
						if op != "insert" {
							run(referenceExec("INSERT INTO t VALUES(1)", 1))
						}
						q := "INSERT INTO t VALUES(1) ON CONFLICT(k) DO NOTHING"
						var x referenceExecutor = c
						if held {
							stmt, err := c.PrepareContext(p.ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							x = referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
						}
						run(referenceExec("BEGIN", 0))
						switch op {
						case "insert":
							run(referenceExec("INSERT INTO t VALUES(1)", 1))
						case "delete":
							run(referenceExec("DELETE FROM t", 1))
						case "update":
							run(referenceExec("UPDATE t SET k=2", 1))
						}
						referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO t VALUES(9) ON CONFLICT DO NOTHING", 1))
						rows := [][]any{{int64(9)}}
						if op != "insert" {
							rows = [][]any{{int64(1)}, {int64(9)}}
						}
						referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT k FROM t ORDER BY k", []string{"k"}, []string{"INT4"}, rows))
						if !b.pg {
							referenceLockExec(t, p.ctx, c, "SET lock_timeout='100ms'", 0)
							want := controlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
							referenceRunStep(t, p.ctx, x, b.recorder, referenceErrorDiagnostic(q, want))
							referenceLockExec(t, p.ctx, c, "SET lock_timeout=0", 0)
						}
						var ap, cp int
						if b.pg {
							ap, cp = referenceRowPIDs(t, p.ctx, a, c)
						}
						done := referenceRowAsync(t, p.ctx, x, q, false, false)
						if b.pg {
							referenceRowWait(t, p.ctx, cp, ap)
						}
						end := "ROLLBACK"
						if commit {
							end = "COMMIT"
						}
						run(referenceExec(end, 0))
						result := referenceLockResult(t, done)
						referenceState(t, result.err, "")
						count := int64(1)
						if (op == "insert") == commit {
							count = 0
						}
						referenceLockAffected(t, result.result, count)
						final := [][]any{{int64(1)}, {int64(9)}}
						if op == "update" && commit {
							final = [][]any{{int64(1)}, {int64(2)}, {int64(9)}}
						}
						run(referenceSelect("SELECT k FROM t ORDER BY k", []string{"k"}, []string{"INT4"}, final))
					})
				})
			}
		}
	}
}
func TestReferenceInsertConflictConcurrentSameKey(t *testing.T) {
	insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, c := referenceLockConnections(t, p.ctx, b.db)
		referenceLockExec(t, p.ctx, a, "CREATE TABLE t(k INT UNIQUE)", 0)
		q := "INSERT INTO t VALUES(1) ON CONFLICT DO NOTHING"
		sa, err := a.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer sa.Close()
		sc, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer sc.Close()
		start := make(chan struct{})
		done := make(chan relationOutcome, 2)
		ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
		defer cancel()
		for _, stmt := range []*sql.Stmt{sa, sc} {
			go func(stmt *sql.Stmt) {
				<-start
				r, e := stmt.ExecContext(ctx)
				done <- relationOutcome{result: r, err: e}
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
				if r.err != nil {
					t.Fatal(r.err)
				}
				n, e := r.result.RowsAffected()
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
		referenceRunStep(t, p.ctx, a, b.recorder, insertConflictRows("SELECT k FROM t", []any{int64(1)}))
	})
}
func TestReferenceInsertConflictSnapshotVisibility(t *testing.T) {
	for _, level := range []string{"READ COMMITTED", "REPEATABLE READ", "SERIALIZABLE"} {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/held=%t", level, held), func(t *testing.T) {
				insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					a, c := referenceLockConnections(t, p.ctx, b.db)
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
					run(referenceExec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE)", 0))
					query := "INSERT INTO t VALUES(1) ON CONFLICT(k) DO NOTHING"
					var x referenceExecutor = a
					if held {
						stmt, err := a.PrepareContext(p.ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						x = referenceHeldStatement{referenceExecutor: a, statement: stmt, query: query}
					}
					run(referenceExec("BEGIN ISOLATION LEVEL "+level, 0))
					run(insertConflictRows("SELECT k FROM t"))
					referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO t VALUES(1)", 1))
					if level == "READ COMMITTED" {
						referenceRunStep(t, p.ctx, x, b.recorder, referenceExec(query, 0))
					} else {
						run(insertConflictRows("SELECT k FROM t"))
						referenceRunStep(t, p.ctx, x, b.recorder, referenceErrorDiagnostic(query, controlsReferenceDiagnostic("40001", "could not serialize access due to concurrent update")))
					}
					run(referenceExec("ROLLBACK", 0))
					run(insertConflictRows("SELECT k FROM t", []any{int64(1)}))
					run(referenceExec("BEGIN ISOLATION LEVEL "+level, 0))
					run(referenceExec("INSERT INTO t VALUES(2)", 1))
					run(referenceExec("INSERT INTO t VALUES(2),(3),(3) ON CONFLICT DO NOTHING", 1))
					run(referenceExec("COMMIT", 0))
					run(referenceErrorDiagnostic("INSERT INTO t VALUES(1)", uniqueReferenceDuplicate("t", "u", "Key (k)=(1) already exists.")))
					run(insertConflictRows("SELECT k FROM t ORDER BY k", []any{int64(1)}, []any{int64(2)}, []any{int64(3)}))
				})
			})
		}
	}
}
func TestReferenceInsertConflictCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, c := referenceLockConnections(t, p.ctx, b.db)
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
				run(referenceExec("CREATE TABLE t(k INT UNIQUE)", 0))
				referenceLockExec(t, p.ctx, a, "BEGIN", 0)
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
				referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(2)", 1)
				run(referenceExec("BEGIN", 0))
				run(referenceExec("SAVEPOINT recover", 0))
				query := "INSERT INTO t VALUES(1),(2) ON CONFLICT DO NOTHING"
				var x referenceExecutor = c
				if prepared {
					stmt, err := c.PrepareContext(p.ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					x = referenceHeldStatement{referenceExecutor: c, statement: stmt, query: query}
				}
				ctx, cancel := context.WithCancel(p.ctx)
				if !b.pg {
					cancel()
					ctx, cancel = context.WithTimeout(p.ctx, 100*time.Millisecond)
				}
				defer cancel()
				waitContext, stopWait := context.WithTimeout(p.ctx, 5*time.Second)
				defer stopWait()
				var ap, cp int
				if b.pg {
					ap, cp = referenceRowPIDs(t, p.ctx, a, c)
				}
				done := referenceRowAsync(t, ctx, x, query, false, false)
				if b.pg {
					referenceRowWait(t, waitContext, cp, ap)
					var canceled bool
					if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", cp).Scan(&canceled); err != nil || !canceled {
						t.Fatal(canceled, err)
					}
				}
				result := referenceLockResult(t, done)
				if result.err == nil || result.result != nil {
					t.Fatal("canceled command exposed success", result)
				}
				if b.pg {
					want := controlsReferenceDiagnostic("57014", "canceling statement due to user request")
					want.Context = "while inserting index tuple (0,1) in relation \"t\""
					referenceAssertDiagnostic(t, result.err, &want)
				} else {
					if !errors.Is(result.err, context.DeadlineExceeded) {
						t.Fatal(result.err)
					}
					var state interface{ SQLState() string }
					if errors.As(result.err, &state) {
						t.Fatal("native caller cancellation fabricated SQLSTATE", result.err)
					}
				}
				run(referenceExec("ROLLBACK TO SAVEPOINT recover", 0))
				run(insertConflictRows("SELECT k FROM t"))
				referenceLockExec(t, p.ctx, a, "ROLLBACK", 0)
				holderReleased = true
				referenceRunStep(t, p.ctx, x, b.recorder, referenceExec(query, 2))
				run(referenceExec("COMMIT", 0))
				run(insertConflictRows("SELECT k FROM t ORDER BY k", []any{int64(1)}, []any{int64(2)}))
			})
		})
	}
}
func TestReferenceInsertConflictOmittedReservations(t *testing.T) {
	for _, op := range []string{"DELETE FROM t WHERE k=1", "UPDATE t SET k=3 WHERE k=1"} {
		t.Run(op, func(t *testing.T) {
			insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, c := referenceLockConnections(t, p.ctx, b.db)
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
				run(referenceExec("CREATE TABLE t(k INT UNIQUE)", 0))
				run(referenceExec("INSERT INTO t VALUES(1)", 1))
				run(referenceExec("BEGIN", 0))
				run(referenceExec("INSERT INTO t VALUES(2) ON CONFLICT DO NOTHING", 1))
				run(referenceExec("INSERT INTO t VALUES(1),(2) ON CONFLICT DO NOTHING", 0))
				referenceLockExec(t, p.ctx, c, "SET lock_timeout='100ms'", 0)
				referenceLockExec(t, p.ctx, c, op, 1)
				want := controlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
				s := referenceErrorDiagnostic("INSERT INTO t VALUES(2) ON CONFLICT DO NOTHING", want)
				pgWant := want
				pgWant.Context = "while inserting index tuple (0,2) in relation \"t\""
				s.postgresDiagnostic = &pgWant
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, c, b.recorder, s)
				run(referenceExec("COMMIT", 0))
				referenceLockExec(t, p.ctx, c, "SET lock_timeout=0", 0)
			})
		})
	}
	t.Run("row_lock", func(t *testing.T) {
		insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
			a, c := referenceLockConnections(t, p.ctx, b.db)
			referenceLockExec(t, p.ctx, a, "CREATE TABLE t(k INT UNIQUE)", 0)
			referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1)", 1)
			referenceLockExec(t, p.ctx, a, "BEGIN", 0)
			referenceRunStep(t, p.ctx, a, b.recorder, referenceSelect("SELECT k FROM t FOR UPDATE", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
			referenceLockExec(t, p.ctx, c, "SET lock_timeout='100ms'", 0)
			referenceLockExec(t, p.ctx, c, "INSERT INTO t VALUES(1) ON CONFLICT DO NOTHING", 0)
			referenceLockExec(t, p.ctx, a, "ROLLBACK", 0)
		})
	})
}
