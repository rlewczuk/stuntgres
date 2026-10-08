package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestUpdateReturningMalformedSumShape(t *testing.T) {
	for _, raw := range []parser.AggregateExpr{
		{Name: "sum"},
		{Name: "sum", Star: true},
		{Name: "sum", Args: []parser.Expr{parser.LiteralExpr{}, parser.LiteralExpr{}}},
	} {
		binder := expressionBinder{ctx: context.Background(), aggregateClause: "RETURNING"}
		node, err := binder.bind(raw, expressionPolicy{context: projectionContext})
		var diagnostic *sqlstate.Error
		if node != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "aggregate call is unsupported" {
			t.Fatal(raw, node, err)
		}
	}
}
