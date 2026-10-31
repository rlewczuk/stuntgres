package types_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestUUIDGenerationStrictNullPhysicalTrees(t *testing.T) {
	b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
	null := parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
	typedNull := parser.CastExpr{Child: null, TypeName: parser.TypeName{Name: "interval"}}
	zero := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "0"}}
	yes := parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true}}
	for _, child := range []parser.Expr{
		null, typedNull,
		parser.CaseExpr{Arms: []parser.CaseArm{{Condition: yes, Result: typedNull}}, Else: zero},
		parser.ConditionalExpr{Kind: parser.Coalesce, Args: []parser.Expr{typedNull, typedNull}},
	} {
		analyzed, err := b.Bind(enginetest.FunctionCall("uuidv7", child), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
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
					planned, changed, err = exec.PreflightCoercionsWithPolicy(evaluation, analyzed, decisive)
				} else {
					planned, changed, err = exec.SimplifyConditional(evaluation, analyzed, decisive)
				}
				if err != nil || !changed {
					t.Fatal("strict NULL simplification", preflight, decisive, changed, err)
				}
				literal, ok := planned.(plan.LiteralExpr)
				if !ok || !literal.Value.IsNull() || literal.ResultUsage() != analyzed.ResultUsage() || !plan.GenerationImmutable(planned) || exec.PlannedVolatility(planned) != expr.Immutable {
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
	for _, raw := range []parser.FunctionExpr{enginetest.FunctionCall("uuidv7"), enginetest.FunctionCall("uuidv7", zero)} {
		analyzed, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		planned, _, err := exec.PreflightCoercionsWithPolicy(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, analyzed, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := planned.(functions.UUIDGeneration); !ok || exec.PlannedVolatility(planned) != expr.Volatile || plan.GenerationImmutable(planned) {
			t.Fatal("real provider admitted as immutable", planned)
		}
	}
}
