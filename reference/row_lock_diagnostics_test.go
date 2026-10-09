package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceRowLockDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,v INT)", 0))
	p.step(t, referenceExec("CREATE SEQUENCE s", 0))
	for _, clause := range referenceRowClauses {
		for _, prepared := range []bool{false, true} {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				for _, tc := range []struct{ query, suffix string }{{"SELECT id FROM t GROUP BY id ", "GROUP BY clause"}, {"SELECT count(*) FROM t HAVING TRUE ", "HAVING clause"}, {"SELECT count(*) FROM t ", "aggregate functions"}} {
					step := referenceImplicitError(tc.query+clause, referenceDiagnostic{Code: "0A000", Message: clause + " is not allowed with " + tc.suffix})
					step.selectRows = true
					if prepared {
						step = referencePrepared(step)
					}
					p.step(t, step)
				}
				step := referenceImplicitError("SELECT last_value FROM s "+clause, referenceDiagnostic{Code: "42809", Message: `cannot lock rows in sequence "s"`})
				step.selectRows = true
				step.errorStage = "execute"
				if prepared {
					step = referencePrepared(step)
				}
				p.step(t, step)
				p.step(t, referenceExec("BEGIN READ ONLY", 0))
				step = referenceImplicitError("SELECT v FROM t WHERE id=999 "+clause, referenceDiagnostic{Code: "25006", Message: "cannot execute SELECT " + clause + " in a read-only transaction"})
				step.selectRows = true
				step.errorStage = "execute"
				if prepared {
					step = referencePrepared(step)
				}
				p.step(t, step)
				p.step(t, referenceExec("ROLLBACK", 0))
			})
		}
	}
	p.step(t, referenceSelect("SELECT last_value FROM s", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(1)}}))
}
func TestReferenceRowLockRepeatableRead(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, clause := range referenceRowClauses {
			for _, change := range []string{"UPDATE t SET v=20 WHERE id=1", "DELETE FROM t WHERE id=1"} {
				t.Run(clause+"/"+change+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					p := newReferenceScenario(t)
					for _, backend := range p.backends() {
						t.Run(backend.name, func(t *testing.T) {
							a, b := referenceLockConnections(t, p.ctx, backend.db)
							recorder := backend.recorder
							referenceRunStep(t, p.ctx, a, recorder, referenceExec("CREATE TABLE t(id INT,v INT)", 0))
							referenceRunStep(t, p.ctx, a, recorder, referenceExec("INSERT INTO t VALUES(1,10)", 1))
							tx, err := b.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							referenceRunStep(t, p.ctx, tx, recorder, referenceSelect("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
							referenceRunStep(t, p.ctx, a, recorder, referenceExec(change, 1))
							if clause == "FOR KEY SHARE" && change[0] == 'U' {
								step := referenceSelect("SELECT v FROM t "+clause, []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
								if prepared {
									step = referencePrepared(step)
								}
								referenceRunStep(t, p.ctx, tx, recorder, step)
								predicate := referenceSelect("SELECT v FROM t WHERE v=10 "+clause, []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
								if prepared {
									predicate = referencePrepared(predicate)
								}
								referenceRunStep(t, p.ctx, tx, recorder, predicate)
								return
							}
							step := referenceImplicitError("SELECT v FROM t "+clause, referenceDiagnostic{Code: "40001", Message: "could not serialize access due to concurrent update"})
							step.selectRows = true
							step.errorStage = "execute"
							if prepared {
								step = referencePrepared(step)
							}
							referenceRunStep(t, p.ctx, tx, recorder, step)
							referenceRunStep(t, p.ctx, tx, recorder, referenceImplicitError("SELECT v FROM t", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
						})
					}
				})
			}
		}
	}
}
