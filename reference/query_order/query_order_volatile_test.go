package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOrderVolatileOccurrences(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE v(id INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO v VALUES(1),(2),(3)", 3))
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
				p.Step(t, fixture.Exec("CREATE SEQUENCE s START WITH 1", 0))
				step := fixture.Select(tc.q, []string{"n", "m"}, []string{"INT8", "INT8"}, tc.rows)
				step.Prepared = prepared
				p.Step(t, step)
				p.Step(t, fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{tc.next}}))
				p.Step(t, fixture.Exec("DROP SEQUENCE s", 0))
			})
		}
	}
}
