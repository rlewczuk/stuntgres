package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestUUIDGenerationBinderNullAndDemand(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	for _, name := range []string{"gen_random_uuid", "uuidv4", "uuidv7"} {
		node, err := b.Bind(nullCountRaw(name), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		if node.ResultUsage().Type.ID() != sqltype.UUID || node.Properties().Volatility != expr.Volatile {
			t.Fatal(name, node)
		}
	}
	node, err := b.Bind(nullCountRaw("uuidv7", parser.ParameterExpr{Number: 1}), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
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
	stored, err := b.Bind(nullCountRaw("uuidv7", parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "0"}}), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	other := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	rebound, changed, err := retagBoundTypes(other.BindingCatalog(), stored, func(d *sqltype.Descriptor) *sqltype.Descriptor { return other.Type(d.ID()) })
	if err != nil || !changed {
		t.Fatal("UUID retag", changed, err)
	}
	n := rebound.(functions.UUIDGeneration)
	if n.Timestamp != other.Type(sqltype.Timestamptz) || n.ResultUsage().Type != other.Type(sqltype.UUID) || n.Args[0].ResultUsage().Type != other.Type(sqltype.Interval) || n.Properties() != stored.Properties() {
		t.Fatal("retag lost descriptor/dependence")
	}
	if generationImmutable(bound) {
		t.Fatal("UUID admitted to immutable consumer")
	}
	if analyze.ClosedUUIDDefault(functions.Random{Info: expr.Info{Usage: uuid.Usage(), Property: expr.Properties{Volatility: expr.Volatile}}, Name: "random"}) {
		t.Fatal("unrelated volatile default admitted")
	}
}

func TestUUIDGenerationStrictNullPhysicalTrees(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	null := parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
	typedNull := parser.CastExpr{Child: null, TypeName: parser.TypeName{Name: "interval"}}
	zero := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "0"}}
	yes := parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true}}
	for _, child := range []parser.Expr{
		null, typedNull,
		parser.CaseExpr{Arms: []parser.CaseArm{{Condition: yes, Result: typedNull}}, Else: zero},
		parser.ConditionalExpr{Kind: parser.Coalesce, Args: []parser.Expr{typedNull, typedNull}},
	} {
		analyzed, err := b.Bind(nullCountRaw("uuidv7", child), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := analyzed.(functions.UUIDGeneration); !ok || analyzed.Properties().Volatility != expr.Volatile {
			t.Fatal("analysis lost symbolic volatile provider", analyzed)
		}
		for _, preflight := range []bool{false, true} {
			for _, decisive := range []bool{false, true} {
				// Deliberately omit the session: any provider demand would fail.
				evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
				var planned expr.Expr
				var changed bool
				if preflight {
					planned, changed, err = preflightCoercionsWithPolicy(evaluation, analyzed, decisive)
				} else {
					planned, changed, err = simplifyConditional(evaluation, analyzed, decisive)
				}
				if err != nil || !changed {
					t.Fatal("strict NULL simplification", preflight, decisive, changed, err)
				}
				literal, ok := planned.(plan.LiteralExpr)
				if !ok || !literal.Value.IsNull() || literal.ResultUsage() != analyzed.ResultUsage() || !generationImmutable(planned) || exec.PlannedVolatility(planned) != expr.Immutable {
					t.Fatal("strict NULL physical tree", planned)
				}
				value, err := planned.Eval(evaluation)
				if err != nil || !value.IsNull() || value.Type() != b.Type(sqltype.UUID) {
					t.Fatal("typed NULL demanded session", value, err)
				}
			}
		}
		// Planning cannot erase the original definition used by Dump/deparse.
		if _, ok := analyzed.(functions.UUIDGeneration); !ok || analyzed.Properties().Volatility != expr.Volatile {
			t.Fatal("analysis mutated")
		}
	}
	for _, raw := range []parser.FunctionExpr{nullCountRaw("uuidv7"), nullCountRaw("uuidv7", zero)} {
		analyzed, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		planned, _, err := preflightCoercionsWithPolicy(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, analyzed, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := planned.(functions.UUIDGeneration); !ok || exec.PlannedVolatility(planned) != expr.Volatile || generationImmutable(planned) {
			t.Fatal("real provider admitted as immutable", planned)
		}
	}
}
