package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteUsingHeldCommandRefresh(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("CREATE TABLE s(k INT,n INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20),(3,30)", 3))
		run(fixture.Exec("INSERT INTO s VALUES(1,101)", 1))
		q := "DELETE FROM t d USING s WHERE d.id=s.k AND d.id=$1 RETURNING old.v,s.n,new.id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		step := fixture.Select(q, []string{"v", "n", "id"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(10), int64(101), nil}}, 1)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		run(fixture.Exec("COMMENT ON TABLE s IS 'compatible refresh'", 0))
		run(fixture.Exec("COMMENT ON COLUMN t.v IS 'compatible target'", 0))
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("INSERT INTO s VALUES(2,202)", 1))
		step.Args = []any{2}
		step.Rows = [][]any{{int64(20), int64(202), nil}}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		run(fixture.Exec("DELETE FROM s WHERE k=1", 1))
		run(fixture.Exec("INSERT INTO t VALUES(1,11)", 1))
		step.Args = []any{1}
		step.Rows = nil
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		run(deleteUsingTargets([][]any{{int64(1), int64(11)}, {int64(3), int64(30)}}))
		run(fixture.Exec("ROLLBACK", 0))
		run(deleteUsingTargets([][]any{{int64(2), int64(20)}, {int64(3), int64(30)}}))
		run(fixture.Select("SELECT k,n FROM s", []string{"k", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(101)}}))
	})
}

func TestReferenceDeleteUsingSourceFailure(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, queryRows := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct", true: "held"}[prepared]+map[bool]string{false: "/exec", true: "/query"}[queryRows], func(t *testing.T) {
				deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := updateFromRun(t, p, b, c)
					run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
					run(fixture.Exec("CREATE TABLE s(k INT,label TEXT)", 0))
					run(fixture.Exec("INSERT INTO s VALUES(1,'1'),(2,'bad')", 2))
					q := "DELETE FROM t d USING (SELECT k,label FROM s WHERE int4(label)>0) x WHERE d.id=x.k RETURNING d.id,x.label"
					executor := fixture.Executor(c)
					if prepared {
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					}
					step := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`))
					step.SelectRows = queryRows
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
					run(deleteUsingTargets([][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
					run(fixture.Select("SELECT k,label FROM s ORDER BY k", []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "1"}, {int64(2), "bad"}}))
				})
			})
		}
	}
}
