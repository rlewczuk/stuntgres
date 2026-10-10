package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestUpdateReturningMalformedSumShape(t *testing.T) {
	e := New()
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	binder.aggregateClause = "RETURNING"
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, raw := range []parser.AggregateExpr{
		{Name: "sum"},
		{Name: "sum", Star: true},
		{Name: "sum", Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 2}}}},
	} {
		node, err := binder.bind(raw, expressionPolicy{context: projectionContext})
		var diagnostic *sqlstate.Error
		message := "function sum() does not exist"
		if len(raw.Args) == 2 {
			message = "function sum(integer, integer) does not exist"
		}
		if node != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "42883" || diagnostic.Message != message || diagnostic.Hint != "No function matches the given name and argument types. You might need to add explicit type casts." {
			t.Fatal(raw, node, err)
		}
	}
}
