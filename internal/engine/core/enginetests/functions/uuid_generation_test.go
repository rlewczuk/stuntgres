package functions_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestUUIDGenerationBinderNullAndDemand(t *testing.T) {
	b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
	for _, name := range []string{"gen_random_uuid", "uuidv4", "uuidv7"} {
		node, err := b.Bind(enginetest.FunctionCall(name), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		if node.ResultUsage().Type.ID() != sqltype.UUID || node.Properties().Volatility != expr.Volatile {
			t.Fatal(name, node)
		}
	}
	node, err := b.Bind(enginetest.FunctionCall("uuidv7", parser.ParameterExpr{Number: 1}), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	if !node.Properties().ParameterDependent || node.Properties().Volatility != expr.Volatile || analyze.ClosedUUIDDefault(node) {
		t.Fatal("parameter dependence", node.Properties())
	}
	uuid := b.Type(sqltype.UUID)
	iv := b.Type(sqltype.Interval)
	argument := plan.LiteralExpr{Info: expr.Info{Usage: iv.Usage()}, Value: sqltype.Null(iv)}
	bound := functions.UUIDGeneration{Info: expr.Info{Usage: uuid.Usage(), Property: expr.Properties{Volatility: expr.Volatile}}, Name: "uuidv7", Args: []expr.Expr{argument}}
	// Tree inspection, descriptor remapping and rendering do not evaluate a child.
	persisted, err := b.Bind(enginetest.FunctionCall("uuidv7", parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "0"}}), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	other := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
	rebound, changed, err := stored.RetagTypes(other.BindingCatalog(), persisted, func(d *sqltype.Descriptor) *sqltype.Descriptor { return other.Type(d.ID()) })
	if err != nil || !changed {
		t.Fatal("UUID retag", changed, err)
	}
	n := rebound.(functions.UUIDGeneration)
	if n.Timestamp != other.Type(sqltype.Timestamptz) || n.ResultUsage().Type != other.Type(sqltype.UUID) || n.Args[0].ResultUsage().Type != other.Type(sqltype.Interval) || n.Properties() != persisted.Properties() {
		t.Fatal("retag lost descriptor/dependence")
	}
	if plan.GenerationImmutable(bound) {
		t.Fatal("UUID admitted to immutable consumer")
	}
	if analyze.ClosedUUIDDefault(functions.Random{Info: expr.Info{Usage: uuid.Usage(), Property: expr.Properties{Volatility: expr.Volatile}}, Name: "random"}) {
		t.Fatal("unrelated volatile default admitted")
	}
}
