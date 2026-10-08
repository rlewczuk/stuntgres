package reference_test

import "testing"

func TestReferenceInsertSelectScalarSource(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { insertSelectRun(t, p.ctx, backend.db, backend.recorder, s) }
			for _, s := range []referenceStep{referenceExec("CREATE TABLE s(i INT,v INT,b BOOL)", 0), referenceExec("INSERT INTO s VALUES(1,NULL,TRUE),(2,3,FALSE)", 2), referenceExec("CREATE TABLE t(i BIGINT,b BOOL)", 0)} {
				run(s)
			}
			for _, prepared := range []bool{false, true} {
				s := referenceExec("INSERT INTO t SELECT num_nulls(v),NOT b FROM s WHERE i>0 ORDER BY i", 2)
				s.prepared = prepared
				run(s)
			}
			result := referenceSelect("SELECT * FROM t", []string{"i", "b"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}, {int64(0), true}, {int64(1), false}, {int64(0), true}})
			result.bag = true
			run(result)
			run(referenceExec("INSERT INTO t SELECT num_nulls(v),NOT b FROM s WHERE FALSE", 0))
			run(result)
		})
	}
}
