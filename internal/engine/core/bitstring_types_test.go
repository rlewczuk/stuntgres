package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestBitstringLiteralBindingAndDeparse(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	varying := b.Type(sqltype.VarBit).Usage()
	varying.Typmod = 5
	raw := parser.LiteralExpr{Value: parser.Value{Kind: parser.BitStringValue, Text: "x0f", Pos: parser.Position{Offset: 7, Character: 8}}}
	node, err := b.Bind(raw, analyze.ExpressionPolicy{})
	if err != nil || node.ResultUsage().Type.ID() != sqltype.Bit || node.ResultUsage().Typmod != -1 || node.ResultUsage().Collation != (sqltype.CollationRef{}) {
		t.Fatal(node, err)
	}
	v, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
	if err != nil {
		t.Fatal(err)
	}
	bits, err := v.Bits()
	if err != nil || bits != "00001111" {
		t.Fatal(bits, err)
	}
	literal, err := regobj.ConstantLiteral(ctx, node.(plan.LiteralExpr))
	if err != nil || literal != "B'00001111'" {
		t.Fatal(literal, err)
	}
	// A destination cannot reinterpret a known B/X constant as unknown text.
	node, err = b.Bind(parser.LiteralExpr{Value: parser.Value{Kind: parser.BitStringValue, Text: "b001", Pos: raw.Value.Pos}}, analyze.ExpressionPolicy{Expected: varying, Context: analyze.AssignmentContext})
	if err != nil {
		t.Fatal(err)
	}
	v, err = node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
	if err != nil || v.Type().ID() != sqltype.VarBit {
		t.Fatal(v, err)
	}
	bits, err = v.Bits()
	if err != nil || bits != "001" {
		t.Fatal(bits, err)
	}
	fixed := b.Type(sqltype.Bit).Usage()
	fixed.Typmod = 3
	for _, kind := range []parser.ValueKind{parser.BitStringValue, parser.StringValue} {
		text := "2"
		if kind == parser.BitStringValue {
			text = "b2"
		}
		_, err = b.Bind(parser.LiteralExpr{Value: parser.Value{Kind: kind, Text: text, Pos: raw.Value.Pos}}, analyze.ExpressionPolicy{Expected: fixed, Context: analyze.AssignmentContext})
		var d *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `"2" is not a valid binary digit`, Position: "8"}
		if !errors.As(err, &d) || *d != want {
			t.Fatal(d, err)
		}
	}
	if analyze.LiteralTypeName(raw.Value) != "bit" {
		t.Fatal("known literal lost type identity")
	}
}
