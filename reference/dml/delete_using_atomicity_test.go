package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteUsingAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, queryRows := range []bool{false, true} {
			name := map[bool]string{false: "direct", true: "prepared"}[prepared] + map[bool]string{false: "/exec", true: "/query"}[queryRows]
			t.Run(name, func(t *testing.T) {
				deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := updateFromRun(t, p, b, c)
					run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
					run(fixture.Exec("CREATE TABLE s(k INT,den INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
					run(fixture.Exec("INSERT INTO s VALUES(1,1),(2,0)", 2))
					q := "DELETE FROM t d USING s WHERE d.id=s.k RETURNING d.id,10/s.den"
					executor := fixture.Executor(c)
					if prepared {
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					}
					step := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
					step.SelectRows = queryRows
					original := deleteUsingTargets([][]any{{int64(1), int64(10)}, {int64(2), int64(20)}})
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
					run(original)
					run(fixture.Select("SELECT k,den FROM s ORDER BY k", []string{"k", "den"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(0)}}))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("UPDATE t SET v=11 WHERE id=1", 1))
					run(fixture.Exec("SAVEPOINT before_using", 0))
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
					run(fixture.ErrorDiagnostic("SELECT id FROM t", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
					run(fixture.Exec("ROLLBACK TO before_using", 0))
					run(deleteUsingTargets([][]any{{int64(1), int64(11)}, {int64(2), int64(20)}}))
					run(fixture.Exec("ROLLBACK", 0))
					run(original)
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO t VALUES(3,30)", 1))
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
					run(fixture.Exec("ROLLBACK", 0))
					run(original)
				})
			})
		}
	}
}
