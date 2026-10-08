package reference_test

import "testing"

func TestReferenceQueryOrderVolatileOccurrences(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE v(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO v VALUES(1),(2),(3)", 3))
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			q    string
			rows [][]any
			next int64
		}{
			{"SELECT nextval('s') AS n,nextval('s') AS m FROM v ORDER BY n DESC", [][]any{{int64(3), int64(3)}, {int64(2), int64(2)}, {int64(1), int64(1)}}, 4},
			{"SELECT nextval('s') AS n,nextval('s') AS m FROM v ORDER BY id DESC", [][]any{{int64(1), int64(2)}, {int64(3), int64(4)}, {int64(5), int64(6)}}, 7},
		} {
			t.Run(tc.q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p.step(t, referenceExec("CREATE SEQUENCE s START WITH 1", 0))
				step := referenceSelect(tc.q, []string{"n", "m"}, []string{"INT8", "INT8"}, tc.rows)
				step.prepared = prepared
				p.step(t, step)
				p.step(t, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{tc.next}}))
				p.step(t, referenceExec("DROP SEQUENCE s", 0))
			})
		}
	}
}
