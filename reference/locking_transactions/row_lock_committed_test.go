package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowLockReadCommitted(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, clause := range fixture.RowClauses {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
						fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
						fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,10)", 1)
						fixture.LockExec(t, p.Ctx, a, "UPDATE t SET v=20 WHERE id=1", 1)
						fixture.LimitLockValues(t, fixture.RowQuery(p.Ctx, b, "SELECT v FROM t WHERE id=1 "+clause, prepared), backend.PG, 20)
						fixture.LimitLockValues(t, fixture.RowQuery(p.Ctx, b, "SELECT v FROM t WHERE v=10 "+clause, prepared), backend.PG)
						fixture.LockExec(t, p.Ctx, a, "DELETE FROM t WHERE id=1", 1)
						fixture.LimitLockValues(t, fixture.RowQuery(p.Ctx, b, "SELECT v FROM t WHERE id=1 "+clause, prepared), backend.PG)
					})
				}
			})
		}
	}
	if *fixture.Mode {
		referenceRowReadCommittedWaits(t)
	}
}

func referenceRowTextJoins(t *testing.T) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, step := range []fixture.Step{
		fixture.Exec("CREATE TABLE a(l TEXT,k INT,q INT)", 0),
		fixture.Exec("CREATE TABLE b(r TEXT,q INT,k INT)", 0),
		fixture.Exec("CREATE TABLE c(k INT,c TEXT)", 0),
		fixture.Exec("CREATE TABLE empty(r TEXT,q INT,k INT)", 0),
		fixture.Exec("CREATE TABLE wide(k BIGINT)", 0),
		fixture.Exec("INSERT INTO a VALUES('a',1,9),('a',1,9),('n',NULL,9),('z',2,8)", 4),
		fixture.Exec("INSERT INTO b VALUES('x',9,1),('x',9,1),('y',8,3),('n',9,NULL)", 4),
		fixture.Exec("INSERT INTO c VALUES(1,'c'),(1,'c')", 2),
		fixture.Exec("INSERT INTO wide VALUES(1)", 1),
	} {
		p.Step(t, step)
	}
	for _, query := range []string{"SELECT a.l,b.r FROM a JOIN b ON a.k=b.k FOR UPDATE", "SELECT a.l,b.r FROM a JOIN b USING(k) FOR NO KEY UPDATE", "SELECT a.l,b.r FROM a NATURAL JOIN b FOR SHARE", "SELECT a.l,b.r FROM a,b WHERE a.k=b.k FOR KEY SHARE"} {
		for _, prepared := range []bool{false, true} {
			t.Run("original_text/"+query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				step := fixture.Select(query, []string{"l", "r"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"a", "x"}, {"a", "x"}, {"a", "x"}})
				step.Bag = true
				if prepared {
					step = fixture.Prepared(step)
				}
				p.Step(t, step)
			})
		}
	}
}
