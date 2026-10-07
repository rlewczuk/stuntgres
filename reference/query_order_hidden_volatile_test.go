//go:build reference

package reference_test

import "testing"

func TestReferenceQueryOrderHiddenVolatileAdmission(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE v(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO v VALUES(1),(2),(3),(4)", 4))
	for _, prepared := range []bool{false, true} {
		p.step(t, referenceExec("CREATE SEQUENCE s START WITH 1", 0))
		step := referenceSelect("SELECT id FROM v WHERE id>2 ORDER BY nextval('s') DESC", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(4)}})
		// Only generator values order this query; input traversal is unspecified.
		// Assert the admitted occurrence bag and exactly two generator effects.
		step.bag = true
		step.prepared = prepared
		p.step(t, step)
		p.step(t, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
		p.step(t, referenceExec("DROP SEQUENCE s", 0))
	}
}
