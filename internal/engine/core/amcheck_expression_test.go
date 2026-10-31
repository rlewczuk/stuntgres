package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func amcheckUnitExpression(t *testing.T, e *core.Engine, parent bool, options ...bool) plan.InstalledAmcheckExpr {
	t.Helper()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	name := "bt_index_check"
	if parent {
		name = "bt_index_parent_check"
	}
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "i"}}}}
	for _, value := range options {
		raw.Args = append(raw.Args, parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: value}})
	}
	node, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	return node.(plan.InstalledAmcheckExpr)
}
