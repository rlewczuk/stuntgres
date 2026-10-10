package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDeleteUsingAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, queryRows := range []bool{false, true} {
			name := map[bool]string{false: "direct", true: "prepared"}[prepared] + map[bool]string{false: "/exec", true: "/query"}[queryRows]
			t.Run(name, func(t *testing.T) {
				deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
					run := updateFromRun(t, p, b, c)
					run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
					run(referenceExec("CREATE TABLE s(k INT,den INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(1,10),(2,20)", 2))
					run(referenceExec("INSERT INTO s VALUES(1,1),(2,0)", 2))
					q := "DELETE FROM t d USING s WHERE d.id=s.k RETURNING d.id,10/s.den"
					executor := referenceExecutor(c)
					if prepared {
						stmt, err := c.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executor = referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
					}
					step := referenceErrorDiagnostic(q, controlsReferenceDiagnostic("22012", "division by zero"))
					step.selectRows = queryRows
					original := deleteUsingTargets([][]any{{int64(1), int64(10)}, {int64(2), int64(20)}})
					referenceRunStep(t, p.ctx, executor, b.recorder, step)
					run(original)
					run(referenceSelect("SELECT k,den FROM s ORDER BY k", []string{"k", "den"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(0)}}))
					run(referenceExec("BEGIN", 0))
					run(referenceExec("UPDATE t SET v=11 WHERE id=1", 1))
					run(referenceExec("SAVEPOINT before_using", 0))
					referenceRunStep(t, p.ctx, executor, b.recorder, step)
					run(referenceErrorDiagnostic("SELECT id FROM t", controlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
					run(referenceExec("ROLLBACK TO before_using", 0))
					run(deleteUsingTargets([][]any{{int64(1), int64(11)}, {int64(2), int64(20)}}))
					run(referenceExec("ROLLBACK", 0))
					run(original)
					run(referenceExec("BEGIN", 0))
					run(referenceExec("INSERT INTO t VALUES(3,30)", 1))
					referenceRunStep(t, p.ctx, executor, b.recorder, step)
					run(referenceExec("ROLLBACK", 0))
					run(original)
				})
			})
		}
	}
}
