package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func updateCurrentOfReference(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	updateFromReference(t, body)
}

func TestReferenceUpdateCurrentOfPositionedRowAndCounts(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE t(id INT,a INT,b INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10,20),(2,30,40),(3,50,60)", 3))
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("DECLARE c CURSOR FOR SELECT 99 AS projected FROM t WHERE id<4 ORDER BY id FOR UPDATE", 0))
				run(fixture.Select("FETCH FORWARD 1 FROM c", []string{"projected"}, []string{"INT4"}, [][]any{{int64(99)}}))
				for _, q := range []string{"UPDATE t SET a=b,b=a WHERE CURRENT OF c", "UPDATE t SET a=a WHERE CURRENT OF c", "UPDATE t SET id=99 WHERE CURRENT OF c"} {
					s := fixture.Exec(q, 1)
					s.Prepared = prepared
					run(s)
				}
				s := fixture.Select("UPDATE t SET a=a+1 WHERE CURRENT OF c RETURNING id,old.a AS before_a,new.a AS after_a,'owned'::text AS label", []string{"id", "before_a", "after_a", "label"}, []string{"INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(99), int64(20), int64(21), "owned"}})
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT id,a,b FROM t ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(30), int64(40)}, {int64(3), int64(50), int64(60)}, {int64(99), int64(21), int64(10)}}))
				run(fixture.Select("FETCH FORWARD 2 FROM c", []string{"projected"}, []string{"INT4"}, [][]any{{int64(99)}, {int64(99)}}))
				s = fixture.Exec("UPDATE t SET a=a+1 WHERE CURRENT OF c", 1)
				s.Prepared = prepared
				run(s)
				s = fixture.Select("UPDATE t SET a=a WHERE CURRENT OF c", []string{}, []string{}, nil)
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT id,a,b FROM t ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(30), int64(40)}, {int64(3), int64(51), int64(60)}, {int64(99), int64(21), int64(10)}}))
			})
		})
	}
}

func TestReferenceUpdateCurrentOfIdentityAndPreparation(t *testing.T) {
	for _, projection := range []string{"42 AS projected", ""} {
		t.Run("duplicates/"+projection, func(t *testing.T) {
			updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE t(v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(7),(7)", 2))
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("DECLARE c CURSOR FOR SELECT "+projection+" FROM t", 0))
				fetch := fixture.Select("FETCH FORWARD 1 FROM c", []string{}, []string{}, [][]any{{}})
				if projection != "" {
					fetch = fixture.Select(fetch.Query, []string{"projected"}, []string{"INT4"}, [][]any{{int64(42)}})
				}
				run(fetch)
				run(fixture.Exec("UPDATE t SET v=9 WHERE CURRENT OF c", 1))
				duplicate := fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}})
				duplicate.Bag = true
				run(duplicate)
			})
		})
	}
	updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		currentOfSeed(run)
		for _, name := range []string{"MiX", "é"} {
			q := `UPDATE t SET v=$1 WHERE CURRENT OF "` + name + `" RETURNING id,v`
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("prepare before DECLARE", err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT missing", 0))
			r, e := stmt.ExecContext(p.Ctx, "first")
			if r != nil {
				t.Fatal("missing cursor exposed result")
			}
			fixture.AssertDiagnostic(t, e, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "34000", Message: `cursor "` + name + `" does not exist`})
			run(fixture.Exec("ROLLBACK TO missing", 0))
			for _, id := range []int64{1, 2} {
				run(fixture.Exec(`DECLARE "`+name+`" CURSOR FOR SELECT 42 AS projected FROM t WHERE id=$1`, 0, id))
				run(fixture.Select(`FETCH FORWARD 1 FROM "`+name+`"`, []string{"projected"}, []string{"INT4"}, [][]any{{int64(42)}}))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{id, "fresh"}}, "fresh"))
				run(fixture.Exec(`CLOSE "`+name+`"`, 0))
			}
			run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "fresh"}, {int64(2), "fresh"}, {int64(3), "three"}}))
			run(fixture.Exec("ROLLBACK", 0))
			if err = stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("permissions", updateCurrentOfAuthority)
}
