package reference_test

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePartialIndexUniqueRowLockStrength(t *testing.T) {
	cases := []struct {
		name, typ, seed, predicate, update string
		blocked                            bool
		finalKey                           any
		finalActive                        bool
	}{
		{"key", "INT", "1", "active", "UPDATE t SET k=2", false, int64(2), true},
		{"predicate", "INT", "1", "active", "UPDATE t SET active=FALSE", false, int64(1), false},
		{"where_true", "INT", "1", "TRUE", "UPDATE t SET k=2", true, int64(2), true},
		{"parenthesized_true", "INT", "1", "(TRUE)", "UPDATE t SET k=2", true, int64(2), true},
		{"contextual_true", "INT", "1", "'true'", "UPDATE t SET k=2", true, int64(2), true},
		{"cast_true", "INT", "1", "'true'::BOOLEAN", "UPDATE t SET k=2", true, int64(2), true},
		{"identity_cast_true", "INT", "1", "TRUE::BOOLEAN", "UPDATE t SET k=2", true, int64(2), true},
		{"typed_text_true", "INT", "1", "'true'::TEXT::BOOLEAN", "UPDATE t SET k=2", false, int64(2), true},
		{"folded_compare", "INT", "1", "1=1", "UPDATE t SET k=2", false, int64(2), true},
		{"folded_conjunction", "INT", "1", "TRUE AND TRUE", "UPDATE t SET k=2", false, int64(2), true},
		{"folded_disjunction", "INT", "1", "active OR TRUE", "UPDATE t SET k=2", false, int64(2), true},
		{"numeric_scale", "NUMERIC", "1.0", "active", "UPDATE t SET k=1.00", false, "1.00", true},
		{"signed_zero", "DOUBLE PRECISION", "'0'", "active", "UPDATE t SET k='-0'", false, float64(0), true},
		{"delete", "INT", "1", "active", "DELETE FROM t", true, nil, false},
		{"ordinary_control", "INT", "1", "active", "UPDATE t SET k=2", true, int64(2), true},
	}
	for _, c := range cases {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/held=%t", c.name, held), func(t *testing.T) {
				partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ func(fixture.Step)) {
					a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(id INT,k "+c.typ+",active BOOL)", 0))
					run(fixture.Exec("CREATE UNIQUE INDEX pu ON t(k) WHERE "+c.predicate, 0))
					if c.name == "where_true" || c.name == "parenthesized_true" || c.name == "contextual_true" || c.name == "cast_true" || c.name == "identity_cast_true" {
						run(fixture.Select("SELECT pg_get_indexdef('pu'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX pu ON public.t USING btree (k)"}}))
					}
					if c.name == "ordinary_control" {
						run(fixture.Exec("CREATE UNIQUE INDEX ordinary_u ON t(k)", 0))
					}
					run(fixture.Exec("INSERT INTO t VALUES(1,"+c.seed+",TRUE)", 1))
					run(fixture.Exec("BEGIN", 0))
					lock := fixture.PartialIndexIDs("SELECT id FROM t FOR KEY SHARE", 1)
					if held {
						lock = fixture.Prepared(lock)
					}
					run(lock)
					fixture.LockExec(t, p.Ctx, peer, "SET lock_timeout='10ms'", 0)
					o := fixture.RowExecute(p.Ctx, peer, c.update, held)
					if c.blocked {
						want := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
						if b.PG {
							action := "updating"
							if c.name == "delete" {
								action = "deleting"
							}
							want.Context = "while " + action + " tuple (0,1) in relation \"t\""
						}
						partialUniqueLockFailure(t, o, want)
					} else {
						fixture.State(t, o.Err, "")
						fixture.LockAffected(t, o.Result, 1)
					}
					run(fixture.Exec("ROLLBACK", 0))
					fixture.LockExec(t, p.Ctx, peer, "RESET lock_timeout", 0)
					if c.blocked {
						fixture.RowAffected(t, p.Ctx, peer, c.update, held, 1)
					}
					typ := "INT4"
					if c.typ == "NUMERIC" {
						typ = "NUMERIC"
					}
					if c.typ == "DOUBLE PRECISION" {
						typ = "FLOAT8"
					}
					rows := [][]any{{int64(1), c.finalKey, c.finalActive}}
					if c.name == "delete" {
						rows = nil
					}
					run(fixture.Select("SELECT id,k,active FROM t", []string{"id", "k", "active"}, []string{"INT4", typ, "BOOL"}, rows))
					if c.name == "numeric_scale" || c.name == "signed_zero" {
						text := "1.00"
						if c.name == "signed_zero" {
							text = "-0"
						}
						var actual string
						if err := a.QueryRowContext(p.Ctx, "SELECT k FROM t").Scan(&actual); err != nil {
							t.Fatal(err)
						}
						if actual != text {
							t.Fatalf("stored key representation: got %q, want %q", actual, text)
						}
					}
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
					run(fixture.Exec("ROLLBACK", 0))
				})
			})
		}
	}
}

func TestReferencePartialIndexUniqueRepeatableReadHistory(t *testing.T) {
	for _, mode := range []string{"key_restore_one_tx", "key_restore_two_tx", "membership_restore", "key_then_payload", "savepoint_abort", "transaction_abort"} {
		for _, ordinary := range []bool{false, true} {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/ordinary=%t/held=%t", mode, ordinary, held), func(t *testing.T) {
					partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ func(fixture.Step)) {
						a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
						run(fixture.Exec("CREATE TABLE t(id INT,k INT,active BOOL,v INT)", 0))
						run(fixture.Exec("CREATE UNIQUE INDEX pu ON t(k) WHERE active", 0))
						if ordinary {
							run(fixture.Exec("CREATE UNIQUE INDEX ordinary_u ON t(k)", 0))
						}
						run(fixture.Exec("INSERT INTO t VALUES(1,1,TRUE,10)", 1))
						run(fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
						run(fixture.Select("SELECT id,k,active,v FROM t", []string{"id", "k", "active", "v"}, []string{"INT4", "INT4", "BOOL", "INT4"}, [][]any{{int64(1), int64(1), true, int64(10)}}))
						prun := func(q string, n int64) { fixture.LockExec(t, p.Ctx, peer, q, n) }
						switch mode {
						case "key_restore_one_tx":
							prun("BEGIN", 0)
							prun("UPDATE t SET k=2", 1)
							prun("UPDATE t SET k=1", 1)
							prun("COMMIT", 0)
						case "key_restore_two_tx", "key_then_payload":
							prun("UPDATE t SET k=2", 1)
							prun("UPDATE t SET k=1", 1)
						case "membership_restore":
							prun("UPDATE t SET active=FALSE", 1)
							prun("UPDATE t SET active=TRUE", 1)
						case "savepoint_abort":
							prun("BEGIN", 0)
							prun("SAVEPOINT m", 0)
							prun("UPDATE t SET k=2,active=FALSE", 1)
							prun("ROLLBACK TO m", 0)
							prun("COMMIT", 0)
						case "transaction_abort":
							prun("BEGIN", 0)
							prun("UPDATE t SET k=2,active=FALSE", 1)
							prun("ROLLBACK", 0)
						}
						prun("UPDATE t SET v=20", 1)
						serialize := ordinary && (mode == "key_restore_one_tx" || mode == "key_restore_two_tx" || mode == "key_then_payload")
						const query = "SELECT id FROM t WHERE k=1 AND active FOR KEY SHARE"
						if serialize {
							o := fixture.RowQuery(p.Ctx, a, query, held)
							err := fixture.QueryLockErrorOutcome(t, o, b.PG, "id", nil)
							want := fixture.ControlsReferenceDiagnostic("40001", "could not serialize access due to concurrent update")
							fixture.AssertDiagnostic(t, err, &want)
							run(fixture.ImplicitError("INSERT INTO t VALUES(2,9,TRUE,90)", fixture.TransactionAborted()))
						} else {
							s := fixture.PartialIndexIDs(query, 1)
							if held {
								s = fixture.Prepared(s)
							}
							run(s)
							// The compatible lock retains the original complete RR image.
							run(fixture.Select("SELECT id,k,active,v FROM t", []string{"id", "k", "active", "v"}, []string{"INT4", "INT4", "BOOL", "INT4"}, [][]any{{int64(1), int64(1), true, int64(10)}}))
						}
						run(fixture.Exec("ROLLBACK", 0))
						run(fixture.Select("SELECT id,k,active,v FROM t", []string{"id", "k", "active", "v"}, []string{"INT4", "INT4", "BOOL", "INT4"}, [][]any{{int64(1), int64(1), true, int64(20)}}))
						run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(2,2,TRUE,30)", 1)))
						run(fixture.PartialIndexIDs("SELECT id FROM t ORDER BY id", 1, 2))
					})
				})
			}
		}
	}
}

func TestReferencePartialIndexUniqueSavepointTimeoutResources(t *testing.T) {
	for _, setting := range []string{"lock_timeout", "statement_timeout", "cancel"} {
		t.Run(setting, func(t *testing.T) {
			partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ func(fixture.Step)) {
				a, blocker, run := partialUniqueLockSetup(t, p, b)
				peer := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				run(fixture.Exec("INSERT INTO t VALUES(10,10,FALSE),(11,11,FALSE)", 2))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,7,TRUE)", 1))
				run(fixture.PartialIndexIDs("SELECT id FROM t WHERE id=10 FOR KEY SHARE", 10))
				run(fixture.Exec("SAVEPOINT m", 0))
				run(fixture.Exec("INSERT INTO t VALUES(2,8,TRUE)", 1))
				run(fixture.PartialIndexIDs("SELECT id FROM t WHERE id=11 FOR KEY SHARE", 11))
				fixture.LockExec(t, p.Ctx, blocker, "BEGIN", 0)
				fixture.LockExec(t, p.Ctx, blocker, "INSERT INTO t VALUES(99,99,TRUE)", 1)
				const query = "INSERT INTO t VALUES(4,99,TRUE)"
				if setting == "cancel" {
					if b.PG {
						ap, bp := fixture.LockPID(t, p.Ctx, a), fixture.LockPID(t, p.Ctx, blocker)
						await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome { return fixture.RowExecute(ctx, a, query, true) })
						fixture.RowWait(t, p.Ctx, ap, bp)
						var ok bool
						if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", ap).Scan(&ok); err != nil || !ok {
							t.Fatal(ok, err)
						}
						want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
						want.Context = `while inserting index tuple (0,6) in relation "pu"`
						partialUniqueLockFailure(t, await(), want)
					} else {
						ctx, cancel := context.WithTimeout(p.Ctx, 100*time.Millisecond)
						o := fixture.RowExecute(ctx, a, query, true)
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
					run(fixture.Exec("SET LOCAL "+setting+"='"+timerValue(setting)+"'", 0))
					code, message := "55P03", "canceling statement due to lock timeout"
					if setting == "statement_timeout" {
						code, message = "57014", "canceling statement due to statement timeout"
					}
					want := fixture.ControlsReferenceDiagnostic(code, message)
					if b.PG {
						want.Context = `while inserting index tuple (0,6) in relation "pu"`
					}
					partialUniqueLockFailure(t, fixture.RowExecute(p.Ctx, a, query, true), want)
				}
				run(fixture.ImplicitError("INSERT INTO t VALUES(5,100,TRUE)", fixture.TransactionAborted()))
				run(fixture.Exec("ROLLBACK TO m", 0))
				// Subtransaction rollback releases postmark claims AND row locks.
				fixture.LockExec(t, p.Ctx, peer, "SET lock_timeout='100ms'", 0)
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(3,8,TRUE)", 1)))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("DELETE FROM t WHERE id=11", 1))
				partialUniqueLockTimeout(t, p, b, peer, "INSERT INTO t VALUES(5,7,TRUE)", "lock_timeout", "55P03", "canceling statement due to lock timeout", `while inserting index tuple (0,8) in relation "pu"`, true)
				partialUniqueLockTimeout(t, p, b, peer, "DELETE FROM t WHERE id=10", "lock_timeout", "55P03", "canceling statement due to lock timeout", `while deleting tuple (0,1) in relation "t"`, true)
				run(partialUniqueLockRows([]int64{1, 3, 10}, []int64{7, 8, 10}, []any{true, true, false}))
				fixture.LockExec(t, p.Ctx, blocker, "ROLLBACK", 0)
				run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(4,99,TRUE)", 1)))
				run(fixture.Exec("COMMIT", 0))
				run(partialUniqueLockRows([]int64{1, 3, 4, 10}, []int64{7, 8, 99, 10}, []any{true, true, true, false}))
				fixture.LockExec(t, p.Ctx, peer, "BEGIN", 0)
				fixture.LockExec(t, p.Ctx, peer, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
				fixture.LockExec(t, p.Ctx, peer, "ROLLBACK", 0)
			})
		})
	}
}
