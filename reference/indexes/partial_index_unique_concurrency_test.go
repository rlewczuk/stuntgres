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

// Keep all physical owners borrowed until every worker has joined. The fixture
// registers rollback/close first; referenceLockOperation registers join last.
func partialUniqueLockSetup(t *testing.T, p *fixture.Pair, b fixture.Backend) (*sql.Conn, *sql.Conn, func(fixture.Step)) {
	t.Helper()
	a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE t(id INT,k INT,active BOOL)", 0))
	run(fixture.Prepared(fixture.Exec("CREATE UNIQUE INDEX pu ON t(k) WHERE active", 0)))
	return a, peer, run
}

func partialUniqueLockRows(ids []int64, keys []int64, active []any) fixture.Step {
	rows := make([][]any, len(ids))
	for i := range ids {
		rows[i] = []any{ids[i], keys[i], active[i]}
	}
	return fixture.Select("SELECT id,k,active FROM t ORDER BY id", []string{"id", "k", "active"}, []string{"INT4", "INT4", "BOOL"}, rows)
}

func partialUniqueLockFailure(t *testing.T, o fixture.RelationOutcome, want fixture.Diagnostic) {
	t.Helper()
	if o.Result != nil || o.Rows != nil || o.Stmt != nil {
		t.Fatal("failed command leaked result/cursor/statement", o)
	}
	fixture.AssertDiagnostic(t, o.Err, &want)
}

func partialUniqueLockTimeout(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, query, setting, code, message, pgContext string, held bool) {
	t.Helper()
	fixture.LockExec(t, p.Ctx, x, "SET "+setting+"='100ms'", 0)
	want := fixture.ControlsReferenceDiagnostic(code, message)
	if b.PG {
		want.Context = pgContext
	}
	partialUniqueLockFailure(t, fixture.RowExecute(p.Ctx, x, query, held), want)
	fixture.LockExec(t, p.Ctx, x, "RESET "+setting, 0)
}

func TestReferencePartialIndexUniqueContenderOutcomes(t *testing.T) {
	for _, operation := range []string{"insert", "leave_false", "leave_null", "delete"} {
		for _, commit := range []bool{false, true} {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/commit=%t/held=%t", operation, commit, held), func(t *testing.T) {
					partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ func(fixture.Step)) {
						a, peer, run := partialUniqueLockSetup(t, p, b)
						if operation != "insert" {
							run(fixture.Exec("INSERT INTO t VALUES(1,7,TRUE)", 1))
						}
						run(fixture.Exec("BEGIN", 0))
						switch operation {
						case "insert":
							run(fixture.Exec("INSERT INTO t VALUES(1,7,TRUE)", 1))
						case "leave_false":
							run(fixture.Exec("UPDATE t SET active=FALSE WHERE id=1", 1))
						case "leave_null":
							run(fixture.Exec("UPDATE t SET active=NULL WHERE id=1", 1))
						case "delete":
							run(fixture.Exec("DELETE FROM t WHERE id=1", 1))
						}
						// This same-key outsider finishes before A ends, not merely eventually.
						fixture.LockExec(t, p.Ctx, peer, "SET lock_timeout='100ms'", 0)
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(9,7,FALSE)", 1)))
						ids, keys, states := []int64{9}, []int64{7}, []any{false}
						if operation != "insert" {
							ids, keys, states = []int64{1, 9}, []int64{7, 7}, []any{true, false}
						}
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, partialUniqueLockRows(ids, keys, states))
						fixture.LockExec(t, p.Ctx, peer, "RESET lock_timeout", 0)
						var ap, bp int
						if b.PG {
							ap = fixture.LockPID(t, p.Ctx, a)
							bp = fixture.LockPID(t, p.Ctx, peer)
						} else {
							partialUniqueLockTimeout(t, p, b, peer, "INSERT INTO t VALUES(2,7,TRUE)", "lock_timeout", "55P03", "canceling statement due to lock timeout", "", held)
						}
						await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
							return fixture.RowExecute(ctx, peer, "INSERT INTO t VALUES(2,7,TRUE)", held)
						})
						if b.PG {
							fixture.RowWait(t, p.Ctx, bp, ap)
						}
						end := "ROLLBACK"
						if commit {
							end = "COMMIT"
						}
						run(fixture.Exec(end, 0))
						o := await()
						collision := (operation == "insert") == commit
						if collision {
							partialUniqueLockFailure(t, o, fixture.UniqueReferenceDuplicate("t", "pu", "Key (k)=(7) already exists."))
						} else {
							fixture.State(t, o.Err, "")
							fixture.LockAffected(t, o.Result, 1)
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
						run(fixture.Exec("BEGIN", 0))
						run(fixture.Exec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
						run(fixture.Exec("ROLLBACK", 0))
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
				partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ func(fixture.Step)) {
					_, peer, run := partialUniqueLockSetup(t, p, b)
					if operation != "insert" {
						run(fixture.Exec("INSERT INTO t VALUES(1,7,"+excluded+")", 1))
					}
					run(fixture.Exec("BEGIN", 0))
					switch operation {
					case "insert":
						run(fixture.Exec("INSERT INTO t VALUES(1,7,"+excluded+")", 1))
					case "update":
						run(fixture.Exec("UPDATE t SET k=8 WHERE id=1", 1))
					case "delete":
						run(fixture.Exec("DELETE FROM t WHERE id=1", 1))
					}
					// Both member and outsider finish under a short SQL wait bound,
					// while the excluded old/new image owner remains uncommitted.
					fixture.LockExec(t, p.Ctx, peer, "SET lock_timeout='100ms'", 0)
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(2,7,TRUE),(3,7,FALSE),(4,8,TRUE),(5,8,NULL)", 4)))
					run(fixture.Exec("ROLLBACK", 0))
					ids, keys, states := []int64{2, 3, 4, 5}, []int64{7, 7, 8, 8}, []any{true, false, true, nil}
					if operation != "insert" {
						state := any(false)
						if excluded == "NULL" {
							state = nil
						}
						ids, keys, states = []int64{1, 2, 3, 4, 5}, []int64{7, 7, 7, 8, 8}, []any{state, true, false, true, nil}
					}
					run(partialUniqueLockRows(ids, keys, states))
					fixture.LockExec(t, p.Ctx, peer, "RESET lock_timeout", 0)
				})
			})
		}
	}
}

func TestReferencePartialIndexUniqueSavepointClaims(t *testing.T) {
	partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ func(fixture.Step)) {
		_, peer, run := partialUniqueLockSetup(t, p, b)
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,7,TRUE)", 1))
		run(fixture.Exec("SAVEPOINT m", 0))
		run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(2,8,TRUE),(3,7,FALSE)", 2)))
		partialUniqueLockTimeout(t, p, b, peer, "INSERT INTO t VALUES(4,8,TRUE)", "lock_timeout", "55P03", "canceling statement due to lock timeout", `while inserting index tuple (0,4) in relation "pu"`, true)
		run(fixture.Exec("ROLLBACK TO m", 0))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(4,8,TRUE)", 1)))
		partialUniqueLockTimeout(t, p, b, peer, "INSERT INTO t VALUES(5,7,TRUE)", "lock_timeout", "55P03", "canceling statement due to lock timeout", `while inserting index tuple (0,6) in relation "pu"`, false)
		run(partialUniqueLockRows([]int64{1, 4}, []int64{7, 8}, []any{true, true}))
		run(fixture.Exec("COMMIT", 0))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO t VALUES(5,7,TRUE)", fixture.UniqueReferenceDuplicate("t", "pu", "Key (k)=(7) already exists."))))
		run(partialUniqueLockRows([]int64{1, 4}, []int64{7, 8}, []any{true, true}))
		// A's premark claim remains authoritative after postmark error recovery.
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT n", 0))
		run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(6,8,TRUE)", fixture.UniqueReferenceDuplicate("t", "pu", "Key (k)=(8) already exists.")))
		run(fixture.Exec("ROLLBACK TO n", 0))
		run(fixture.Exec("INSERT INTO t VALUES(6,9,TRUE)", 1))
		run(fixture.Exec("COMMIT", 0))
		run(partialUniqueLockRows([]int64{1, 4, 6}, []int64{7, 8, 9}, []any{true, true, true}))
	})
}

func TestReferencePartialIndexUniqueCanceledAndTimedClaims(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, mode := range []string{"lock_timeout", "statement_timeout", "cancel"} {
			t.Run(fmt.Sprintf("%s/held=%t", mode, held), func(t *testing.T) {
				partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ func(fixture.Step)) {
					a, peer, run := partialUniqueLockSetup(t, p, b)
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1,7,TRUE)", 1))
					const query = "INSERT INTO t VALUES(2,7,TRUE)"
					if mode == "cancel" {
						if b.PG {
							ap, bp := fixture.LockPID(t, p.Ctx, a), fixture.LockPID(t, p.Ctx, peer)
							await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome { return fixture.RowExecute(ctx, peer, query, held) })
							fixture.RowWait(t, p.Ctx, bp, ap)
							var ok bool
							if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&ok); err != nil || !ok {
								t.Fatal(ok, err)
							}
							want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
							want.Context = `while inserting index tuple (0,2) in relation "pu"`
							partialUniqueLockFailure(t, await(), want)
						} else {
							ctx, cancel := context.WithTimeout(p.Ctx, 100*time.Millisecond)
							o := fixture.RowExecute(ctx, peer, query, held)
							cancel()
							if o.Result != nil || o.Rows != nil || o.Stmt != nil || !errors.Is(o.Err, context.DeadlineExceeded) {
								t.Fatal(o)
							}
							var state interface{ SQLState() string }
							if errors.As(o.Err, &state) {
								t.Fatal("caller deadline fabricated SQLSTATE", o.Err)
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
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(3,8,TRUE)", 1)))
					run(fixture.Exec("ROLLBACK", 0))
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Prepared(fixture.Exec(query, 1)))
					run(partialUniqueLockRows([]int64{2, 3}, []int64{7, 8}, []any{true, true}))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
					run(fixture.Exec("ROLLBACK", 0))
				})
			})
		}
	}
}

func TestReferencePartialIndexUniqueKeyDeadlock(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%t", held), func(t *testing.T) {
			partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ func(fixture.Step)) {
				a, peer, run := partialUniqueLockSetup(t, p, b)
				namespace := fixture.UniqueReferencePublicNamespace(t, p.Ctx, b.DB, b.PG)
				var indexOID int64
				if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='pu' AND relnamespace=$1", namespace).Scan(&indexOID); err != nil || indexOID <= 0 {
					t.Fatal(indexOID, err)
				}
				if b.PG {
					fixture.LockExec(t, p.Ctx, a, "SET deadlock_timeout='2s'", 0)
					fixture.LockExec(t, p.Ctx, peer, "SET deadlock_timeout='100ms'", 0)
				}
				run(fixture.Exec("BEGIN", 0))
				fixture.LockExec(t, p.Ctx, peer, "BEGIN", 0)
				run(fixture.Exec("INSERT INTO t VALUES(1,7,TRUE)", 1))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("INSERT INTO t VALUES(2,8,TRUE)", 1))
				var ap, bp int
				var ax, bx string
				if b.PG {
					ap, bp = fixture.LockPID(t, p.Ctx, a), fixture.LockPID(t, p.Ctx, peer)
					if err := a.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id()::text").Scan(&ax); err != nil {
						t.Fatal(err)
					}
					if err := peer.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id()::text").Scan(&bx); err != nil {
						t.Fatal(err)
					}
				} else {
					// Prove the first edge without losing A's premark claim.
					run(fixture.Exec("SAVEPOINT admission", 0))
					fixture.LockExec(t, p.Ctx, a, "SET LOCAL lock_timeout='100ms'", 0)
					partialUniqueLockFailure(t, fixture.RowExecute(p.Ctx, a, "INSERT INTO t VALUES(3,8,TRUE)", held), fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout"))
					run(fixture.Exec("ROLLBACK TO admission", 0))
					run(fixture.Exec("RELEASE SAVEPOINT admission", 0))
					run(fixture.Exec("RESET lock_timeout", 0))
				}
				first := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					return fixture.RowExecute(ctx, a, "INSERT INTO t VALUES(3,8,TRUE)", held)
				})
				if b.PG {
					fixture.RowWait(t, p.Ctx, ap, bp)
				}
				second := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					return fixture.RowExecute(ctx, peer, "INSERT INTO t VALUES(4,7,TRUE)", held)
				})
				// Join both. Which worker reports first is never victim identity.
				outcomes := []fixture.RelationOutcome{first(), second()}
				victim := -1
				for i, o := range outcomes {
					if o.Err == nil {
						fixture.LockAffected(t, o.Result, 1)
						continue
					}
					if victim != -1 {
						t.Fatal("both participants failed", outcomes)
					}
					victim = i
					if o.Result != nil || o.Rows != nil || o.Stmt != nil {
						t.Fatal("victim published resources", o)
					}
					if b.PG {
						vp, hp, vx, hx, slot := ap, bp, ax, bx, 3
						if i == 1 {
							vp, hp, vx, hx, slot = bp, ap, bx, ax, 4
						}
						want := fixture.ControlsReferenceDiagnostic("40P01", "deadlock detected")
						want.Hint = "See server log for query details."
						want.Detail = fmt.Sprintf("Process %d waits for ShareLock on transaction %s; blocked by process %d.\nProcess %d waits for ShareLock on transaction %s; blocked by process %d.", vp, hx, hp, hp, vx, vp)
						want.Context = fmt.Sprintf("while inserting index tuple (0,%d) in relation \"pu\"", slot)
						fixture.AssertDiagnostic(t, o.Err, &want)
					} else {
						uniqueReferenceNativeKeyDeadlock(t, o.Err, indexOID)
					}
				}
				if victim < 0 {
					t.Fatal("opposite member claims did not deadlock")
				}
				conns := []fixture.Executor{a, peer}
				fixture.RunStep(t, p.Ctx, conns[victim], b.Recorder, fixture.ImplicitError("INSERT INTO t VALUES(5,9,TRUE)", fixture.TransactionAborted()))
				fixture.LockExec(t, p.Ctx, conns[victim], "ROLLBACK", 0)
				fixture.LockExec(t, p.Ctx, conns[1-victim], "COMMIT", 0)
				ids := []int64{1, 3}
				keys := []int64{7, 8}
				if victim == 0 {
					ids, keys = []int64{2, 4}, []int64{8, 7}
				}
				run(partialUniqueLockRows(ids, keys, []any{true, true}))
				// Reuse the exact victim session, then prove no relation holds leaked.
				fixture.RunStep(t, p.Ctx, conns[victim], b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(5,9,TRUE)", 1)))
				run(partialUniqueLockRows(append(ids, 5), append(keys, 9), []any{true, true, true}))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
				run(fixture.Exec("ROLLBACK", 0))
			})
		})
	}
}
