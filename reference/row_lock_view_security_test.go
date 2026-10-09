package reference_test

import "testing"

func TestReferenceRowLockViewSecurity(t *testing.T) {
	p := newReferenceScenario(t)
	for _, s := range []referenceStep{referenceExec("CREATE TABLE text_source(id INT,v TEXT)", 0), referenceExec("INSERT INTO text_source VALUES(1,'bad'),(2,'7')", 2), referenceExec("CREATE VIEW plain_view WITH(security_barrier=false) AS SELECT id,v FROM text_source WHERE id=2", 0), referenceExec("CREATE VIEW barrier_view WITH(security_barrier=true) AS SELECT id,v FROM text_source WHERE id=2", 0)} {
		p.step(t, s)
	}
	for _, name := range []string{"plain_view", "barrier_view"} {
		for _, clause := range referenceRowClauses {
			for _, prepared := range []bool{false, true} {
				step := referenceSelect("SELECT CAST(v AS INT) AS value FROM "+name+" "+clause, []string{"value"}, []string{"INT4"}, [][]any{{int64(7)}})
				step.prepared = prepared
				p.step(t, step)
			}
		}
	}
	// This qualification/projection result is shared. Planner placement of marks
	// relative to a security barrier is not asserted as native resource parity.
}
