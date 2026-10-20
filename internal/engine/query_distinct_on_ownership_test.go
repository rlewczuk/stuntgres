package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryDistinctOnComputedKeyCacheOwnership(t *testing.T) {
	for _, suffix := range []string{"", " ORDER BY v+0,id DESC"} {
		_, query, source := queryCoreFixture(t, "SELECT DISTINCT ON(v+0) v+0 AS shown FROM t"+suffix)
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
		probe := queryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) { calls++; return original.Eval(c) })
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
	_, query, source := queryCoreFixture(t, "SELECT DISTINCT ON(v) id FROM t")
	node := query.Root.(*plan.ProjectNode).Child.(*plan.DistinctOnNode)
	usage := node.Expressions[0].ResultUsage()
	scratch := make([]sqltype.Value, 1)
	node.Expressions[0] = queryProbe(usage, func(c *exec.Context) (sqltype.Value, error) {
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

func TestQueryDistinctOnRequiredCloseAndPrimaryError(t *testing.T) {
	e := New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	usage := integer.Usage()
	one, _ := sqltype.Integer(integer, 1)
	schema := plan.QuerySchema{{Name: "id", Usage: usage}}
	family, err := sqltype.BindGrouping(usage)
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"close", "key", "sort"} {
		closeFailure := errors.New("ON child close")
		primary := errors.New("ON key or sort primary")
		closed := 0
		inner := &exec.Stage{Source: &exec.Source{Anonymous: [][]sqltype.Value{{one}}}, Columns: schema, Expressions: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: one}}, Evaluation: exec.Context{}, Finish: func() error { closed++; return closeFailure }}
		bridge := &exec.Source{Materialized: true, FlatWidth: 1, NestedRoot: &plan.ProjectNode{QuerySchema: schema, Child: &plan.SingletonNode{}}, NestedStage: inner}
		source := &exec.Source{Sources: []*exec.Source{bridge}}
		key := queryProbe(usage, func(c *exec.Context) (sqltype.Value, error) {
			if phase == "key" {
				return sqltype.Value{}, primary
			}
			return c.Row[0], nil
		})
		var child plan.Node = &plan.SourceNode{QuerySchema: schema, Occurrence: 0}
		if phase == "sort" {
			comparison, err := sqltype.BindOrdering(usage)
			if err != nil {
				t.Fatal(err)
			}
			child = &plan.SortNode{QuerySchema: schema, Child: child, CacheWidth: 1, Keys: []plan.SortKey{{Cache: 0, Slot: -1, Comparison: comparison, Expression: queryProbe(usage, func(*exec.Context) (sqltype.Value, error) { return sqltype.Value{}, primary })}}}
		}
		node := &plan.DistinctOnNode{QuerySchema: schema, Child: child, Expressions: []expr.Expr{key}, Grouping: []plan.Grouping{{Grouping: family}}}
		root := &plan.ProjectNode{QuerySchema: schema, Child: node, Expressions: []expr.Expr{plan.ColumnExpr{Info: expr.Info{Usage: usage}, Scope: 1, Frame: 1, Slot: 0}}}
		rows, count, err := exec.Finalize(context.Background(), root, source, nil)
		want := error(primary)
		if phase == "close" {
			want = closeFailure
		}
		if rows != nil || count != 0 || !errors.Is(err, want) || closed != 1 {
			t.Fatal(phase, rows, count, err, want, closed)
		}
	}
}
