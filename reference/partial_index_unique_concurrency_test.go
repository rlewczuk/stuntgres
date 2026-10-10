package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
)

// Keep all physical owners borrowed until every worker has joined. The fixture
// registers rollback/close first; referenceLockOperation registers join last.
func partialUniqueLockSetup(t *testing.T, p *referencePair, b referenceBackend) (*sql.Conn, *sql.Conn, func(referenceStep)) {
	t.Helper()
	a, peer := referenceLockConnections(t, p.ctx, b.db)
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
	run(referenceExec("CREATE TABLE t(id INT,k INT,active BOOL)", 0))
	run(referencePrepared(referenceExec("CREATE UNIQUE INDEX pu ON t(k) WHERE active", 0)))
	return a, peer, run
}

func partialUniqueLockRows(ids []int64, keys []int64, active []any) referenceStep {
	rows := make([][]any, len(ids))
	for i := range ids {
		rows[i] = []any{ids[i], keys[i], active[i]}
	}
	return referenceSelect("SELECT id,k,active FROM t ORDER BY id", []string{"id", "k", "active"}, []string{"INT4", "INT4", "BOOL"}, rows)
}

func partialUniqueLockFailure(t *testing.T, o relationOutcome, want referenceDiagnostic) {
	t.Helper()
	if o.result != nil || o.rows != nil || o.stmt != nil {
		t.Fatal("failed command leaked result/cursor/statement", o)
	}
	referenceAssertDiagnostic(t, o.err, &want)
}

func partialUniqueLockTimeout(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, query, setting, code, message, pgContext string, held bool) {
	t.Helper()
	referenceLockExec(t, p.ctx, x, "SET "+setting+"='100ms'", 0)
	want := controlsReferenceDiagnostic(code, message)
	if b.pg {
		want.Context = pgContext
	}
	partialUniqueLockFailure(t, referenceRowExecute(p.ctx, x, query, held), want)
	referenceLockExec(t, p.ctx, x, "RESET "+setting, 0)
}

func TestReferencePartialIndexUniqueContenderOutcomes(t *testing.T) {
	for _, operation := range []string{"insert", "leave_false", "leave_null", "delete"} {
		for _, commit := range []bool{false, true} {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/commit=%t/held=%t", operation, commit, held), func(t *testing.T) {
					partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, _ func(referenceStep)) {
						a, peer, run := partialUniqueLockSetup(t, p, b)
						if operation != "insert" {
							run(referenceExec("INSERT INTO t VALUES(1,7,TRUE)", 1))
						}
						run(referenceExec("BEGIN", 0))
						switch operation {
						case "insert":
							run(referenceExec("INSERT INTO t VALUES(1,7,TRUE)", 1))
						case "leave_false":
							run(referenceExec("UPDATE t SET active=FALSE WHERE id=1", 1))
						case "leave_null":
							run(referenceExec("UPDATE t SET active=NULL WHERE id=1", 1))
						case "delete":
							run(referenceExec("DELETE FROM t WHERE id=1", 1))
						}
						// This same-key outsider finishes before A ends, not merely eventually.
						referenceLockExec(t, p.ctx, peer, "SET lock_timeout='100ms'", 0)
						referenceRunStep(t, p.ctx, peer, b.recorder, referencePrepared(referenceExec("INSERT INTO t VALUES(9,7,FALSE)", 1)))
						ids, keys, states := []int64{9}, []int64{7}, []any{false}
						if operation != "insert" {
							ids, keys, states = []int64{1, 9}, []int64{7, 7}, []any{true, false}
						}
						referenceRunStep(t, p.ctx, peer, b.recorder, partialUniqueLockRows(ids, keys, states))
						referenceLockExec(t, p.ctx, peer, "RESET lock_timeout", 0)
						var ap, bp int
						if b.pg {
							ap = referenceLockPID(t, p.ctx, a)
							bp = referenceLockPID(t, p.ctx, peer)
						} else {
							partialUniqueLockTimeout(t, p, b, peer, "INSERT INTO t VALUES(2,7,TRUE)", "lock_timeout", "55P03", "canceling statement due to lock timeout", "", held)
						}
						await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
							return referenceRowExecute(ctx, peer, "INSERT INTO t VALUES(2,7,TRUE)", held)
						})
						if b.pg {
							referenceRowWait(t, p.ctx, bp, ap)
						}
						end := "ROLLBACK"
						if commit {
							end = "COMMIT"
						}
						run(referenceExec(end, 0))
						o := await()
						collision := (operation == "insert") == commit
						if collision {
							partialUniqueLockFailure(t, o, uniqueReferenceDuplicate("t", "pu", "Key (k)=(7) already exists."))
						} else {
							referenceState(t, o.err, "")
							referenceLockAffected(t, o.result, 1)
						}
						ids, keys, states = []int64{2, 9}, []int64{7, 7}, []any{true, false}
						if collision {
							ids, keys, states = []int64{1, 9}, []int64{7, 7}, []any{true, false}
						}
						if commit && operation == "leave_false" {
							ids, keys, states = []int64{1, 2, 9}, []int64{7, 7, 7}, []any{false, true, false}
						}
						if commit && operation == "leave_null" {
							ids, keys, states = []int64{1, 2, 9}, []int64{7, 7, 7}, []any{nil, true, false}
						}
						run(partialUniqueLockRows(ids, keys, states))
						run(referenceExec("BEGIN", 0))
						run(referenceExec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
						run(referenceExec("ROLLBACK", 0))
					})
				})
			}
		}
	}
}

func TestReferencePartialIndexUniqueOutsiderReservations(t *testing.T) {
	for _, operation := range []string{"insert", "update", "delete"} {
		for _, excluded := range []string{"FALSE", "NULL"} {
			t.Run(operation+"/"+excluded, func(t *testing.T) {
				partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, _ func(referenceStep)) {
					_, peer, run := partialUniqueLockSetup(t, p, b)
					if operation != "insert" {
						run(referenceExec("INSERT INTO t VALUES(1,7,"+excluded+")", 1))
					}
					run(referenceExec("BEGIN", 0))
					switch operation {
					case "insert":
						run(referenceExec("INSERT INTO t VALUES(1,7,"+excluded+")", 1))
					case "update":
						run(referenceExec("UPDATE t SET k=8 WHERE id=1", 1))
					case "delete":
						run(referenceExec("DELETE FROM t WHERE id=1", 1))
					}
					// Both member and outsider finish under a short SQL wait bound,
					// while the excluded old/new image owner remains uncommitted.
					referenceLockExec(t, p.ctx, peer, "SET lock_timeout='100ms'", 0)
					referenceRunStep(t, p.ctx, peer, b.recorder, referencePrepared(referenceExec("INSERT INTO t VALUES(2,7,TRUE),(3,7,FALSE),(4,8,TRUE),(5,8,NULL)", 4)))
					run(referenceExec("ROLLBACK", 0))
					ids, keys, states := []int64{2, 3, 4, 5}, []int64{7, 7, 8, 8}, []any{true, false, true, nil}
					if operation != "insert" {
						state := any(false)
						if excluded == "NULL" {
							state = nil
						}
						ids, keys, states = []int64{1, 2, 3, 4, 5}, []int64{7, 7, 7, 8, 8}, []any{state, true, false, true, nil}
					}
					run(partialUniqueLockRows(ids, keys, states))
					referenceLockExec(t, p.ctx, peer, "RESET lock_timeout", 0)
				})
			})
		}
	}
}

func TestReferencePartialIndexUniqueSavepointClaims(t *testing.T) {
	partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, _ func(referenceStep)) {
		_, peer, run := partialUniqueLockSetup(t, p, b)
		run(referenceExec("BEGIN", 0))
		run(referenceExec("INSERT INTO t VALUES(1,7,TRUE)", 1))
		run(referenceExec("SAVEPOINT m", 0))
		run(referencePrepared(referenceExec("INSERT INTO t VALUES(2,8,TRUE),(3,7,FALSE)", 2)))
		partialUniqueLockTimeout(t, p, b, peer, "INSERT INTO t VALUES(4,8,TRUE)", "lock_timeout", "55P03", "canceling statement due to lock timeout", `while inserting index tuple (0,4) in relation "pu"`, true)
		run(referenceExec("ROLLBACK TO m", 0))
		referenceRunStep(t, p.ctx, peer, b.recorder, referencePrepared(referenceExec("INSERT INTO t VALUES(4,8,TRUE)", 1)))
		partialUniqueLockTimeout(t, p, b, peer, "INSERT INTO t VALUES(5,7,TRUE)", "lock_timeout", "55P03", "canceling statement due to lock timeout", `while inserting index tuple (0,6) in relation "pu"`, false)
		run(partialUniqueLockRows([]int64{1, 4}, []int64{7, 8}, []any{true, true}))
		run(referenceExec("COMMIT", 0))
		referenceRunStep(t, p.ctx, peer, b.recorder, referencePrepared(referenceErrorDiagnostic("INSERT INTO t VALUES(5,7,TRUE)", uniqueReferenceDuplicate("t", "pu", "Key (k)=(7) already exists."))))
		run(partialUniqueLockRows([]int64{1, 4}, []int64{7, 8}, []any{true, true}))
		// A's premark claim remains authoritative after postmark error recovery.
		run(referenceExec("BEGIN", 0))
		run(referenceExec("SAVEPOINT n", 0))
		run(referenceErrorDiagnostic("INSERT INTO t VALUES(6,8,TRUE)", uniqueReferenceDuplicate("t", "pu", "Key (k)=(8) already exists.")))
		run(referenceExec("ROLLBACK TO n", 0))
		run(referenceExec("INSERT INTO t VALUES(6,9,TRUE)", 1))
		run(referenceExec("COMMIT", 0))
		run(partialUniqueLockRows([]int64{1, 4, 6}, []int64{7, 8, 9}, []any{true, true, true}))
	})
}

func TestReferencePartialIndexUniqueCanceledAndTimedClaims(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, mode := range []string{"lock_timeout", "statement_timeout", "cancel"} {
			t.Run(fmt.Sprintf("%s/held=%t", mode, held), func(t *testing.T) {
				partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, _ func(referenceStep)) {
					a, peer, run := partialUniqueLockSetup(t, p, b)
					run(referenceExec("BEGIN", 0))
					run(referenceExec("INSERT INTO t VALUES(1,7,TRUE)", 1))
					const query = "INSERT INTO t VALUES(2,7,TRUE)"
					if mode == "cancel" {
						if b.pg {
							ap, bp := referenceLockPID(t, p.ctx, a), referenceLockPID(t, p.ctx, peer)
							await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome { return referenceRowExecute(ctx, peer, query, held) })
							referenceRowWait(t, p.ctx, bp, ap)
							var ok bool
							if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&ok); err != nil || !ok {
								t.Fatal(ok, err)
							}
							want := controlsReferenceDiagnostic("57014", "canceling statement due to user request")
							want.Context = `while inserting index tuple (0,2) in relation "pu"`
							partialUniqueLockFailure(t, await(), want)
						} else {
							ctx, cancel := context.WithTimeout(p.ctx, 100*time.Millisecond)
							o := referenceRowExecute(ctx, peer, query, held)
							cancel()
							if o.result != nil || o.rows != nil || o.stmt != nil || !errors.Is(o.err, context.DeadlineExceeded) {
								t.Fatal(o)
							}
							var state interface{ SQLState() string }
							if errors.As(o.err, &state) {
								t.Fatal("caller deadline fabricated SQLSTATE", o.err)
							}
						}
					} else {
						code, message := "55P03", "canceling statement due to lock timeout"
						if mode == "statement_timeout" {
							code, message = "57014", "canceling statement due to statement timeout"
						}
						partialUniqueLockTimeout(t, p, b, peer, query, mode, code, message, `while inserting index tuple (0,2) in relation "pu"`, held)
					}
					// Same borrowed peer admits an unrelated member before holder ends.
					referenceRunStep(t, p.ctx, peer, b.recorder, referencePrepared(referenceExec("INSERT INTO t VALUES(3,8,TRUE)", 1)))
					run(referenceExec("ROLLBACK", 0))
					referenceRunStep(t, p.ctx, peer, b.recorder, referencePrepared(referenceExec(query, 1)))
					run(partialUniqueLockRows([]int64{2, 3}, []int64{7, 8}, []any{true, true}))
					run(referenceExec("BEGIN", 0))
					run(referenceExec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
					run(referenceExec("ROLLBACK", 0))
				})
			})
		}
	}
}

func TestReferencePartialIndexUniqueKeyDeadlock(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%t", held), func(t *testing.T) {
			partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, _ func(referenceStep)) {
				a, peer, run := partialUniqueLockSetup(t, p, b)
				namespace := uniqueReferencePublicNamespace(t, p.ctx, b.db, b.pg)
				var indexOID int64
				if err := a.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='pu' AND relnamespace=$1", namespace).Scan(&indexOID); err != nil || indexOID <= 0 {
					t.Fatal(indexOID, err)
				}
				if b.pg {
					referenceLockExec(t, p.ctx, a, "SET deadlock_timeout='2s'", 0)
					referenceLockExec(t, p.ctx, peer, "SET deadlock_timeout='100ms'", 0)
				}
				run(referenceExec("BEGIN", 0))
				referenceLockExec(t, p.ctx, peer, "BEGIN", 0)
				run(referenceExec("INSERT INTO t VALUES(1,7,TRUE)", 1))
				referenceRunStep(t, p.ctx, peer, b.recorder, referenceExec("INSERT INTO t VALUES(2,8,TRUE)", 1))
				var ap, bp int
				var ax, bx string
				if b.pg {
					ap, bp = referenceLockPID(t, p.ctx, a), referenceLockPID(t, p.ctx, peer)
					if err := a.QueryRowContext(p.ctx, "SELECT pg_current_xact_id()::text").Scan(&ax); err != nil {
						t.Fatal(err)
					}
					if err := peer.QueryRowContext(p.ctx, "SELECT pg_current_xact_id()::text").Scan(&bx); err != nil {
						t.Fatal(err)
					}
				} else {
					// Prove the first edge without losing A's premark claim.
					run(referenceExec("SAVEPOINT admission", 0))
					referenceLockExec(t, p.ctx, a, "SET LOCAL lock_timeout='100ms'", 0)
					partialUniqueLockFailure(t, referenceRowExecute(p.ctx, a, "INSERT INTO t VALUES(3,8,TRUE)", held), controlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout"))
					run(referenceExec("ROLLBACK TO admission", 0))
					run(referenceExec("RELEASE SAVEPOINT admission", 0))
					run(referenceExec("RESET lock_timeout", 0))
				}
				first := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
					return referenceRowExecute(ctx, a, "INSERT INTO t VALUES(3,8,TRUE)", held)
				})
				if b.pg {
					referenceRowWait(t, p.ctx, ap, bp)
				}
				second := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
					return referenceRowExecute(ctx, peer, "INSERT INTO t VALUES(4,7,TRUE)", held)
				})
				// Join both. Which worker reports first is never victim identity.
				outcomes := []relationOutcome{first(), second()}
				victim := -1
				for i, o := range outcomes {
					if o.err == nil {
						referenceLockAffected(t, o.result, 1)
						continue
					}
					if victim != -1 {
						t.Fatal("both participants failed", outcomes)
					}
					victim = i
					if o.result != nil || o.rows != nil || o.stmt != nil {
						t.Fatal("victim published resources", o)
					}
					if b.pg {
						vp, hp, vx, hx, slot := ap, bp, ax, bx, 3
						if i == 1 {
							vp, hp, vx, hx, slot = bp, ap, bx, ax, 4
						}
						want := controlsReferenceDiagnostic("40P01", "deadlock detected")
						want.Hint = "See server log for query details."
						want.Detail = fmt.Sprintf("Process %d waits for ShareLock on transaction %s; blocked by process %d.\nProcess %d waits for ShareLock on transaction %s; blocked by process %d.", vp, hx, hp, hp, vx, vp)
						want.Context = fmt.Sprintf("while inserting index tuple (0,%d) in relation \"pu\"", slot)
						referenceAssertDiagnostic(t, o.err, &want)
					} else {
						uniqueReferenceNativeKeyDeadlock(t, o.err, indexOID)
					}
				}
				if victim < 0 {
					t.Fatal("opposite member claims did not deadlock")
				}
				conns := []referenceExecutor{a, peer}
				referenceRunStep(t, p.ctx, conns[victim], b.recorder, referenceImplicitError("INSERT INTO t VALUES(5,9,TRUE)", referenceTransactionAborted()))
				referenceLockExec(t, p.ctx, conns[victim], "ROLLBACK", 0)
				referenceLockExec(t, p.ctx, conns[1-victim], "COMMIT", 0)
				ids := []int64{1, 3}
				keys := []int64{7, 8}
				if victim == 0 {
					ids, keys = []int64{2, 4}, []int64{8, 7}
				}
				run(partialUniqueLockRows(ids, keys, []any{true, true}))
				// Reuse the exact victim session, then prove no relation holds leaked.
				referenceRunStep(t, p.ctx, conns[victim], b.recorder, referencePrepared(referenceExec("INSERT INTO t VALUES(5,9,TRUE)", 1)))
				run(partialUniqueLockRows(append(ids, 5), append(keys, 9), []any{true, true, true}))
				run(referenceExec("BEGIN", 0))
				run(referenceExec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
				run(referenceExec("ROLLBACK", 0))
			})
		})
	}
}
