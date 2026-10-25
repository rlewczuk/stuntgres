package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceHashPartitionRouting(t *testing.T) {
	referenceHashScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := referenceHashRun(t, p, b, prepared)
		// Literal REL_18_0 support-function predictions, not native-generated partitions.
		keys := []any{nil, int64(-2147483648), int64(-10), int64(-2), int64(-1), int64(0), int64(1), int64(2), int64(3), int64(4), int64(5), int64(6), int64(7), int64(8), int64(9), int64(10), int64(17), int64(42), int64(2147483647)}
		for _, vector := range []struct {
			m         int
			remainder []int
		}{
			{4, []int{0, 2, 3, 2, 1, 0, 0, 2, 1, 3, 1, 3, 3, 1, 1, 3, 0, 2, 3}},
			{8, []int{0, 6, 7, 2, 5, 0, 0, 2, 1, 3, 5, 3, 7, 5, 5, 7, 4, 2, 7}},
			{3, []int{0, 0, 0, 1, 2, 2, 2, 0, 1, 0, 2, 0, 1, 0, 2, 1, 2, 0, 1}},
		} {
			name := fmt.Sprintf("hp_route%d", vector.m)
			run(fixture.Exec("CREATE TABLE "+name+"(k INT,id INT) PARTITION BY HASH(k)", 0))
			referenceHashChildren(run, name, vector.m)
			q := "INSERT INTO " + name + " VALUES($1,$2)"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
			for i, k := range keys {
				s := fixture.Exec(q, 1, k, i)
				if prepared {
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				} else {
					run(s)
				}
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			for r := range vector.m {
				var want [][]any
				for i, got := range vector.remainder {
					if got == r {
						want = append(want, []any{int64(i)})
					}
				}
				run(fixture.Select(fmt.Sprintf("SELECT id FROM %s_%d ORDER BY id", name, r), []string{"id"}, []string{"INT4"}, want))
			}
			run(fixture.Select("SELECT count(*) FROM "+name, []string{"count"}, []string{"INT8"}, [][]any{{int64(len(keys))}}))
			run(fixture.Select("SELECT id FROM ONLY "+name, []string{"id"}, []string{"INT4"}, nil))
		}
		run(fixture.Exec("CREATE TABLE hp_pair(a INT,b INT,id INT) PARTITION BY HASH(a,b)", 0))
		referenceHashChildren(run, "hp_pair", 8)
		run(fixture.Exec("INSERT INTO hp_pair VALUES(NULL,NULL,0),(0,NULL,1),(NULL,0,2),(0,0,3),(0,1,4),(1,0,5),(1,2,6),(2,1,7),(-1,42,8)", 9))
		for r, ids := range [][]any{{int64(0), int64(1), int64(2), int64(7)}, {int64(6)}, nil, {int64(3), int64(4)}, {int64(8)}, nil, nil, {int64(5)}} {
			var want [][]any
			for _, id := range ids {
				want = append(want, []any{id})
			}
			run(fixture.Select(fmt.Sprintf("SELECT id FROM hp_pair_%d ORDER BY id", r), []string{"id"}, []string{"INT4"}, want))
		}
		run(referenceRangeLeafMiss("INSERT INTO hp_route4_0 VALUES(2,100)", "hp_route4_0", "2, 100"))
		run(fixture.Exec("CREATE TABLE hp_outer(k INT,payload INT) PARTITION BY RANGE(payload)", 0))
		run(fixture.Exec("CREATE TABLE hp_middle PARTITION OF hp_outer FOR VALUES FROM(0) TO(10) PARTITION BY HASH(k)", 0))
		run(fixture.Exec("CREATE TABLE hp_inner PARTITION OF hp_middle FOR VALUES WITH(MODULUS 4,REMAINDER 0) PARTITION BY RANGE(payload)", 0))
		run(fixture.Exec("CREATE TABLE hp_nested_leaf PARTITION OF hp_inner FOR VALUES FROM(0) TO(10)", 0))
		run(fixture.Exec("INSERT INTO hp_outer VALUES(1,2)", 1))
		run(referenceRangeRows("SELECT k FROM hp_nested_leaf", 1))
		run(referenceRangeMiss("INSERT INTO hp_outer VALUES(2,2)", "hp_middle", "k", "2"))
		run(referenceRangeLeafMiss("INSERT INTO hp_nested_leaf VALUES(2,2)", "hp_nested_leaf", "2, 2"))
		run(referenceRangeRows("SELECT k FROM hp_outer", 1))
	})
}

func TestReferenceHashPartitionMetadata(t *testing.T) {
	referenceHashScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := referenceHashRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE hp_meta(k INT,payload TEXT) PARTITION BY HASH(k)", 0))
		referenceHashChildren(run, "hp_meta", 4)
		for _, q := range []string{"SELECT k,payload FROM hp_meta", "SELECT k,payload FROM ONLY hp_meta", "INSERT INTO hp_meta VALUES(1,'one') RETURNING k,payload"} {
			var want [][]any
			if q[0] == 'I' {
				want = [][]any{{int64(1), "one"}}
			}
			s := fixture.Select(q, []string{"k", "payload"}, []string{"INT4", "TEXT"}, want)
			s.CheckMetadata = referenceRangeMetadata(b.PG, s.Columns, s.Types, []bool{true, true})
			run(s)
		}
	})
}
