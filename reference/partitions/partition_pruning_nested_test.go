package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionPruningNested(t *testing.T) {
	referencePruningScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		for _, q := range []string{
			"CREATE TABLE pp_rl(k INT) PARTITION BY RANGE(k)",
			"CREATE TABLE pp_rl_mid PARTITION OF pp_rl FOR VALUES FROM(1) TO(5) PARTITION BY LIST(k)",
			"CREATE TABLE pp_rl_a PARTITION OF pp_rl_mid FOR VALUES IN(1,3)",
			"CREATE TABLE pp_rl_md PARTITION OF pp_rl_mid DEFAULT",
			"CREATE TABLE pp_rl_d PARTITION OF pp_rl DEFAULT",
			"CREATE TABLE pp_lh(k INT) PARTITION BY LIST(k)",
			"CREATE TABLE pp_lh_mid PARTITION OF pp_lh FOR VALUES IN(1,2,NULL) PARTITION BY HASH(k)",
			"CREATE TABLE pp_lh_a PARTITION OF pp_lh_mid FOR VALUES WITH(MODULUS 2,REMAINDER 0)",
			"CREATE TABLE pp_lh_b PARTITION OF pp_lh_mid FOR VALUES WITH(MODULUS 2,REMAINDER 1)",
			"CREATE TABLE pp_lh_d PARTITION OF pp_lh DEFAULT",
			"CREATE TABLE pp_hr(k INT) PARTITION BY HASH(k)",
			"CREATE TABLE pp_hr_mid PARTITION OF pp_hr FOR VALUES WITH(MODULUS 1,REMAINDER 0) PARTITION BY RANGE(k)",
			"CREATE TABLE pp_hr_a PARTITION OF pp_hr_mid FOR VALUES FROM(1) TO(3)",
			"CREATE TABLE pp_hr_d PARTITION OF pp_hr_mid DEFAULT",
		} {
			run(fixture.Exec(q, 0))
		}
		for _, name := range []string{"pp_rl", "pp_lh", "pp_hr"} {
			run(fixture.Exec("INSERT INTO "+name+" VALUES(1),(1),(2),(3),(7),(NULL)", 6))
			referencePruningKeys(run, b.PG, "SELECT k FROM "+name+" WHERE k=1", int64(1), int64(1))
			referencePruningKeys(run, b.PG, "SELECT k FROM "+name+" WHERE k=7", int64(7))
			referencePruningKeys(run, b.PG, "SELECT k FROM "+name+" WHERE k IS NULL", nil)
			referencePruningKeys(run, b.PG, "SELECT k FROM "+name+" WHERE k>=2 AND k<4", int64(2), int64(3))
		}
		for _, q := range []string{
			"CREATE TABLE pp_lex(a INT,b INT,id INT) PARTITION BY RANGE(a,b)",
			"CREATE TABLE pp_lex_low PARTITION OF pp_lex FOR VALUES FROM(MINVALUE,MINVALUE) TO(1,MINVALUE)",
			"CREATE TABLE pp_lex_mid PARTITION OF pp_lex FOR VALUES FROM(1,2) TO(3,4)",
			"CREATE TABLE pp_lex_high PARTITION OF pp_lex FOR VALUES FROM(3,4) TO(MAXVALUE,MAXVALUE)",
			"CREATE TABLE pp_lex_def PARTITION OF pp_lex DEFAULT",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO pp_lex VALUES(0,9,0),(1,1,1),(1,2,2),(2,-9,3),(3,3,4),(3,4,5),(NULL,2,6),(2,NULL,7)", 8))
		for _, c := range []struct {
			predicate string
			ids       []int64
		}{
			{"a=1 AND b=1", []int64{1}},
			{"a=1 AND b>=2", []int64{2}},
			{"a=2", []int64{3, 7}},
			{"a=3 AND b<4", []int64{4}},
			{"a=3 AND b=4", []int64{5}},
			{"b=2", []int64{2, 6}},
			{"a IS NULL OR b IS NULL", []int64{6, 7}},
		} {
			rows := make([][]any, len(c.ids))
			for i, id := range c.ids {
				rows[i] = []any{id}
			}
			s := fixture.Select("SELECT id FROM pp_lex WHERE "+c.predicate, []string{"id"}, []string{"INT4"}, rows)
			s.Bag = true
			run(s)
		}
	})
}
