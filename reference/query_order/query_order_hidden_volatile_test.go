//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOrderHiddenVolatileAdmission(t *testing.T) {
	p := fixture.NewPair(t)
	p.Step(t, fixture.Exec("CREATE TABLE v(id INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO v VALUES(1),(2),(3),(4)", 4))
	for _, prepared := range []bool{false, true} {
		p.Step(t, fixture.Exec("CREATE SEQUENCE s START WITH 1", 0))
		step := fixture.Select("SELECT id FROM v WHERE id>2 ORDER BY nextval('s') DESC", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(4)}})
		// Only generator values order this query; input traversal is unspecified.
		// Assert the admitted occurrence bag and exactly two generator effects.
		step.Bag = true
		step.Prepared = prepared
		p.Step(t, step)
		p.Step(t, fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
		p.Step(t, fixture.Exec("DROP SEQUENCE s", 0))
	}
}
