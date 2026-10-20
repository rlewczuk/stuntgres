package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestQueryOrderSharedHiddenKeysAndOwnedIndices(t *testing.T) {
	e, query, source := queryCoreFixture(t, "SELECT id FROM t WHERE f ORDER BY id+1 DESC,id+1,id+2")
	project := query.Root.(*plan.ProjectNode)
	sorted := project.Child.(*plan.SortNode)
	calls := [2]int{}
	for i := range sorted.Keys {
		counter := 0
		if i == 2 {
			counter = 1
		}
		sorted.Keys[i].Expression = queryProbe(sorted.Keys[i].Expression.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
			calls[counter]++
			return c.Row[0], nil
		})
	}
	rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
	if err != nil || count != 2 || calls != [2]int{2, 2} {
		t.Fatal(count, calls, err)
	}
	defer rows.Close()
	// Filtered occurrences have nonidentity source indices. Finalization closes
	// the stage, and later engine publication must not corrupt transferred indices.
	run(t, e, "INSERT INTO t VALUES(5,3,TRUE)")
	dst := make([]sqltype.Value, 1)
	for _, want := range []int64{4, 1} {
		present, err := rows.Next(context.Background(), dst)
		value, inputErr := sqltype.Integer(project.Schema()[0].Usage.Type, want)
		if !present || err != nil || inputErr != nil || !sqltype.SameDatum(dst[0], value) {
			t.Fatal(dst, want, present, err, inputErr)
		}
	}
	if present, err := rows.Next(context.Background(), dst); present || err != nil {
		t.Fatal(present, err)
	}
	if calls != [2]int{2, 2} {
		t.Fatal("retained result reevaluated keys", calls)
	}
}
