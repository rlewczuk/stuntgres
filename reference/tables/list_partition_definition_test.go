package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func referenceListScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, bool), before ...func(*testing.T)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			for _, setup := range before {
				setup(t)
			}
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) { body(t, p, b, prepared) })
			}
		})
	}
}

func referenceListAbsent(run func(fixture.Step), name string) {
	run(fixture.Select("SELECT relname FROM pg_class WHERE relname='"+name+"'", []string{"relname"}, []string{"NAME"}, nil))
	run(fixture.Select("SELECT typname FROM pg_type WHERE typname='"+name+"'", []string{"typname"}, []string{"NAME"}, nil))
	run(fixture.Select("SELECT c.relname FROM pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid WHERE c.relname='"+name+"'", []string{"relname"}, []string{"NAME"}, nil))
}

func referenceListSetup(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE lp(k INT,payload TEXT) PARTITION BY LIST(k)", 0))
	run(fixture.Exec("CREATE TABLE lp_a PARTITION OF lp FOR VALUES IN(1,3,NULL)", 0))
	run(fixture.Exec("CREATE TABLE lp_b PARTITION OF lp FOR VALUES IN(2)", 0))
	run(fixture.Exec("CREATE TABLE lp_def PARTITION OF lp DEFAULT", 0))
}

func TestReferenceListPartitionDefinition(t *testing.T) {
	referenceListScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		referenceListSetup(run)
		run(fixture.Exec("INSERT INTO lp VALUES(1,'a'),(2,'b'),(3,'c'),(NULL,'n'),(7,'d'),(1,'a')", 6))
		for _, c := range []struct {
			table string
			rows  [][]any
		}{
			{"lp", [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), "b"}, {int64(3), "c"}, {int64(7), "d"}, {nil, "n"}}},
			{"lp_a", [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(3), "c"}, {nil, "n"}}},
			{"lp_b", [][]any{{int64(2), "b"}}},
			{"lp_def", [][]any{{int64(7), "d"}}},
			{"ONLY lp", nil},
		} {
			s := fixture.Select("SELECT k,payload FROM "+c.table+" ORDER BY k NULLS LAST,payload", []string{"k", "payload"}, []string{"INT4", "TEXT"}, c.rows)
			s.CheckMetadata = referenceRangeMetadata(b.PG, s.Columns, s.Types, []bool{true, true})
			run(s)
		}
		run(fixture.Prepared(fixture.Exec("INSERT INTO lp VALUES($1,$2)", 1, 2, "bound")))
		run(fixture.Prepared(fixture.Select("SELECT payload FROM lp_b WHERE k=$1 ORDER BY payload", []string{"payload"}, []string{"TEXT"}, [][]any{{"b"}, {"bound"}}, 2)))
	})
}

func TestReferenceListPartitionRouting(t *testing.T) {
	referenceListScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE lp_route(k INT) PARTITION BY LIST(k)", 0))
		run(fixture.Exec("CREATE TABLE lp_one PARTITION OF lp_route FOR VALUES IN(1)", 0))
		run(referenceRangeMiss("INSERT INTO lp_route VALUES(7)", "lp_route", "k", "7"))
		run(referenceRangeMiss("INSERT INTO lp_route VALUES(NULL)", "lp_route", "k", "null"))
		run(fixture.Exec("CREATE TABLE lp_null PARTITION OF lp_route FOR VALUES IN(NULL)", 0))
		run(fixture.Exec("INSERT INTO lp_route VALUES(NULL)", 1))
		run(fixture.Select("SELECT k FROM lp_null", []string{"k"}, []string{"INT4"}, [][]any{{nil}}))
		run(referenceRangeLeafMiss("INSERT INTO lp_one VALUES(2)", "lp_one", "2"))
		run(referenceRangeRows("SELECT k FROM lp_one"))
		run(fixture.Exec("CREATE TABLE lp_other(k INT) PARTITION BY LIST(k)", 0))
		run(fixture.Exec("CREATE TABLE lp_other_def PARTITION OF lp_other DEFAULT", 0))
		run(fixture.Exec("INSERT INTO lp_other VALUES(NULL),(7)", 2))
		run(fixture.Select("SELECT k FROM lp_other_def ORDER BY k NULLS LAST", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}}))
	})
}

func TestReferenceListPartitionNesting(t *testing.T) {
	referenceListScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		for _, q := range []string{
			"CREATE TABLE lp_nest(k INT,payload TEXT) PARTITION BY LIST(k)",
			"CREATE TABLE lp_mid PARTITION OF lp_nest FOR VALUES IN(1,2) PARTITION BY LIST(payload)",
			"CREATE TABLE lp_x PARTITION OF lp_mid FOR VALUES IN('x')",
		} {
			run(fixture.Exec(q, 0))
		}
		run(referenceRangeMiss("INSERT INTO lp_nest VALUES(1,'other')", "lp_mid", "payload", "other"))
		run(fixture.Exec("CREATE TABLE lp_mid_def PARTITION OF lp_mid DEFAULT", 0))
		run(fixture.Exec("INSERT INTO lp_nest VALUES(1,'x'),(2,'other')", 2))
		run(fixture.Select("SELECT k,payload FROM lp_x", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}}))
		run(fixture.Select("SELECT k,payload FROM lp_mid_def", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "other"}}))
		run(fixture.Select("SELECT k FROM lp_nest ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		run(fixture.Exec("CREATE TABLE lp_far PARTITION OF lp_nest FOR VALUES IN(5) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE lp_outside PARTITION OF lp_far FOR VALUES FROM(10) TO(20)", 0))
		run(referenceRangeLeafMiss("INSERT INTO lp_outside VALUES(12,'outside')", "lp_outside", "12, outside"))
		run(fixture.Exec("CREATE TABLE lp_hash PARTITION OF lp_nest FOR VALUES IN(8) PARTITION BY HASH(k)", 0))
		run(fixture.Exec("CREATE TABLE lp_hash_leaf PARTITION OF lp_hash FOR VALUES WITH(MODULUS 1,REMAINDER 0)", 0))
		run(fixture.Exec("INSERT INTO lp_nest VALUES(8,'hash')", 1))
		run(referenceRangeRows("SELECT k FROM lp_hash_leaf", 8))
	})
}
