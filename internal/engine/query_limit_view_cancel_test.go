package engine

import (
	"context"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryLimitViewCancellation(t *testing.T) {
	e, query, input := queryCoreFixture(t, "SELECT id FROM t WHERE f")
	inner := query.Root.(*projectNode)
	filter := inner.child.(*filterNode)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bt, _ := e.CatalogRead().LookupType(sqltype.Bool)
	calls := 0
	filter.predicate = queryProbe(bt.Usage(), func(*EvalContext) (sqltype.Value, error) { calls++; cancel(); return sqltype.Boolean(bt, false), nil })
	bridge := &querySource{ref: input.ref, nestedRoot: inner, nestedInputs: input, materialized: true, flatWidth: 1}
	leaf := &sourceNode{querySchema: inner.Schema(), ref: input.ref}
	limit := &limitNode{querySchema: inner.Schema(), child: leaf, count: queryLimitBound(t, e, 1)}
	outer := &projectNode{querySchema: inner.Schema(), child: limit, expressions: inner.expressions}
	rows, n, err := finalizeQuery(ctx, outer, bridge, nil)
	if rows != nil || n != 0 || err != context.Canceled || calls != 1 || input.view != nil || bridge.nestedStage != nil {
		t.Fatal(rows, n, err, calls, input.view, bridge.nestedStage)
	}
}
