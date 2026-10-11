package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOrderSecondKeyNulls(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,a INT,b INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,1,NULL),(2,1,2),(3,1,1),(4,NULL,NULL),(5,NULL,3)", 5))
	for _, tc := range []struct {
		policy string
		ids    []int64
	}{
		{"ASC NULLS FIRST", []int64{4, 5, 1, 3, 2}},
		{"ASC NULLS LAST", []int64{5, 4, 3, 2, 1}},
		{"DESC NULLS FIRST", []int64{4, 5, 1, 2, 3}},
		{"DESC NULLS LAST", []int64{5, 4, 2, 3, 1}},
	} {
		want := make([][]any, len(tc.ids))
		for i, id := range tc.ids {
			want[i] = []any{id}
		}
		step := fixture.Select("SELECT id FROM t ORDER BY a ASC NULLS FIRST,b "+tc.policy+",id", []string{"id"}, []string{"INT4"}, want)
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
}
