package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestStringNumericBaseMD5InternalCharAdmission(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	for _, null := range []bool{false, true} {
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		literal := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "a"}}
		if null {
			literal.Value.Kind = parser.NullValue
		}
		cast := parser.CastExpr{Child: literal, TypeName: parser.TypeName{Name: "char", Quoted: true}}
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "md5"}}}, Args: []parser.Expr{cast}}
		node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(null, err)
		}
		value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
		if err != nil || value.Type() != b.Type(sqltype.Text) || value.IsNull() != null {
			t.Fatal(value, err)
		}
		if !null {
			digest, err := value.Text()
			if err != nil || digest != "0cc175b9c0f1b6a831c399e269772661" {
				t.Fatal(digest, err)
			}
		}
		// Canonical view definitions require direct CHAR string/NULL casts.
		// Both operand roles must preserve the same typed literal value.
		for _, textual := range []bool{false, true} {
			operand, err := b.StringOperand(cast, textual)
			if err != nil {
				t.Fatal("literal CHAR operand", textual, err)
			}
			value, err := operand.Node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
			if err != nil || value.Type() != b.Type(sqltype.Char) || value.IsNull() != null {
				t.Fatal("literal CHAR value", textual, value, err)
			}
			if !null {
				got, err := value.Type().Output(ctx, value)
				if err != nil || got != "a" {
					t.Fatal(got, err)
				}
			}
		}
		direct, err := b.Bind(cast, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil || direct.ResultUsage().Type != b.Type(sqltype.Char) {
			t.Fatal("direct literal CHAR cast", direct, err)
		}
		usage, err := core.EngineResolveType(e, cast.TypeName, binderOperation(b))
		if err != nil || usage.Type != b.Type(sqltype.Char) {
			t.Fatal("ordinary CHAR admission", usage, err)
		}
	}
	for _, established := range []bool{false, true} {
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		char := b.Type(sqltype.Char).Usage()
		if established {
			b.Params.Parameters[1] = char
		}
		cast := parser.CastExpr{Child: parser.ParameterExpr{Number: 1}, TypeName: parser.TypeName{Name: "char", Quoted: true}}
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "md5"}}}, Args: []parser.Expr{cast}}
		node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal("CHAR parameter annotation", established, err)
		}
		if node.ResultUsage().Type != b.Type(sqltype.Text) || b.Params.Parameters[1] != char {
			t.Fatal("lost annotated CHAR parameter contract", established, node.ResultUsage(), b.Params.Parameters[1])
		}
	}
}
