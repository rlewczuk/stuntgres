package engine

import (
	"context"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestNumericFormattingBindingAuthority(t *testing.T) {
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	text := func(s string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: s}}
	}
	raw := func(name string, args ...parser.Expr) parser.FunctionExpr {
		return parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args, Pos: parser.Position{Character: 8}}
	}
	for _, c := range []struct {
		source string
		id     sqltype.ID
	}{
		{"int4", sqltype.Int4}, {"int8", sqltype.Int8}, {"numeric", sqltype.Numeric}, {"float4", sqltype.Float4}, {"float8", sqltype.Float8}, {"int2", sqltype.Float8},
	} {
		input := parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: c.source}, Text: "1"}
		bound, err := b.bindFunctionProvider(raw("to_char", input, text("FM9.000000000000000000")))
		if err != nil {
			t.Fatal(c, err)
		}
		n, ok := bound.(functions.NumericFormatting)
		if !ok || n.Args[0].ResultUsage().Type.ID() != c.id || n.ResultUsage().Type.ID() != sqltype.Text || n.ResultUsage().Typmod != -1 || n.Properties().Volatility != Stable {
			t.Fatal("overload authority", c, bound)
		}
	}
	bound, err := b.bindNumericFormatting(raw("to_number", text("12"), text("FM99")), true)
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := bound.(functions.NumericFormatting); !ok || n.ResultUsage().Type.ID() != sqltype.Numeric || n.ResultUsage().Typmod != -1 || n.Properties().Volatility != Stable {
		t.Fatal(bound)
	}
	// An invalid constant mask belongs to demanded execution, not binding.
	if _, err := b.bindFunctionProvider(raw("to_char", parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "numeric"}, Text: "12"}, text("9.9.9"))); err != nil {
		t.Fatal(err)
	}
}
func TestNumericFormattingDemandAndRetag(t *testing.T) {
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	numeric, text := b.typ(sqltype.Numeric), b.typ(sqltype.Text)
	mask := "9.9.9"
	plan, err := sqltype.BindNumericFormat(ctx, "to_char", text.Usage(), numeric.Usage(), &mask)
	if err != nil {
		t.Fatal(err)
	}
	// Pure NULL/sibling-error runtime demand is exercised locally. Retain
	// genuine STABLE/group/retag owner invariants with executable literals.
	n := functions.NumericFormatting{Info: expressionInfo{Usage: text.Usage(), Property: ExprProperties{Volatility: Stable}}, Plan: plan, Args: [2]BoundExpr{
		literalExpression{expressionInfo: expressionInfo{Usage: numeric.Usage()}, value: sqltype.Null(numeric)},
		literalExpression{expressionInfo: expressionInfo{Usage: text.Usage()}, value: sqltype.String(text, mask)},
	}}
	simplified, _, err := simplifyConditional(&EvalContext{Context: ctx}, n, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := simplified.(functions.NumericFormatting); !ok {
		t.Fatal("STABLE demand lost through NULL folding", simplified)
	}
	if plannedVolatility(n) != Stable || generationImmutable(n) || !b.sameGroupExpression(n, n) {
		t.Fatal("intrinsic STABLE/semantic identity")
	}
	other := n
	other.Args[1] = literalExpression{expressionInfo: expressionInfo{Usage: text.Usage()}, value: sqltype.String(text, "FM99")}
	if b.sameGroupExpression(n, other) {
		t.Fatal("mask omitted from group identity")
	}
	replacements := map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		replacements[d.ID()] = d
	}
	rebound, changed, err := retagBoundTypes(New().CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacements[d.ID()] })
	if err != nil || !changed {
		t.Fatal(rebound, changed, err)
	}
	value, err := rebound.Eval(&EvalContext{Context: ctx})
	if err != nil || value.Type() != replacements[sqltype.Text] || n.ResultUsage().Type != text {
		t.Fatal("retained descriptor ownership", value, err)
	}
}

func TestNumericFormattingUUIDDefaultAdmission(t *testing.T) {
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	text := func(s string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: s}}
	}
	call := func(name string, args ...parser.Expr) parser.FunctionExpr {
		return parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args}
	}
	count := call("num_nulls", call("uuidv4"))
	for _, raw := range []parser.FunctionExpr{
		call("to_char", count, text("FM9")),
		call("to_number", call("format", text("%s"), count), text("9")),
	} {
		node, err := b.bindFunction(raw)
		if err != nil {
			t.Fatal(err)
		}
		numeric, ok := node.(functions.NumericFormatting)
		if !ok || numeric.Properties().Volatility != Volatile {
			t.Fatal("numeric wrapper lost UUID child volatility", node)
		}
		if !closedUUIDDefault(numeric.Args[0]) {
			t.Fatal("existing closed UUID source composition was rejected")
		}
		if !closedUUIDDefault(numeric) {
			t.Error("numeric wrapper rejected otherwise admitted closed UUID DEFAULT", raw)
		}
		for _, property := range []ExprProperties{
			{Volatility: Volatile, RowDependent: true},
			{Volatility: Volatile, ParameterDependent: true},
			{Volatility: Volatile, StageDependent: true},
		} {
			dependent := numeric
			dependent.Property = property
			if closedUUIDDefault(dependent) {
				t.Fatal("dependent numeric DEFAULT admitted", property)
			}
		}
	}
	for _, raw := range []parser.FunctionExpr{
		call("to_char", call("random"), text("FM9")),
		call("to_number", call("format", text("%s"), call("random")), text("9")),
		call("to_char", count, call("format", text("%s"), call("random"))),
	} {
		node, err := b.bindFunction(raw)
		if err != nil {
			t.Fatal(err)
		}
		if closedUUIDDefault(node) {
			t.Fatal("unrelated volatile descendant admitted through numeric wrapper", raw)
		}
	}
}
