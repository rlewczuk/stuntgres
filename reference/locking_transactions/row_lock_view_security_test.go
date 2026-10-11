package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowLockViewSecurity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE text_source(id INT,v TEXT)", 0), fixture.Exec("INSERT INTO text_source VALUES(1,'bad'),(2,'7')", 2), fixture.Exec("CREATE VIEW plain_view WITH(security_barrier=false) AS SELECT id,v FROM text_source WHERE id=2", 0), fixture.Exec("CREATE VIEW barrier_view WITH(security_barrier=true) AS SELECT id,v FROM text_source WHERE id=2", 0)} {
		p.Step(t, s)
	}
	for _, name := range []string{"plain_view", "barrier_view"} {
		for _, clause := range fixture.RowClauses {
			for _, prepared := range []bool{false, true} {
				step := fixture.Select("SELECT CAST(v AS INT) AS value FROM "+name+" "+clause, []string{"value"}, []string{"INT4"}, [][]any{{int64(7)}})
				step.Prepared = prepared
				p.Step(t, step)
			}
		}
	}
	// This qualification/projection result is shared. Planner placement of marks
	// relative to a security barrier is not asserted as native resource parity.
}
