package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// Public SQL behavior is covered by the shared reference definitions. These
// units exercise retained provider authority and typed expression-tree demand.
func TestDatetimeFormattingTypedConsumers(t *testing.T) {
	e := New()
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	text := b.typ(sqltype.Text)
	for _, signature := range []struct {
		name           string
		source, result sqltype.ID
	}{
		{"to_char", sqltype.Timestamp, sqltype.Text}, {"to_char", sqltype.Timestamptz, sqltype.Text}, {"to_char", sqltype.Interval, sqltype.Text},
		{"to_date", sqltype.Text, sqltype.Date}, {"to_timestamp", sqltype.Text, sqltype.Timestamptz},
	} {
		source, result := b.typ(signature.source), b.typ(signature.result)
		template := "YYYY"
		datetimeFormatPlan, err := sqltype.BindDatetimeFormat(ctx, signature.name, result.Usage(), source.Usage(), &template)
		if err != nil {
			t.Fatal(err)
		}
		node := functions.DatetimeFormatting{Info: expr.Info{Usage: result.Usage(), Property: expr.Properties{Volatility: expr.Stable}}, Plan: datetimeFormatPlan, Args: [2]expr.Expr{
			plan.LiteralExpr{Info: expr.Info{Usage: source.Usage()}, Value: sqltype.Null(source)},
			plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, template)},
		}}
		if exec.PlannedVolatility(node) != expr.Stable || generationImmutable(node) {
			t.Fatal("lost intrinsic STABLE classification", signature)
		}
		if !b.sameGroupExpression(node, node) {
			t.Fatal("semantic identity lost", signature)
		}
		other := node
		other.Args[1] = plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "MM")}
		if b.sameGroupExpression(node, other) {
			t.Fatal("template omitted from semantic identity", signature)
		}
		value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
		if err != nil || !value.IsNull() || value.Type() != result {
			t.Fatal(value, err)
		}
		replacements := map[sqltype.ID]*sqltype.Descriptor{}
		for _, d := range sqltype.Builtins() {
			replacements[d.ID()] = d
		}
		rebound, changed, err := retagBoundTypes(New().CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacements[d.ID()] })
		if err != nil || !changed {
			t.Fatal("provider rebind", rebound, changed, err)
		}
		retained := rebound.(functions.DatetimeFormatting)
		value, err = retained.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
		if err != nil || value.Type() != replacements[signature.result] || node.ResultUsage().Type != result {
			t.Fatal("rebind changed source authority or retained obsolete result", value, err)
		}
	}
}

func TestDatetimeFormattingBindingAuthority(t *testing.T) {
	ctx := context.Background()
	zone, err := sqltype.ResolveZone("UTC")
	if err != nil {
		t.Fatal(err)
	}
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{Zone: zone}, nil)
	text := func(s string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: s}}
	}
	raw := func(name string, args ...parser.Expr) parser.FunctionExpr {
		return parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args, Pos: parser.Position{Character: 8}}
	}
	for _, signature := range []struct {
		name, source string
		result       sqltype.ID
	}{
		{"to_char", "timestamp", sqltype.Text}, {"to_char", "timestamptz", sqltype.Text}, {"to_char", "interval", sqltype.Text},
		{"to_date", "text", sqltype.Date}, {"to_timestamp", "text", sqltype.Timestamptz},
	} {
		var input parser.Expr = text("2006")
		if signature.source != "text" {
			value := "2006-01-01"
			if signature.source == "interval" {
				value = "25 months"
			}
			input = parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: signature.source}, Text: value}
		}
		bound, err := b.bindFunctionProvider(raw(signature.name, input, text("YYYY")))
		if err != nil {
			t.Fatal(signature, err)
		}
		node, ok := bound.(functions.DatetimeFormatting)
		if !ok || node.ResultUsage().Type.ID() != signature.result || node.ResultUsage().Typmod != -1 || node.Properties().Volatility != expr.Stable {
			t.Fatal("signature authority", signature, bound)
		}
	}
	integer := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}
	bound, err := b.bindFunctionProvider(raw("to_char", integer, text("FM9")))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := bound.(functions.NumericFormatting); !ok {
		t.Fatal("numeric overload did not route to its owner", bound)
	}
	for _, c := range []struct {
		call    parser.FunctionExpr
		visible bool
		code    string
	}{
		{raw("to_char", parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}, text("YYYY")), true, "42725"},
		{raw("to_char", parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "time"}, Text: "12:00"}, text("HH24")), true, "0A000"},
		{raw("to_timestamp", integer), true, "0A000"},
		{raw("to_date", integer, text("YYYY")), true, "42883"},
		{raw("to_date", text("2006"), text("YYYY")), false, "42883"},
		{raw("to_date", text("2006"), text("YYYY"), text("extra")), true, "42883"},
	} {
		call := c.call
		if !c.visible {
			// A schema-qualified name outside pg_catalog hides the builtin.
			call.Name.Parts = append([]parser.Identifier{{Text: "public"}}, call.Name.Parts...)
		}
		_, err := b.bindFunctionProvider(call)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != c.code || diagnostic.Position != "8" {
			t.Fatal("binder diagnostic/ownership", c, err)
		}
	}
}
