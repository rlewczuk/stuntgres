package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceUniqueRepeatableReadKeyHistory(t *testing.T) {
	cases := []struct {
		name, typ, seed     string
		updates             []string
		separate, serialize bool
		final               int64
	}{
		{"payload", "INT", "1", []string{"UPDATE t SET v=20"}, false, false, 20},
		{"exact_noop", "INT", "1", []string{"UPDATE t SET k=k"}, false, false, 10},
		{"unique_change", "INT", "1", []string{"UPDATE t SET k=2"}, false, true, 10},
		{"numeric_scale", "NUMERIC", "1.0", []string{"UPDATE t SET k=1.00"}, false, true, 10},
		{"numeric_exact", "NUMERIC", "1.0", []string{"UPDATE t SET k=1.0"}, false, false, 10},
		{"signed_zero", "DOUBLE PRECISION", "'0'", []string{"UPDATE t SET k='-0'"}, false, true, 10},
		{"restored_one_tx", "INT", "1", []string{"UPDATE t SET k=2", "UPDATE t SET k=1"}, false, true, 10},
		{"restored_two_tx", "INT", "1", []string{"UPDATE t SET k=2", "UPDATE t SET k=1"}, true, true, 10},
		{"change_then_payload", "INT", "1", []string{"UPDATE t SET k=2", "UPDATE t SET v=20"}, true, true, 20},
		{"restore_then_payload", "INT", "1", []string{"UPDATE t SET k=2", "UPDATE t SET k=1", "UPDATE t SET v=20"}, true, true, 20},
	}
	for _, c := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", c.name, prepared), func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						a, peer := referenceLockConnections(t, p.ctx, b.db)
						run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
						run(referenceExec("CREATE TABLE t(k "+c.typ+" CONSTRAINT u UNIQUE,v INT)", 0))
						run(referenceExec("INSERT INTO t VALUES("+c.seed+",10)", 1))
						run(referenceExec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
						run(referenceSelect("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
						if !c.separate {
							referenceLockExec(t, p.ctx, peer, "BEGIN", 0)
						}
						for _, update := range c.updates {
							referenceLockExec(t, p.ctx, peer, update, 1)
						}
						if !c.separate {
							referenceLockExec(t, p.ctx, peer, "COMMIT", 0)
						}
						query := "SELECT v FROM t WHERE k=" + c.seed + " FOR KEY SHARE"
						if c.serialize {
							outcome := referenceRowQuery(p.ctx, a, query, prepared)
							if outcome.rows != nil {
								present := outcome.rows.Next()
								if outcome.err == nil {
									outcome.err = outcome.rows.Err()
								}
								outcome.rows.Close()
								if present {
									t.Fatal("key-changing RR locking query returned a retained row")
								}
							}
							if outcome.stmt != nil {
								outcome.stmt.Close()
							}
							want := controlsReferenceDiagnostic("40001", "could not serialize access due to concurrent update")
							referenceAssertDiagnostic(t, outcome.err, &want)
							aborted := referenceImplicitError("INSERT INTO t VALUES(8,80)", referenceTransactionAborted())
							if prepared {
								aborted = referencePrepared(aborted)
							}
							run(aborted)
						} else {
							s := referenceSelect(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
							if prepared {
								s = referencePrepared(s)
							}
							run(s)
						}
						run(referenceExec("ROLLBACK", 0))
						run(referenceSelect("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{c.final}}))
						run(referenceExec("INSERT INTO t VALUES(9,90)", 1))
						run(referenceSelect("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{c.final}, {int64(90)}}))
					})
				}
			})
		}
	}
}

func TestReferenceUniqueRepeatableReadHistoryControls(t *testing.T) {
	for _, mode := range []string{"rolled_back_change", "old_change", "old_restore"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", mode, prepared), func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						a, peer := referenceLockConnections(t, p.ctx, b.db)
						run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
						run(referenceExec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE,v INT)", 0))
						run(referenceExec("INSERT INTO t VALUES(1,10)", 1))
						key := int64(1)
						if mode != "rolled_back_change" {
							referenceLockExec(t, p.ctx, peer, "UPDATE t SET k=2", 1)
							key = 2
							if mode == "old_restore" {
								referenceLockExec(t, p.ctx, peer, "UPDATE t SET k=1", 1)
								key = 1
							}
						}
						run(referenceExec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
						run(referenceSelect("SELECT k,v FROM t", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{key, int64(10)}}))
						if mode == "rolled_back_change" {
							referenceLockExec(t, p.ctx, peer, "BEGIN", 0)
							referenceLockExec(t, p.ctx, peer, "UPDATE t SET k=2", 1)
							referenceLockExec(t, p.ctx, peer, "ROLLBACK", 0)
						}
						referenceLockExec(t, p.ctx, peer, "UPDATE t SET v=20", 1)
						s := referenceSelect(fmt.Sprintf("SELECT k,v FROM t WHERE k=%d FOR KEY SHARE", key), []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{key, int64(10)}})
						if prepared {
							s = referencePrepared(s)
						}
						run(s)
						run(referenceExec("ROLLBACK", 0))
						run(referenceSelect("SELECT k,v FROM t FOR KEY SHARE", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{key, int64(20)}}))
					})
				}
			})
		}
	}
}
