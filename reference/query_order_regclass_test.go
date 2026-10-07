//go:build reference

package reference_test

import "testing"

func TestReferenceQueryOrderRegclass(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT)", 0))
	query := "SELECT id FROM t ORDER BY CASE id WHEN 1 THEN '4294967295'::regclass WHEN 2 THEN '1'::regclass ELSE '2147483648'::regclass END"
	step := referenceSelect(query, []string{"id"}, []string{"INT4"}, nil)
	p.step(t, step)
	p.step(t, referencePrepared(step))
	p.step(t, referenceExec("INSERT INTO t VALUES(1),(2),(3)", 3))
	step.rows = [][]any{{int64(2)}, {int64(3)}, {int64(1)}}
	p.step(t, step)
	p.step(t, referencePrepared(step))
	p.step(t, referenceSelect("SELECT NULL::regclass AS r FROM t ORDER BY r,id", []string{"r"}, []string{"REGCLASS"}, [][]any{{nil}, {nil}, {nil}}))
}
