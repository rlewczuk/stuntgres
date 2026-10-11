package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangePartitionBounds(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE rp_tuple(x INT,y BIGINT) PARTITION BY RANGE(x,y)", 0))
		run(fixture.Prepared(fixture.Exec("CREATE TABLE rp_tuple_leaf PARTITION OF rp_tuple FOR VALUES FROM(1,2) TO(3,4)", 0)))
		run(fixture.Exec("INSERT INTO rp_tuple VALUES(1,2),(1,999),(2,-999),(3,3)", 4))
		for _, c := range []struct{ input, detail string }{{"(1,1)", "1, 1"}, {"(3,4)", "3, 4"}, {"(2,NULL)", "2, null"}, {"(NULL,2)", "null, 2"}} {
			run(referenceRangeMiss("INSERT INTO rp_tuple VALUES"+c.input, "rp_tuple", "x, y", c.detail))
		}
		run(fixture.Exec("CREATE TABLE rp_tuple_def PARTITION OF rp_tuple DEFAULT", 0))
		run(fixture.Prepared(fixture.Exec("INSERT INTO rp_tuple VALUES($1,$2)", 1, 2, nil)))
		run(fixture.Exec("INSERT INTO rp_tuple VALUES(1,1),(3,4),(NULL,2)", 3))
		rows := [][]any{{int64(1), int64(2)}, {int64(1), int64(999)}, {int64(2), int64(-999)}, {int64(3), int64(3)}}
		run(fixture.Select("SELECT * FROM rp_tuple_leaf ORDER BY x,y", []string{"x", "y"}, []string{"INT4", "INT8"}, rows))
		run(fixture.Select("SELECT * FROM rp_tuple_def ORDER BY x NULLS LAST,y", []string{"x", "y"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), nil}, {int64(3), int64(4)}, {nil, int64(2)}}))
		run(fixture.Exec("CREATE TABLE rp_sentinel(x INT,y TEXT) PARTITION BY RANGE(x,y)", 0))
		run(fixture.Exec("CREATE TABLE rp_sentinel_leaf PARTITION OF rp_sentinel FOR VALUES FROM(0,MAXVALUE) TO(10,MAXVALUE)", 0))
		run(fixture.Exec("CREATE TABLE rp_sentinel_def PARTITION OF rp_sentinel DEFAULT", 0))
		run(fixture.Exec("INSERT INTO rp_sentinel VALUES(0,'z'),(1,'a'),(10,'z'),(11,'a'),(2,NULL)", 5))
		run(fixture.Select("SELECT * FROM rp_sentinel_leaf ORDER BY x", []string{"x", "y"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(10), "z"}}))
		run(fixture.Select("SELECT * FROM rp_sentinel_def ORDER BY x", []string{"x", "y"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "z"}, {int64(2), nil}, {int64(11), "a"}}))
		run(fixture.Exec("CREATE TABLE rp_min(x INT,y INT) PARTITION BY RANGE(x,y)", 0))
		run(fixture.Exec("CREATE TABLE rp_min_a PARTITION OF rp_min FOR VALUES FROM(MINVALUE,MINVALUE) TO(0,MINVALUE)", 0))
		run(fixture.Exec("CREATE TABLE rp_min_b PARTITION OF rp_min FOR VALUES FROM(0,MINVALUE) TO(MAXVALUE,MAXVALUE)", 0))
		run(fixture.Exec("INSERT INTO rp_min VALUES(-1,999),(0,-999),(1,0)", 3))
		run(fixture.Select("SELECT x FROM rp_min_a", []string{"x"}, []string{"INT4"}, [][]any{{int64(-1)}}))
		run(fixture.Select("SELECT x FROM rp_min_b ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}}))
	})
}
