package reference_test

import "testing"

func TestReferenceRowLockViewFailingFirst(t *testing.T) {
	p := newReferenceScenario(t)
	for _, step := range []referenceStep{referenceExec("CREATE TABLE t(id INT,v INT)", 0), referenceExec("INSERT INTO t VALUES(1,10),(2,20)", 2), referenceExec("CREATE VIEW v AS SELECT id,v FROM t", 0)} {
		p.step(t, step)
	}
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, b := referenceLockConnections(t, p.ctx, backend.db)
			for _, clause := range referenceRowClauses {
				for _, prepared := range []bool{false, true} {
					t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						step := referenceSelect("SELECT v FROM v WHERE id=1 "+clause, []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
						step.prepared = prepared
						referenceRunStep(t, p.ctx, a, backend.recorder, step)
						holder, err := a.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						referenceRunStep(t, p.ctx, holder, backend.recorder, step)
						referenceRunStep(t, p.ctx, b, backend.recorder, referenceSelect("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
					})
				}
			}
		})
	}
}
