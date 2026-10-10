package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDeleteUsingHeldCommandRefresh(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
		run(referenceExec("CREATE TABLE s(k INT,n INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,10),(2,20),(3,30)", 3))
		run(referenceExec("INSERT INTO s VALUES(1,101)", 1))
		q := "DELETE FROM t d USING s WHERE d.id=s.k AND d.id=$1 RETURNING old.v,s.n,new.id"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		step := referenceSelect(q, []string{"v", "n", "id"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(10), int64(101), nil}}, 1)
		referenceRunStep(t, p.ctx, held, b.recorder, step)
		run(referenceExec("COMMENT ON TABLE s IS 'compatible refresh'", 0))
		run(referenceExec("COMMENT ON COLUMN t.v IS 'compatible target'", 0))
		run(referenceExec("BEGIN", 0))
		defer c.ExecContext(p.ctx, "ROLLBACK")
		run(referenceExec("INSERT INTO s VALUES(2,202)", 1))
		step.args = []any{2}
		step.rows = [][]any{{int64(20), int64(202), nil}}
		referenceRunStep(t, p.ctx, held, b.recorder, step)
		run(referenceExec("DELETE FROM s WHERE k=1", 1))
		run(referenceExec("INSERT INTO t VALUES(1,11)", 1))
		step.args = []any{1}
		step.rows = nil
		referenceRunStep(t, p.ctx, held, b.recorder, step)
		run(deleteUsingTargets([][]any{{int64(1), int64(11)}, {int64(3), int64(30)}}))
		run(referenceExec("ROLLBACK", 0))
		run(deleteUsingTargets([][]any{{int64(2), int64(20)}, {int64(3), int64(30)}}))
		run(referenceSelect("SELECT k,n FROM s", []string{"k", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(101)}}))
	})
}

func TestReferenceDeleteUsingSourceFailure(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, queryRows := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct", true: "held"}[prepared]+map[bool]string{false: "/exec", true: "/query"}[queryRows], func(t *testing.T) {
				deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
					run := updateFromRun(t, p, b, c)
					run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(1,10),(2,20)", 2))
					run(referenceExec("CREATE TABLE s(k INT,label TEXT)", 0))
					run(referenceExec("INSERT INTO s VALUES(1,'1'),(2,'bad')", 2))
					q := "DELETE FROM t d USING (SELECT k,label FROM s WHERE int4(label)>0) x WHERE d.id=x.k RETURNING d.id,x.label"
					executor := referenceExecutor(c)
					if prepared {
						stmt, err := c.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executor = referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
					}
					step := referenceErrorDiagnostic(q, controlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`))
					step.selectRows = queryRows
					referenceRunStep(t, p.ctx, executor, b.recorder, step)
					run(deleteUsingTargets([][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
					run(referenceSelect("SELECT k,label FROM s ORDER BY k", []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "1"}, {int64(2), "bad"}}))
				})
			})
		}
	}
}
