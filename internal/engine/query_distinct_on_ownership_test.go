package engine

import (
	"context"
	"errors"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryDistinctOnComputedKeyCacheOwnership(t *testing.T) {
	for _, suffix := range []string{"", " ORDER BY v+0,id DESC"} {
		_, query, source := queryCoreFixture(t, "SELECT DISTINCT ON(v+0) v+0 AS shown FROM t"+suffix)
		project := query.Root.(*projectNode)
		node := project.child.(*distinctOnNode)
		var cached []BoundExpr
		switch input := node.child.(type) {
		case *evaluationNode:
			cached = input.expressions
		case *sortNode:
			cached = []BoundExpr{input.keys[0].expression}
		default:
			t.Fatalf("missing key cache %T", input)
		}
		original := cached[0]
		calls := 0
		probe := queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) { calls++; return original.Eval(c) })
		switch input := node.child.(type) {
		case *evaluationNode:
			input.expressions[0] = probe
		case *sortNode:
			input.keys[0].expression = probe
		}
		rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
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
		if n != 3 || calls != 4 || source.view != nil {
			t.Fatal("final projection reevaluated key or retained pins", suffix, n, calls, source.view)
		}
	}
}

func TestQueryDistinctOnScratchKeyMutation(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT DISTINCT ON(v) id FROM t")
	node := query.Root.(*projectNode).child.(*distinctOnNode)
	usage := node.expressions[0].ResultUsage()
	scratch := make([]sqltype.Value, 1)
	node.expressions[0] = queryProbe(usage, func(c *EvalContext) (sqltype.Value, error) {
		id, err := c.Row[0].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		scratch[0], err = sqltype.Integer(usage.Type, id%2)
		return scratch[0], err
	})
	rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
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
	schema := querySchema{{Name: "id", Usage: usage}}
	family, err := sqltype.BindGrouping(usage)
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"close", "key", "sort"} {
		closeFailure := errors.New("ON child close")
		primary := errors.New("ON key or sort primary")
		closed := 0
		inner := &indexedStage{source: &querySource{anonymous: [][]sqltype.Value{{one}}}, schema: schema, expressions: []BoundExpr{literalExpression{expressionInfo: expressionInfo{usage: usage}, value: one}}, evaluation: EvalContext{}, finish: func() error { closed++; return closeFailure }}
		bridge := &querySource{materialized: true, flatWidth: 1, nestedRoot: &projectNode{querySchema: schema, child: &singletonNode{}}, nestedStage: inner}
		source := &querySource{sources: []*querySource{bridge}}
		key := queryProbe(usage, func(c *EvalContext) (sqltype.Value, error) {
			if phase == "key" {
				return sqltype.Value{}, primary
			}
			return c.Row[0], nil
		})
		var child LogicalNode = &sourceNode{querySchema: schema, occurrence: 0}
		if phase == "sort" {
			comparison, err := sqltype.BindOrdering(usage)
			if err != nil {
				t.Fatal(err)
			}
			child = &sortNode{querySchema: schema, child: child, cacheWidth: 1, keys: []sortKey{{cache: 0, slot: -1, comparison: comparison, expression: queryProbe(usage, func(*EvalContext) (sqltype.Value, error) { return sqltype.Value{}, primary })}}}
		}
		node := &distinctOnNode{querySchema: schema, child: child, expressions: []BoundExpr{key}, grouping: []sqltype.Grouping{family}}
		root := &projectNode{querySchema: schema, child: node, expressions: []BoundExpr{columnExpression{expressionInfo: expressionInfo{usage: usage}, scope: 1, frame: 1, slot: 0}}}
		rows, count, err := finalizeQuery(context.Background(), root, source, nil)
		want := error(primary)
		if phase == "close" {
			want = closeFailure
		}
		if rows != nil || count != 0 || !errors.Is(err, want) || closed != 1 {
			t.Fatal(phase, rows, count, err, want, closed)
		}
	}
}
