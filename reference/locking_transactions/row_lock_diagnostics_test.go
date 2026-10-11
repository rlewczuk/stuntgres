package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowLockDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
	p.Step(t, fixture.Exec("CREATE SEQUENCE s", 0))
	for _, clause := range fixture.RowClauses {
		for _, prepared := range []bool{false, true} {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				for _, tc := range []struct{ query, suffix string }{{"SELECT id FROM t GROUP BY id ", "GROUP BY clause"}, {"SELECT count(*) FROM t HAVING TRUE ", "HAVING clause"}, {"SELECT count(*) FROM t ", "aggregate functions"}} {
					step := fixture.ImplicitError(tc.query+clause, fixture.Diagnostic{Code: "0A000", Message: clause + " is not allowed with " + tc.suffix})
					step.SelectRows = true
					if prepared {
						step = fixture.Prepared(step)
					}
					p.Step(t, step)
				}
				step := fixture.ImplicitError("SELECT last_value FROM s "+clause, fixture.Diagnostic{Code: "42809", Message: `cannot lock rows in sequence "s"`})
				step.SelectRows = true
				step.ErrorStage = "execute"
				if prepared {
					step = fixture.Prepared(step)
				}
				p.Step(t, step)
				p.Step(t, fixture.Exec("BEGIN READ ONLY", 0))
				step = fixture.ImplicitError("SELECT v FROM t WHERE id=999 "+clause, fixture.Diagnostic{Code: "25006", Message: "cannot execute SELECT " + clause + " in a read-only transaction"})
				step.SelectRows = true
				step.ErrorStage = "execute"
				if prepared {
					step = fixture.Prepared(step)
				}
				p.Step(t, step)
				p.Step(t, fixture.Exec("ROLLBACK", 0))
			})
		}
	}
	p.Step(t, fixture.Select("SELECT last_value FROM s", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(1)}}))
}
func TestReferenceRowLockRepeatableRead(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, clause := range fixture.RowClauses {
			for _, change := range []string{"UPDATE t SET v=20 WHERE id=1", "DELETE FROM t WHERE id=1"} {
				t.Run(clause+"/"+change+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, backend := range p.Backends() {
						t.Run(backend.Name, func(t *testing.T) {
							a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
							recorder := backend.Recorder
							fixture.RunStep(t, p.Ctx, a, recorder, fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
							fixture.RunStep(t, p.Ctx, a, recorder, fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
							tx, err := b.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							fixture.RunStep(t, p.Ctx, tx, recorder, fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
							fixture.RunStep(t, p.Ctx, a, recorder, fixture.Exec(change, 1))
							if clause == "FOR KEY SHARE" && change[0] == 'U' {
								step := fixture.Select("SELECT v FROM t "+clause, []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
								if prepared {
									step = fixture.Prepared(step)
								}
								fixture.RunStep(t, p.Ctx, tx, recorder, step)
								predicate := fixture.Select("SELECT v FROM t WHERE v=10 "+clause, []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
								if prepared {
									predicate = fixture.Prepared(predicate)
								}
								fixture.RunStep(t, p.Ctx, tx, recorder, predicate)
								return
							}
							step := fixture.ImplicitError("SELECT v FROM t "+clause, fixture.Diagnostic{Code: "40001", Message: "could not serialize access due to concurrent update"})
							step.SelectRows = true
							step.ErrorStage = "execute"
							if prepared {
								step = fixture.Prepared(step)
							}
							fixture.RunStep(t, p.Ctx, tx, recorder, step)
							fixture.RunStep(t, p.Ctx, tx, recorder, fixture.ImplicitError("SELECT v FROM t", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
						})
					}
				})
			}
		}
	}
}
