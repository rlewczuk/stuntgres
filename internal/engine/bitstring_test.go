package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestBitstringLiteralBindingAndDeparse(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	varying := b.typ(sqltype.VarBit).Usage()
	varying.Typmod = 5
	raw := parser.LiteralExpr{Value: parser.Value{Kind: parser.BitStringValue, Text: "x0f", Pos: parser.Position{Offset: 7, Character: 8}}}
	node, err := b.bind(raw, expressionPolicy{})
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
	literal, err := literalSQL(ctx, node.(plan.LiteralExpr))
	if err != nil || literal != "B'00001111'" {
		t.Fatal(literal, err)
	}
	// A destination cannot reinterpret a known B/X constant as unknown text.
	node, err = b.bind(parser.LiteralExpr{Value: parser.Value{Kind: parser.BitStringValue, Text: "b001", Pos: raw.Value.Pos}}, expressionPolicy{expected: varying, context: assignmentContext})
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
	fixed := b.typ(sqltype.Bit).Usage()
	fixed.Typmod = 3
	for _, kind := range []parser.ValueKind{parser.BitStringValue, parser.StringValue} {
		text := "2"
		if kind == parser.BitStringValue {
			text = "b2"
		}
		_, err = b.bind(parser.LiteralExpr{Value: parser.Value{Kind: kind, Text: text, Pos: raw.Value.Pos}}, expressionPolicy{expected: fixed, context: assignmentContext})
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

func TestBitstringFormatLiteralReplay(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	for _, tc := range []struct{ input, canonical string }{{"b001", "001"}, {"x0f", "00001111"}, {"b", ""}} {
		raw := nullCountRaw("format", formatUnitLiteral("%s"), parser.LiteralExpr{Value: parser.Value{Kind: parser.BitStringValue, Text: tc.input}})
		node, err := b.bind(raw, expressionPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		text, err := defaultSQL(ctx, e.CatalogRead(), node)
		want := "pg_catalog.format('%s', B'" + tc.canonical + "')"
		if err != nil || text != want {
			t.Fatal(text, want, err)
		}
		// Rendering literal identity must not enable even an explicit BIT
		// identity cast; serialization fixes the owning seam instead.
		bit := b.typ(sqltype.Bit).Usage()
		if _, err := sqltype.PlanCoercion(bit, bit, sqltype.Explicit); err == nil {
			t.Fatal("BIT cast admission widened")
		}
	}
}

func TestBitstringModifierProvenanceResolution(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	one := int32(1)
	for _, name := range []string{"bit", "varbit", "bit varying"} {
		raw := parser.TypeName{Name: name, Pos: parser.Position{Offset: 17, Character: 18}, Modifiers: []parser.TypeModifier{{Integer: &one, Pos: parser.Position{Offset: 21, Character: 22}}}}
		u, err := e.resolveType(raw, binderOperation(b))
		if err != nil || u.Typmod != 1 {
			t.Fatal(u, err)
		}
		for _, modifier := range []parser.TypeModifier{
			{NonSimple: true, Raw: "1/0"},
			{NonSimple: true, Raw: "not an integer"},
			{NonSimple: true, Integer: &one},
			{NonSimple: true, UnaryPlus: true, Integer: &one},
		} {
			raw.Modifiers[0] = modifier
			_, err := e.resolveType(raw, binderOperation(b))
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "type modifiers must be simple constants or identifiers", Position: "18"}
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal(name, modifier, diagnostic, err)
			}
		}
	}
}
