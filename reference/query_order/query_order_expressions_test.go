package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOrderScalarConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,i INT,j INT,a BOOL,b BOOL)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,1,2,TRUE,FALSE),(2,3,1,FALSE,TRUE),(3,NULL,2,NULL,NULL)", 3))
	for _, tc := range []struct {
		query, label, typ string
		want              [][]any
	}{
		{"SELECT id,i<j AS x FROM t ORDER BY x DESC NULLS LAST,id", "x", "BOOL", [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}}},
		{"SELECT id,i IS NULL result FROM t ORDER BY result DESC,id", "result", "BOOL", [][]any{{int64(3), true}, {int64(1), false}, {int64(2), false}}},
		{"SELECT id,a AND a AS result FROM t ORDER BY a AND a DESC NULLS LAST,id", "result", "BOOL", [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}}},
		{"SELECT id,num_nulls(i) AS n FROM t ORDER BY n DESC,id", "n", "INT4", [][]any{{int64(3), int64(1)}, {int64(1), int64(0)}, {int64(2), int64(0)}}},
		{"SELECT id,i BETWEEN 1 AND 2 AS x FROM t ORDER BY i BETWEEN 1 AND 2 DESC NULLS LAST,id", "x", "BOOL", [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}}},
		{"SELECT id,CAST(i AS int4) cast_value FROM t ORDER BY cast_value DESC NULLS LAST,id", "cast_value", "INT4", [][]any{{int64(2), int64(3)}, {int64(1), int64(1)}, {int64(3), nil}}},
		{"SELECT id,i::int4 AS renamed FROM t ORDER BY renamed DESC NULLS LAST,id", "renamed", "INT4", [][]any{{int64(2), int64(3)}, {int64(1), int64(1)}, {int64(3), nil}}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			step := fixture.Select(tc.query, []string{"id", tc.label}, []string{"INT4", tc.typ}, tc.want)
			p.Step(t, step)
			p.Step(t, fixture.Prepared(step))
		})
	}
}
