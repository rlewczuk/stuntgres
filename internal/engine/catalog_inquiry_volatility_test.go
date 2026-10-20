package engine

import (
	"context"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestCatalogInquiryRetainedVolatility(t *testing.T) {
	e := New()
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := statement(t, "SELECT current_schemas(CASE WHEN true THEN false ELSE random()>0 END)").(*parser.Select).Targets[0]
	node, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	if node.Properties().Volatility != expr.Volatile || plannedIntrinsicVolatility(node) != expr.Volatile {
		t.Fatal("analysis fallback lost eliminated-arm volatility", node.Properties())
	}
	simplified, changed, err := simplifyConditional(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, node, false)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	child := simplified.(expr.Composite).Children()[0]
	literal, ok := child.(plan.LiteralExpr)
	if !ok {
		t.Fatalf("decisive CASE did not simplify: %T", child)
	}
	value, err := literal.Value.Boolean()
	if err != nil || value {
		t.Fatal(literal.Value, err)
	}
	if exec.PlannedVolatility(simplified) != expr.Volatile || plannedIntrinsicVolatility(simplified) != expr.Volatile {
		t.Fatal("physical/intrinsic inquiry inspection changed the historical analyzed fallback")
	}
}
