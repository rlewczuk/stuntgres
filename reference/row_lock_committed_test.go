package reference_test

import "testing"

func TestReferenceRowLockReadCommitted(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, clause := range referenceRowClauses {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						a, b := referenceLockConnections(t, p.ctx, backend.db)
						referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
						referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10)", 1)
						referenceLockExec(t, p.ctx, a, "UPDATE t SET v=20 WHERE id=1", 1)
						referenceLimitLockValues(t, referenceRowQuery(p.ctx, b, "SELECT v FROM t WHERE id=1 "+clause, prepared), backend.pg, 20)
						referenceLimitLockValues(t, referenceRowQuery(p.ctx, b, "SELECT v FROM t WHERE v=10 "+clause, prepared), backend.pg)
						referenceLockExec(t, p.ctx, a, "DELETE FROM t WHERE id=1", 1)
						referenceLimitLockValues(t, referenceRowQuery(p.ctx, b, "SELECT v FROM t WHERE id=1 "+clause, prepared), backend.pg)
					})
				}
			})
		}
	}
	if *referenceMode {
		referenceRowReadCommittedWaits(t)
	}
}

func referenceRowTextJoins(t *testing.T) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE a(l TEXT,k INT,q INT)", 0),
		referenceExec("CREATE TABLE b(r TEXT,q INT,k INT)", 0),
		referenceExec("CREATE TABLE c(k INT,c TEXT)", 0),
		referenceExec("CREATE TABLE empty(r TEXT,q INT,k INT)", 0),
		referenceExec("CREATE TABLE wide(k BIGINT)", 0),
		referenceExec("INSERT INTO a VALUES('a',1,9),('a',1,9),('n',NULL,9),('z',2,8)", 4),
		referenceExec("INSERT INTO b VALUES('x',9,1),('x',9,1),('y',8,3),('n',9,NULL)", 4),
		referenceExec("INSERT INTO c VALUES(1,'c'),(1,'c')", 2),
		referenceExec("INSERT INTO wide VALUES(1)", 1),
	} {
		p.step(t, step)
	}
	for _, query := range []string{"SELECT a.l,b.r FROM a JOIN b ON a.k=b.k FOR UPDATE", "SELECT a.l,b.r FROM a JOIN b USING(k) FOR NO KEY UPDATE", "SELECT a.l,b.r FROM a NATURAL JOIN b FOR SHARE", "SELECT a.l,b.r FROM a,b WHERE a.k=b.k FOR KEY SHARE"} {
		for _, prepared := range []bool{false, true} {
			t.Run("original_text/"+query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				step := referenceSelect(query, []string{"l", "r"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"a", "x"}, {"a", "x"}, {"a", "x"}})
				step.bag = true
				if prepared {
					step = referencePrepared(step)
				}
				p.step(t, step)
			})
		}
	}
}
