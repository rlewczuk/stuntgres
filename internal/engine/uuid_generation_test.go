package engine

import (
	"context"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestUUIDGenerationBinderNullAndDemand(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	for _, name := range []string{"gen_random_uuid", "uuidv4", "uuidv7"} {
		node, err := b.bind(nullCountRaw(name), expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		if node.ResultUsage().Type.ID() != sqltype.UUID || node.Properties().Volatility != Volatile {
			t.Fatal(name, node)
		}
	}
	node, err := b.bind(nullCountRaw("uuidv7", parser.ParameterExpr{Number: 1}), expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	if !node.Properties().ParameterDependent || node.Properties().Volatility != Volatile || closedUUIDDefault(node) {
		t.Fatal("parameter dependence", node.Properties())
	}
	uuid := b.typ(sqltype.UUID)
	iv := b.typ(sqltype.Interval)
	argument := literalExpression{expressionInfo{usage: iv.Usage()}, sqltype.Null(iv)}
	expr := functions.UUIDGeneration[BoundExpr, ExprContext, ExprProperties]{Info: functionInfo(expressionInfo{usage: uuid.Usage(), properties: ExprProperties{Volatility: Volatile}}), Name: "uuidv7", Args: []BoundExpr{argument}}
	// Tree inspection, descriptor remapping and rendering do not evaluate a child.
	stored, err := b.bind(nullCountRaw("uuidv7", parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "0"}}), expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	other := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	rebound, changed, err := retagBoundTypes(other.bindingCatalog(), stored, func(d *sqltype.Descriptor) *sqltype.Descriptor { return other.typ(d.ID()) })
	if err != nil || !changed {
		t.Fatal("UUID retag", changed, err)
	}
	n := rebound.(functions.UUIDGeneration[BoundExpr, ExprContext, ExprProperties])
	if n.Timestamp != other.typ(sqltype.Timestamptz) || n.ResultUsage().Type != other.typ(sqltype.UUID) || n.Args[0].ResultUsage().Type != other.typ(sqltype.Interval) || n.Properties() != stored.Properties() {
		t.Fatal("retag lost descriptor/dependence")
	}
	if generationImmutable(expr) {
		t.Fatal("UUID admitted to immutable consumer")
	}
	if closedUUIDDefault(functions.Random[BoundExpr, ExprContext, ExprProperties]{Info: functionInfo(expressionInfo{usage: uuid.Usage(), properties: ExprProperties{Volatility: Volatile}}), Name: "random"}) {
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
		analyzed, err := b.bind(nullCountRaw("uuidv7", child), expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := analyzed.(functions.UUIDGeneration[BoundExpr, ExprContext, ExprProperties]); !ok || analyzed.Properties().Volatility != Volatile {
			t.Fatal("analysis lost symbolic volatile provider", analyzed)
		}
		for _, preflight := range []bool{false, true} {
			for _, decisive := range []bool{false, true} {
				// Deliberately omit the session: any provider demand would fail.
				evaluation := &EvalContext{Context: context.Background()}
				var planned BoundExpr
				var changed bool
				if preflight {
					planned, changed, err = preflightCoercionsWithPolicy(evaluation, analyzed, decisive)
				} else {
					planned, changed, err = simplifyConditional(evaluation, analyzed, decisive)
				}
				if err != nil || !changed {
					t.Fatal("strict NULL simplification", preflight, decisive, changed, err)
				}
				literal, ok := planned.(literalExpression)
				if !ok || !literal.value.IsNull() || literal.ResultUsage() != analyzed.ResultUsage() || !generationImmutable(planned) || plannedVolatility(planned) != Immutable {
					t.Fatal("strict NULL physical tree", planned)
				}
				value, err := planned.Eval(evaluation)
				if err != nil || !value.IsNull() || value.Type() != b.typ(sqltype.UUID) {
					t.Fatal("typed NULL demanded session", value, err)
				}
			}
		}
		// Planning cannot erase the original definition used by Dump/deparse.
		if _, ok := analyzed.(functions.UUIDGeneration[BoundExpr, ExprContext, ExprProperties]); !ok || analyzed.Properties().Volatility != Volatile {
			t.Fatal("analysis mutated")
		}
	}
	for _, raw := range []parser.FunctionExpr{nullCountRaw("uuidv7"), nullCountRaw("uuidv7", zero)} {
		analyzed, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		planned, _, err := preflightCoercionsWithPolicy(&EvalContext{Context: context.Background()}, analyzed, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := planned.(functions.UUIDGeneration[BoundExpr, ExprContext, ExprProperties]); !ok || plannedVolatility(planned) != Volatile || generationImmutable(planned) {
			t.Fatal("real provider admitted as immutable", planned)
		}
	}
}
