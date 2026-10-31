package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func preflightCast(t *testing.T, q string) expr.Expr {
	t.Helper()
	b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
	expression, err := b.Bind(castTarget(t, q), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	return expression
}

func castTarget(t *testing.T, q string) parser.Expr {
	t.Helper()
	return enginetest.Statement(t, q).(*parser.Select).Targets[0]
}
