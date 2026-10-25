package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func referencePruningHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, table string, bindings []any, wants [][]any) {
	t.Helper()
	query := "SELECT k FROM " + table + " WHERE k=$1"
	stmt, err := b.DB.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
	for i, binding := range bindings {
		rows := make([][]any, len(wants[i]))
		for j, k := range wants[i] {
			rows[j] = []any{k}
		}
		s := fixture.Select(query, []string{"k"}, []string{"INT4"}, rows, binding)
		s.Bag = true
		s.CheckMetadata = referenceRangeMetadata(b.PG, s.Columns, s.Types, []bool{true})
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
}

func referencePruningPreparedBody(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
	run := partitionConstraintRun(t, p, b, prepared)
	referencePruningRangeSetup(run)
	referencePruningHeld(t, p, b, "pp_range", []any{1, 3, 5, nil, 1}, [][]any{{int64(1), int64(1)}, {int64(3)}, {int64(5)}, nil, {int64(1), int64(1)}})
	referenceListSetup(run)
	run(fixture.Exec("INSERT INTO lp VALUES(1,'a'),(1,'a'),(2,'b'),(3,'c'),(7,'d'),(NULL,'n')", 6))
	referencePruningHeld(t, p, b, "lp", []any{1, 2, 3, 7, 99, nil, 1}, [][]any{{int64(1), int64(1)}, {int64(2)}, {int64(3)}, {int64(7)}, nil, nil, {int64(1), int64(1)}})
	// The same held analysis survives supported RANGE topology changes. It
	// must never retain an execution's branch set or the old DEFAULT ownership.
	query := "SELECT k FROM pp_range WHERE k=$1"
	stmt, err := b.DB.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
	check := func(key int, keys ...int64) {
		s := referenceRangeRows(query, keys...)
		s.Args = []any{key}
		s.CheckMetadata = referenceRangeMetadata(b.PG, s.Columns, s.Types, []bool{true})
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	check(9)
	run(fixture.Exec("CREATE TABLE pp_range_nine(k INT)", 0))
	run(fixture.Exec("INSERT INTO pp_range_nine VALUES(9)", 1))
	run(fixture.Exec("ALTER TABLE pp_range ATTACH PARTITION pp_range_nine FOR VALUES FROM(9) TO(10)", 0))
	check(9, 9)
	check(1, 1, 1)
	run(fixture.Exec("ALTER TABLE pp_range DETACH PARTITION pp_range_nine", 0))
	check(9)
	run(fixture.Exec("INSERT INTO pp_range VALUES(9)", 1))
	check(9, 9)
	check(3, 3)
}

func TestReferencePartitionPruningPrepared(t *testing.T) {
	referencePruningScenario(t, referencePruningPreparedBody)
}
