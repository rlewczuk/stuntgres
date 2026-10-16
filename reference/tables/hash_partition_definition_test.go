package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func referenceHashScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, bool)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) { body(t, p, b, prepared) })
			}
		})
	}
}

func referenceHashRun(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) func(fixture.Step) {
	t.Helper()
	return func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
}

func referenceHashChildren(run func(fixture.Step), name string, modulus int) {
	for r := range modulus {
		run(fixture.Exec(fmt.Sprintf("CREATE TABLE %s_%d PARTITION OF %s FOR VALUES WITH(MODULUS %d,REMAINDER %d)", name, r, name, modulus, r), 0))
	}
}

func TestReferenceHashPartitionDefinition(t *testing.T) {
	referenceHashScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := referenceHashRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE hp_one(k INT,payload TEXT) PARTITION BY HASH(k)", 0))
		run(fixture.Exec("CREATE TABLE hp_one_leaf PARTITION OF hp_one FOR VALUES WITH(REMAINDER 0,MODULUS 1)", 0))
		run(fixture.Exec("INSERT INTO hp_one VALUES(NULL,'null'),(-2147483648,'min'),(2147483647,'max')", 3))
		run(fixture.Select("SELECT payload FROM hp_one ORDER BY payload", []string{"payload"}, []string{"TEXT"}, [][]any{{"max"}, {"min"}, {"null"}}))
		run(fixture.Exec("CREATE TABLE hp_max(k INT) PARTITION BY HASH(k)", 0))
		run(fixture.Exec("CREATE TABLE hp_max_leaf PARTITION OF hp_max FOR VALUES WITH(MODULUS 2147483647,REMAINDER 2147483646)", 0))
		run(fixture.Exec("CREATE TABLE hp_expr(k INT) PARTITION BY HASH((k+1))", 0))
		referenceHashChildren(run, "hp_expr", 4)
		run(fixture.Exec("INSERT INTO hp_expr VALUES(0),(1),(2),(3)", 4))
		run(referenceRangeRows("SELECT k FROM hp_expr_0", 0))
		run(referenceRangeRows("SELECT k FROM hp_expr_1", 2))
		run(referenceRangeRows("SELECT k FROM hp_expr_2", 1))
		run(referenceRangeRows("SELECT k FROM hp_expr_3", 3))
		var columns, keys []string
		for i := range 33 {
			columns = append(columns, fmt.Sprintf("k%d INT", i))
			keys = append(keys, fmt.Sprintf("k%d", i))
		}
		run(fixture.Exec("CREATE TABLE hp_keys("+strings.Join(columns[:32], ",")+") PARTITION BY HASH("+strings.Join(keys[:32], ",")+")", 0))
		referenceHashChildren(run, "hp_keys", 1)
		run(fixture.Exec("INSERT INTO hp_keys DEFAULT VALUES", 1))
		run(fixture.Select("SELECT count(*) FROM hp_keys_0", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(referenceRangeError("CREATE TABLE hp_toomany("+strings.Join(columns, ",")+") PARTITION BY HASH("+strings.Join(keys, ",")+")", "54011", "cannot partition using more than 32 columns", fixture.Diagnostic{}))
	})
}

func TestReferenceHashPartitionMixedModuli(t *testing.T) {
	referenceHashScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := referenceHashRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE hp_mix(k INT) PARTITION BY HASH(k)", 0))
		for _, bound := range []struct {
			name string
			m, r int
		}{{"hp_mix_d", 8, 4}, {"hp_mix_b", 4, 2}, {"hp_mix_a", 2, 1}, {"hp_mix_c", 8, 0}} {
			run(fixture.Exec(fmt.Sprintf("CREATE TABLE %s PARTITION OF hp_mix FOR VALUES WITH(MODULUS %d,REMAINDER %d)", bound.name, bound.m, bound.r), 0))
		}
		run(fixture.Exec("INSERT INTO hp_mix VALUES(NULL),(0),(1),(2),(3),(4),(17),(42),(-2147483648),(2147483647)", 10))
		run(referenceRangeRows("SELECT k FROM hp_mix_a ORDER BY k", 3, 4, 2147483647))
		run(referenceRangeRows("SELECT k FROM hp_mix_b ORDER BY k", -2147483648, 2, 42))
		run(fixture.Select("SELECT k FROM hp_mix_c ORDER BY k NULLS FIRST", []string{"k"}, []string{"INT4"}, [][]any{{nil}, {int64(0)}, {int64(1)}}))
		run(referenceRangeRows("SELECT k FROM hp_mix_d", 17))
		run(fixture.Select("SELECT count(*) FROM hp_mix", []string{"count"}, []string{"INT8"}, [][]any{{int64(10)}}))
		run(referenceRangeRows("SELECT k FROM ONLY hp_mix"))
	})
}
