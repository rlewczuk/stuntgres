package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func referencePruningRangeBody(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
	run := partitionConstraintRun(t, p, b, prepared)
	referencePruningRangeSetup(run)
	for _, c := range []struct {
		predicate string
		keys      []any
	}{
		{"k=1", []any{int64(1), int64(1)}},
		{"k>=3 AND k<5", []any{int64(3), int64(4)}},
		{"k<1", []any{int64(-1), int64(0)}},
		{"k=3", []any{int64(3)}},
		{"k=5", []any{int64(5)}},
		{"k IS NULL", []any{nil}},
		{"k=NULL", nil},
		{"k=1 OR k IS NULL", []any{int64(1), int64(1), nil}},
		{"k<3", []any{int64(-1), int64(0), int64(1), int64(1), int64(2)}},
		{"1=k", []any{int64(1), int64(1)}},
		{"3>k AND k>=1", []any{int64(1), int64(1), int64(2)}},
		{"k>3 AND k<3", nil},
		{"FALSE", nil},
		{"NULL::BOOL", nil},
		{"k=1 OR abs(k)=5", []any{int64(1), int64(1), int64(5)}},
		{"k IS NOT NULL AND k<>1 AND k<=3", []any{int64(-1), int64(0), int64(2), int64(3)}},
	} {
		referencePruningKeys(run, b.PG, "SELECT k FROM pp_range WHERE "+c.predicate, c.keys...)
	}
	run(fixture.Exec("CREATE TABLE pp_childless(k INT) PARTITION BY RANGE(k)", 0))
	referencePruningKeys(run, b.PG, "SELECT k FROM pp_childless WHERE k=1")
	referencePruningKeys(run, b.PG, "SELECT k FROM ONLY pp_range WHERE k=1")
	// A nested filter and a view occurrence retain the same source identity.
	run(fixture.Exec("CREATE VIEW pp_range_view AS SELECT k FROM pp_range WHERE k>=1", 0))
	referencePruningKeys(run, b.PG, "SELECT k FROM pp_range_view WHERE k=1", int64(1), int64(1))
	referencePruningKeys(run, b.PG, "SELECT k FROM (SELECT k FROM pp_range WHERE k<5) AS q WHERE k=3", int64(3))
	run(fixture.Select("SELECT count(*) AS n FROM pp_range a JOIN pp_range b ON a.k=b.k WHERE a.k=1", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
}

func TestReferencePartitionPruningRange(t *testing.T) {
	referencePruningScenario(t, referencePruningRangeBody)
}
