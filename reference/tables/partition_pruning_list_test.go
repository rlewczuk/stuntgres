package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func referencePruningListBody(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
	run := partitionConstraintRun(t, p, b, prepared)
	referenceListSetup(run)
	run(fixture.Exec("INSERT INTO lp VALUES(1,'a'),(1,'a'),(2,'b'),(3,'c'),(7,'d'),(NULL,'n')", 6))
	for _, c := range []struct {
		predicate string
		keys      []any
	}{
		{"k=1", []any{int64(1), int64(1)}},
		{"k=2", []any{int64(2)}},
		{"k=7", []any{int64(7)}},
		{"k=99", nil},
		{"k IS NULL", []any{nil}},
		{"k=NULL", nil},
		{"k<3", []any{int64(1), int64(1), int64(2)}},
		{"k<>1", []any{int64(2), int64(3), int64(7)}},
		{"k=1 OR k IS NULL", []any{int64(1), int64(1), nil}},
		{"k IS NOT NULL", []any{int64(1), int64(1), int64(2), int64(3), int64(7)}},
		{"k=1 AND k=3", nil},
		{"k=2 OR abs(k)=7", []any{int64(2), int64(7)}},
	} {
		referencePruningKeys(run, b.PG, "SELECT k FROM lp WHERE "+c.predicate, c.keys...)
	}
	for _, q := range []string{
		"CREATE TABLE pp_list_default(k INT) PARTITION BY LIST(k)",
		"CREATE TABLE pp_list_one PARTITION OF pp_list_default FOR VALUES IN(1)",
		"CREATE TABLE pp_list_other PARTITION OF pp_list_default DEFAULT",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO pp_list_default VALUES(1),(7),(NULL)", 3))
	referencePruningKeys(run, b.PG, "SELECT k FROM pp_list_default WHERE k IS NULL", nil)
	referencePruningKeys(run, b.PG, "SELECT k FROM pp_list_default WHERE k>1", int64(7))
	for _, q := range []string{
		"CREATE TABLE pp_list_pair(k INT) PARTITION BY LIST(k)",
		"CREATE TABLE pp_list_pair_a PARTITION OF pp_list_pair FOR VALUES IN(1,NULL)",
		"CREATE TABLE pp_list_pair_b PARTITION OF pp_list_pair FOR VALUES IN(3)",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO pp_list_pair VALUES(1),(NULL),(3)", 3))
	referencePruningKeys(run, b.PG, "SELECT k FROM pp_list_pair WHERE k<>1", int64(3))
}

func TestReferencePartitionPruningList(t *testing.T) {
	referencePruningScenario(t, referencePruningListBody)
}
