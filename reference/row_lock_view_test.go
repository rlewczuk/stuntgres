//go:build reference

package reference_test

import "testing"

func TestReferenceRowLockViewFailingFirst(t *testing.T) {
	p := newReferencePair(t)
	for _, step := range []referenceStep{referenceExec("CREATE TABLE t(id INT,v INT)", 0), referenceExec("INSERT INTO t VALUES(1,10),(2,20)", 2), referenceExec("CREATE VIEW v AS SELECT id,v FROM t", 0)} {
		p.step(t, step)
	}
	t.Run("postgres_first", func(t *testing.T) {
		for _, clause := range referenceRowClauses {
			step := referenceSelect("SELECT v FROM v WHERE id=1 "+clause, []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, step)
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referencePrepared(step))
		}
	})
	t.Run("paired", func(t *testing.T) {
		for _, clause := range referenceRowClauses {
			step := referenceSelect("SELECT v FROM v WHERE id=1 "+clause, []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
			p.step(t, step)
			p.step(t, referencePrepared(step))
		}
	})
}
