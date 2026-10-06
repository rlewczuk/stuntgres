//go:build reference

package reference_test

import "testing"

func TestReferenceInsertSelectScalarSource(t *testing.T) {
	p := newReferencePair(t)
	for _, s := range []referenceStep{referenceExec("CREATE TABLE s(i INT,v INT,b BOOL)", 0), referenceExec("INSERT INTO s VALUES(1,NULL,TRUE),(2,3,FALSE)", 2), referenceExec("CREATE TABLE t(i BIGINT,b BOOL)", 0)} {
		p.step(t, s)
	}
	for _, prepared := range []bool{false, true} {
		s := referenceExec("INSERT INTO t SELECT num_nulls(v),NOT b FROM s WHERE i>0 ORDER BY i", 2)
		s.prepared = prepared
		p.step(t, s)
	}
	result := referenceSelect("SELECT * FROM t", []string{"i", "b"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}, {int64(0), true}, {int64(1), false}, {int64(0), true}})
	result.bag = true
	p.step(t, result)
	p.step(t, referenceExec("INSERT INTO t SELECT num_nulls(v),NOT b FROM s WHERE FALSE", 0))
	p.step(t, result)
}
