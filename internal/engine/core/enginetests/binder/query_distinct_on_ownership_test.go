package binder_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestQueryDistinctOnComputedKeyCacheOwnership(t *testing.T) {
	for _, suffix := range []string{"", " ORDER BY v+0,id DESC"} {
		query, source := enginetest.QueryFixture(t, engine.New(), "SELECT DISTINCT ON(v+0) v+0 AS shown FROM t"+suffix)
		project := query.Root.(*plan.ProjectNode)
		node := project.Child.(*plan.DistinctOnNode)
		var cached []expr.Expr
		switch input := node.Child.(type) {
		case *plan.EvaluationNode:
			cached = input.Expressions
		case *plan.SortNode:
			cached = []expr.Expr{input.Keys[0].Expression}
		default:
			t.Fatalf("missing key cache %T", input)
		}
		original := cached[0]
		calls := 0
		probe := enginetest.QueryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) { calls++; return original.Eval(c) })
		switch input := node.Child.(type) {
		case *plan.EvaluationNode:
			input.Expressions[0] = probe
		case *plan.SortNode:
			input.Keys[0].Expression = probe
		}
		rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
		if err != nil || count != 3 || calls != 4 {
			t.Fatal(suffix, rows, count, calls, err)
		}
		value := make([]sqltype.Value, 1)
		n := 0
		for {
			ok, err := rows.Next(context.Background(), value)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				break
			}
			n++
		}
		rows.Close()
		if n != 3 || calls != 4 || source.View != nil {
			t.Fatal("final projection reevaluated key or retained pins", suffix, n, calls, source.View)
		}
	}
}

func TestQueryDistinctOnScratchKeyMutation(t *testing.T) {
	query, source := enginetest.QueryFixture(t, engine.New(), "SELECT DISTINCT ON(v) id FROM t")
	node := query.Root.(*plan.ProjectNode).Child.(*plan.DistinctOnNode)
	usage := node.Expressions[0].ResultUsage()
	scratch := make([]sqltype.Value, 1)
	node.Expressions[0] = enginetest.QueryProbe(usage, func(c *exec.Context) (sqltype.Value, error) {
		id, err := c.Row[0].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		scratch[0], err = sqltype.Integer(usage.Type, id%2)
		return scratch[0], err
	})
	rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
	if err != nil || count != 2 {
		t.Fatal("seen key retained mutable scratch headers", rows, count, err)
	}
	defer rows.Close()
	values := make([]sqltype.Value, 1)
	for _, want := range []int64{1, 2} {
		ok, err := rows.Next(context.Background(), values)
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
		got, err := values[0].Integer()
		if err != nil || got != want {
			t.Fatal(got, want, err)
		}
	}
}
