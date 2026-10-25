package enginetests_test

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
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestNumericFormattingBindingAuthority(t *testing.T) {
	ctx := context.Background()
	b := enginetest.ExpressionBinder(t, ctx, engine.New(), sqltype.InputEnvironment{}, nil)
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
		bound, err := b.BindFunctionProvider(raw("to_char", input, text("FM9.000000000000000000")))
		if err != nil {
			t.Fatal(c, err)
		}
		n, ok := bound.(functions.NumericFormatting)
		if !ok || n.Args[0].ResultUsage().Type.ID() != c.id || n.ResultUsage().Type.ID() != sqltype.Text || n.ResultUsage().Typmod != -1 || n.Properties().Volatility != expr.Stable {
			t.Fatal("overload authority", c, bound)
		}
	}
	bound, err := b.BindFunctionProvider(raw("to_number", text("12"), text("FM99")))
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := bound.(functions.NumericFormatting); !ok || n.ResultUsage().Type.ID() != sqltype.Numeric || n.ResultUsage().Typmod != -1 || n.Properties().Volatility != expr.Stable {
		t.Fatal(bound)
	}
	// An invalid constant mask belongs to demanded execution, not binding.
	if _, err := b.BindFunctionProvider(raw("to_char", parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "numeric"}, Text: "12"}, text("9.9.9"))); err != nil {
		t.Fatal(err)
	}
}
func TestNumericFormattingDemandAndRetag(t *testing.T) {
	ctx := context.Background()
	b := enginetest.ExpressionBinder(t, ctx, engine.New(), sqltype.InputEnvironment{}, nil)
	numeric, text := b.Type(sqltype.Numeric), b.Type(sqltype.Text)
	mask := "9.9.9"
	numericFormatPlan, err := sqltype.BindNumericFormat(ctx, "to_char", text.Usage(), numeric.Usage(), &mask)
	if err != nil {
		t.Fatal(err)
	}
	// Pure NULL/sibling-error runtime demand is exercised locally. Retain
	// genuine STABLE/group/retag owner invariants with executable literals.
	n := functions.NumericFormatting{Info: expr.Info{Usage: text.Usage(), Property: expr.Properties{Volatility: expr.Stable}}, Plan: numericFormatPlan, Args: [2]expr.Expr{
		plan.LiteralExpr{Info: expr.Info{Usage: numeric.Usage()}, Value: sqltype.Null(numeric)},
		plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, mask)},
	}}
	simplified, _, err := exec.SimplifyConditional(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}, n, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := simplified.(functions.NumericFormatting); !ok {
		t.Fatal("STABLE demand lost through NULL folding", simplified)
	}
	if exec.PlannedVolatility(n) != expr.Stable || plan.GenerationImmutable(n) || !b.SameGroupExpression(n, n) {
		t.Fatal("intrinsic STABLE/semantic identity")
	}
	other := n
	other.Args[1] = plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "FM99")}
	if b.SameGroupExpression(n, other) {
		t.Fatal("mask omitted from group identity")
	}
	replacements := map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		replacements[d.ID()] = d
	}
	rebound, changed, err := stored.RetagTypes(engine.New().CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacements[d.ID()] })
	if err != nil || !changed {
		t.Fatal(rebound, changed, err)
	}
	value, err := rebound.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
	if err != nil || value.Type() != replacements[sqltype.Text] || n.ResultUsage().Type != text {
		t.Fatal("retained descriptor ownership", value, err)
	}
}

func TestNumericFormattingUUIDDefaultAdmission(t *testing.T) {
	ctx := context.Background()
	b := enginetest.ExpressionBinder(t, ctx, engine.New(), sqltype.InputEnvironment{}, nil)
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
		node, err := b.BindFunction(raw)
		if err != nil {
			t.Fatal(err)
		}
		numeric, ok := node.(functions.NumericFormatting)
		if !ok || numeric.Properties().Volatility != expr.Volatile {
			t.Fatal("numeric wrapper lost UUID child volatility", node)
		}
		if !analyze.ClosedUUIDDefault(numeric.Args[0]) {
			t.Fatal("existing closed UUID source composition was rejected")
		}
		if !analyze.ClosedUUIDDefault(numeric) {
			t.Error("numeric wrapper rejected otherwise admitted closed UUID DEFAULT", raw)
		}
		for _, property := range []expr.Properties{
			{Volatility: expr.Volatile, RowDependent: true},
			{Volatility: expr.Volatile, ParameterDependent: true},
			{Volatility: expr.Volatile, StageDependent: true},
		} {
			dependent := numeric
			dependent.Property = property
			if analyze.ClosedUUIDDefault(dependent) {
				t.Fatal("dependent numeric DEFAULT admitted", property)
			}
		}
	}
	for _, raw := range []parser.FunctionExpr{
		call("to_char", call("random"), text("FM9")),
		call("to_number", call("format", text("%s"), call("random")), text("9")),
		call("to_char", count, call("format", text("%s"), call("random"))),
	} {
		node, err := b.BindFunction(raw)
		if err != nil {
			t.Fatal(err)
		}
		if analyze.ClosedUUIDDefault(node) {
			t.Fatal("unrelated volatile descendant admitted through numeric wrapper", raw)
		}
	}
}
