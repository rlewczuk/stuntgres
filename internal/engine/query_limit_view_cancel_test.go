package engine

import (
	"context"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryLimitViewCancellation(t *testing.T) {
	e, query, input := queryCoreFixture(t, "SELECT id FROM t WHERE f")
	inner := query.Root.(*plan.ProjectNode)
	filter := inner.Child.(*plan.FilterNode)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bt, _ := e.CatalogRead().LookupType(sqltype.Bool)
	calls := 0
	filter.Predicate = queryProbe(bt.Usage(), func(*EvalContext) (sqltype.Value, error) { calls++; cancel(); return sqltype.Boolean(bt, false), nil })
	bridge := &querySource{Ref: input.Ref, NestedRoot: inner, NestedInputs: input, Materialized: true, FlatWidth: 1}
	leaf := &plan.SourceNode{QuerySchema: inner.Schema(), Ref: input.Ref}
	limit := &plan.LimitNode{QuerySchema: inner.Schema(), Child: leaf, Count: queryLimitBound(t, e, 1)}
	outer := &plan.ProjectNode{QuerySchema: inner.Schema(), Child: limit, Expressions: inner.Expressions}
	rows, n, err := exec.Finalize(ctx, outer, bridge, nil)
	if rows != nil || n != 0 || err != context.Canceled || calls != 1 || input.View != nil || bridge.NestedStage != nil {
		t.Fatal(rows, n, err, calls, input.View, bridge.NestedStage)
	}
}
