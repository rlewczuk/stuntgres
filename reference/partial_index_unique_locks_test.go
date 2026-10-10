package reference_test

import (
	"context"
	"errors"
	"fmt"
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
				partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, _ func(referenceStep)) {
					a, peer := referenceLockConnections(t, p.ctx, b.db)
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
					run(referenceExec("CREATE TABLE t(id INT,k "+c.typ+",active BOOL)", 0))
					run(referenceExec("CREATE UNIQUE INDEX pu ON t(k) WHERE "+c.predicate, 0))
					if c.name == "where_true" || c.name == "parenthesized_true" || c.name == "contextual_true" || c.name == "cast_true" || c.name == "identity_cast_true" {
						run(referenceSelect("SELECT pg_get_indexdef('pu'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX pu ON public.t USING btree (k)"}}))
					}
					if c.name == "ordinary_control" {
						run(referenceExec("CREATE UNIQUE INDEX ordinary_u ON t(k)", 0))
					}
					run(referenceExec("INSERT INTO t VALUES(1,"+c.seed+",TRUE)", 1))
					run(referenceExec("BEGIN", 0))
					lock := partialIndexIDs("SELECT id FROM t FOR KEY SHARE", 1)
					if held {
						lock = referencePrepared(lock)
					}
					run(lock)
					referenceLockExec(t, p.ctx, peer, "SET lock_timeout='100ms'", 0)
					o := referenceRowExecute(p.ctx, peer, c.update, held)
					if c.blocked {
						want := controlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
						if b.pg {
							action := "updating"
							if c.name == "delete" {
								action = "deleting"
							}
							want.Context = "while " + action + " tuple (0,1) in relation \"t\""
						}
						partialUniqueLockFailure(t, o, want)
					} else {
						referenceState(t, o.err, "")
						referenceLockAffected(t, o.result, 1)
					}
					run(referenceExec("ROLLBACK", 0))
					referenceLockExec(t, p.ctx, peer, "RESET lock_timeout", 0)
					if c.blocked {
						referenceRowAffected(t, p.ctx, peer, c.update, held, 1)
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
					run(referenceSelect("SELECT id,k,active FROM t", []string{"id", "k", "active"}, []string{"INT4", typ, "BOOL"}, rows))
					if c.name == "numeric_scale" || c.name == "signed_zero" {
						text := "1.00"
						if c.name == "signed_zero" {
							text = "-0"
						}
						var actual string
						if err := a.QueryRowContext(p.ctx, "SELECT k FROM t").Scan(&actual); err != nil {
							t.Fatal(err)
						}
						if actual != text {
							t.Fatalf("stored key representation: got %q, want %q", actual, text)
						}
					}
					run(referenceExec("BEGIN", 0))
					run(referenceExec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
					run(referenceExec("ROLLBACK", 0))
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
					partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, _ func(referenceStep)) {
						a, peer := referenceLockConnections(t, p.ctx, b.db)
						run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
						run(referenceExec("CREATE TABLE t(id INT,k INT,active BOOL,v INT)", 0))
						run(referenceExec("CREATE UNIQUE INDEX pu ON t(k) WHERE active", 0))
						if ordinary {
							run(referenceExec("CREATE UNIQUE INDEX ordinary_u ON t(k)", 0))
						}
						run(referenceExec("INSERT INTO t VALUES(1,1,TRUE,10)", 1))
						run(referenceExec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
						run(referenceSelect("SELECT id,k,active,v FROM t", []string{"id", "k", "active", "v"}, []string{"INT4", "INT4", "BOOL", "INT4"}, [][]any{{int64(1), int64(1), true, int64(10)}}))
						prun := func(q string, n int64) { referenceLockExec(t, p.ctx, peer, q, n) }
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
							o := referenceRowQuery(p.ctx, a, query, held)
							err := queryLockErrorOutcome(t, o, b.pg, "id", nil)
							want := controlsReferenceDiagnostic("40001", "could not serialize access due to concurrent update")
							referenceAssertDiagnostic(t, err, &want)
							run(referenceImplicitError("INSERT INTO t VALUES(2,9,TRUE,90)", referenceTransactionAborted()))
						} else {
							s := partialIndexIDs(query, 1)
							if held {
								s = referencePrepared(s)
							}
							run(s)
							// The compatible lock retains the original complete RR image.
							run(referenceSelect("SELECT id,k,active,v FROM t", []string{"id", "k", "active", "v"}, []string{"INT4", "INT4", "BOOL", "INT4"}, [][]any{{int64(1), int64(1), true, int64(10)}}))
						}
						run(referenceExec("ROLLBACK", 0))
						run(referenceSelect("SELECT id,k,active,v FROM t", []string{"id", "k", "active", "v"}, []string{"INT4", "INT4", "BOOL", "INT4"}, [][]any{{int64(1), int64(1), true, int64(20)}}))
						run(referencePrepared(referenceExec("INSERT INTO t VALUES(2,2,TRUE,30)", 1)))
						run(partialIndexIDs("SELECT id FROM t ORDER BY id", 1, 2))
					})
				})
			}
		}
	}
}

func TestReferencePartialIndexUniqueSavepointTimeoutResources(t *testing.T) {
	for _, setting := range []string{"lock_timeout", "statement_timeout", "cancel"} {
		t.Run(setting, func(t *testing.T) {
			partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, _ func(referenceStep)) {
				a, blocker, run := partialUniqueLockSetup(t, p, b)
				peer := sequenceCoreConn(t, p.ctx, b.db)
				run(referenceExec("INSERT INTO t VALUES(10,10,FALSE),(11,11,FALSE)", 2))
				run(referenceExec("BEGIN", 0))
				run(referenceExec("INSERT INTO t VALUES(1,7,TRUE)", 1))
				run(partialIndexIDs("SELECT id FROM t WHERE id=10 FOR KEY SHARE", 10))
				run(referenceExec("SAVEPOINT m", 0))
				run(referenceExec("INSERT INTO t VALUES(2,8,TRUE)", 1))
				run(partialIndexIDs("SELECT id FROM t WHERE id=11 FOR KEY SHARE", 11))
				referenceLockExec(t, p.ctx, blocker, "BEGIN", 0)
				referenceLockExec(t, p.ctx, blocker, "INSERT INTO t VALUES(99,99,TRUE)", 1)
				const query = "INSERT INTO t VALUES(4,99,TRUE)"
				if setting == "cancel" {
					if b.pg {
						ap, bp := referenceLockPID(t, p.ctx, a), referenceLockPID(t, p.ctx, blocker)
						await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome { return referenceRowExecute(ctx, a, query, true) })
						referenceRowWait(t, p.ctx, ap, bp)
						var ok bool
						if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", ap).Scan(&ok); err != nil || !ok {
							t.Fatal(ok, err)
						}
						want := controlsReferenceDiagnostic("57014", "canceling statement due to user request")
						want.Context = `while inserting index tuple (0,6) in relation "pu"`
						partialUniqueLockFailure(t, await(), want)
					} else {
						ctx, cancel := context.WithTimeout(p.ctx, 100*time.Millisecond)
						o := referenceRowExecute(ctx, a, query, true)
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
					run(referenceExec("SET LOCAL "+setting+"='100ms'", 0))
					code, message := "55P03", "canceling statement due to lock timeout"
					if setting == "statement_timeout" {
						code, message = "57014", "canceling statement due to statement timeout"
					}
					want := controlsReferenceDiagnostic(code, message)
					if b.pg {
						want.Context = `while inserting index tuple (0,6) in relation "pu"`
					}
					partialUniqueLockFailure(t, referenceRowExecute(p.ctx, a, query, true), want)
				}
				run(referenceImplicitError("INSERT INTO t VALUES(5,100,TRUE)", referenceTransactionAborted()))
				run(referenceExec("ROLLBACK TO m", 0))
				// Subtransaction rollback releases postmark claims AND row locks.
				referenceLockExec(t, p.ctx, peer, "SET lock_timeout='100ms'", 0)
				referenceRunStep(t, p.ctx, peer, b.recorder, referencePrepared(referenceExec("INSERT INTO t VALUES(3,8,TRUE)", 1)))
				referenceRunStep(t, p.ctx, peer, b.recorder, referenceExec("DELETE FROM t WHERE id=11", 1))
				partialUniqueLockTimeout(t, p, b, peer, "INSERT INTO t VALUES(5,7,TRUE)", "lock_timeout", "55P03", "canceling statement due to lock timeout", `while inserting index tuple (0,8) in relation "pu"`, true)
				partialUniqueLockTimeout(t, p, b, peer, "DELETE FROM t WHERE id=10", "lock_timeout", "55P03", "canceling statement due to lock timeout", `while deleting tuple (0,1) in relation "t"`, true)
				run(partialUniqueLockRows([]int64{1, 3, 10}, []int64{7, 8, 10}, []any{true, true, false}))
				referenceLockExec(t, p.ctx, blocker, "ROLLBACK", 0)
				run(referencePrepared(referenceExec("INSERT INTO t VALUES(4,99,TRUE)", 1)))
				run(referenceExec("COMMIT", 0))
				run(partialUniqueLockRows([]int64{1, 3, 4, 10}, []int64{7, 8, 99, 10}, []any{true, true, true, false}))
				referenceLockExec(t, p.ctx, peer, "BEGIN", 0)
				referenceLockExec(t, p.ctx, peer, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
				referenceLockExec(t, p.ctx, peer, "ROLLBACK", 0)
			})
		})
	}
}
