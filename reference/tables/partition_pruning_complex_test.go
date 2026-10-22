package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func referencePruningComplexBody(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
	run := partitionConstraintRun(t, p, b, prepared)
	columns := []string{"k INT"}
	predicates := make([]string, 12)
	zeros := make([]string, 12)
	for i := range predicates {
		columns = append(columns, fmt.Sprintf("a%d INT", i))
		predicates[i] = fmt.Sprintf("(a%d=0 OR a%d=1)", i, i)
		zeros[i] = "0"
	}
	run(fixture.Exec("CREATE TABLE pp_complex("+strings.Join(columns, ",")+") PARTITION BY RANGE(k)", 0))
	run(fixture.Exec("CREATE TABLE pp_complex_a PARTITION OF pp_complex FOR VALUES FROM(1) TO(3)", 0))
	// This ordinary physical branch remains empty throughout the scenario.
	run(fixture.Exec("CREATE TABLE pp_complex_empty PARTITION OF pp_complex FOR VALUES FROM(3) TO(5)", 0))
	run(fixture.Exec("CREATE TABLE pp_complex_default PARTITION OF pp_complex DEFAULT", 0))
	for _, k := range []int{1, 99, 99} {
		run(fixture.Exec(fmt.Sprintf("INSERT INTO pp_complex VALUES(%d,%s)", k, strings.Join(zeros, ",")), 1))
	}
	// A retained DEFAULT row fails the original residual, despite matching k.
	zeros[11] = "2"
	run(fixture.Exec("INSERT INTO pp_complex VALUES(99,"+strings.Join(zeros, ",")+")", 1))
	prefix := "SELECT k FROM pp_complex WHERE " + strings.Join(predicates, " AND ") + " AND k="
	for _, c := range []struct {
		key  string
		rows []any
	}{
		{"99", []any{int64(99), int64(99)}},
		{"1", []any{int64(1)}},
		{"3", nil},
		{"NULL", nil},
	} {
		referencePruningKeys(run, b.PG, prefix+c.key, c.rows...)
	}

	// Keep exactly one analyzed statement across changing bindings: exhaustion
	// may retain extra physical branches, never a previous execution's rows.
	query := prefix + "$1"
	stmt, err := b.DB.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
	for _, c := range []struct {
		key  any
		rows [][]any
	}{
		{99, [][]any{{int64(99)}, {int64(99)}}},
		{1, [][]any{{int64(1)}}},
		{3, nil},
		{nil, nil},
		{99, [][]any{{int64(99)}, {int64(99)}}},
	} {
		s := fixture.Select(query, []string{"k"}, []string{"INT4"}, c.rows, c.key)
		s.Bag = true
		s.CheckMetadata = referenceRangeMetadata(b.PG, s.Columns, s.Types, []bool{true})
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
}

func TestReferencePartitionPruningComplexPredicate(t *testing.T) {
	referencePruningScenario(t, referencePruningComplexBody)
}
