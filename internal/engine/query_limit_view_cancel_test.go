package engine

import (
	"context"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryLimitViewCancellation(t *testing.T) {
	e, query, input := queryCoreFixture(t, "SELECT id FROM t WHERE f")
	inner := query.Root.(*projectNode)
	filter := inner.Child.(*filterNode)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bt, _ := e.CatalogRead().LookupType(sqltype.Bool)
	calls := 0
	filter.Predicate = queryProbe(bt.Usage(), func(*EvalContext) (sqltype.Value, error) { calls++; cancel(); return sqltype.Boolean(bt, false), nil })
	bridge := &querySource{ref: input.ref, nestedRoot: inner, nestedInputs: input, materialized: true, flatWidth: 1}
	leaf := &sourceNode{QuerySchema: inner.Schema(), Ref: input.ref}
	limit := &limitNode{QuerySchema: inner.Schema(), Child: leaf, Count: queryLimitBound(t, e, 1)}
	outer := &projectNode{QuerySchema: inner.Schema(), Child: limit, Expressions: inner.Expressions}
	rows, n, err := finalizeQuery(ctx, outer, bridge, nil)
	if rows != nil || n != 0 || err != context.Canceled || calls != 1 || input.view != nil || bridge.nestedStage != nil {
		t.Fatal(rows, n, err, calls, input.view, bridge.nestedStage)
	}
}
