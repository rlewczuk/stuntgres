package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateCurrentOfFeatureSmoke(t *testing.T) {
	updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE t(id INT,a INT,b INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10,20),(2,30,40),(3,50,60)", 3))
		q := `UPDATE t SET a=b,b=a WHERE CURRENT OF "MiX" RETURNING id,old.a AS before_a,new.a AS after_a`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec(`DECLARE "MiX" NO SCROLL CURSOR FOR SELECT 99 AS projected FROM t ORDER BY id FOR UPDATE`, 0))
		run(fixture.Exec("SAVEPOINT before_motion", 0))
		run(fixture.Select(`FETCH FORWARD 1 FROM "MiX"`, []string{"projected"}, []string{"INT4"}, [][]any{{int64(99)}}))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"id", "before_a", "after_a"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(20)}}))
		run(fixture.Select("SELECT id,a,b FROM t ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(20), int64(10)}, {int64(2), int64(30), int64(40)}, {int64(3), int64(50), int64(60)}}))
		run(fixture.Exec("ROLLBACK TO before_motion", 0))
		run(fixture.Exec(`UPDATE t SET a=a+1 WHERE CURRENT OF "MiX"`, 1))
		run(fixture.Select(`FETCH FORWARD 2 FROM "MiX"`, []string{"projected"}, []string{"INT4"}, [][]any{{int64(99)}, {int64(99)}}))
		run(fixture.Exec(`UPDATE t SET a=a+1 WHERE CURRENT OF "MiX"`, 1))
		run(fixture.Exec(`CLOSE "MiX"`, 0))
		run(fixture.Exec("COMMIT", 0))
		expected := fixture.Select("SELECT id,a,b FROM t ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(11), int64(20)}, {int64(2), int64(30), int64(40)}, {int64(3), int64(51), int64(60)}})
		run(expected)
		run(fixture.Exec("BEGIN", 0))
		currentOfFailure(t, p, b, c, currentOfError(q, "34000", `cursor "MiX" does not exist`, false))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Exec("CREATE TABLE t_dup(v INT)", 0))
		run(fixture.Exec("INSERT INTO t_dup VALUES(7),(7)", 2))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DECLARE dup CURSOR FOR SELECT 42 AS projected FROM t_dup", 0))
		run(fixture.Select("FETCH FORWARD 1 FROM dup", []string{"projected"}, []string{"INT4"}, [][]any{{int64(42)}}))
		run(fixture.Exec("UPDATE t_dup SET v=9 WHERE CURRENT OF dup", 1))
		run(fixture.Exec("COMMIT", 0))
		duplicate := fixture.Select("SELECT v FROM t_dup", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}})
		duplicate.Bag = true
		run(duplicate)
		if !b.PG {
			var dump bytes.Buffer
			if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			connector, e := stuntgres.NewDriver().OpenConnector(t.Name() + "/restored")
			if e != nil {
				t.Fatal(e)
			}
			restored := sql.OpenDB(connector)
			defer restored.Close()
			if err = stuntgres.Restore(p.Ctx, restored, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, restored, b.Recorder, expected)
			fixture.RunStep(t, p.Ctx, restored, b.Recorder, duplicate)
			fixture.DeleteReferenceExecuteStep(t, p, restored, b.Recorder, currentOfError(`UPDATE t SET a=0 WHERE CURRENT OF "MiX"`, "34000", `cursor "MiX" does not exist`, false))
		}
	})
}
