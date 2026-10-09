package engine

import (
	"context"
	"errors"
	"reflect"
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
		plan, err := sqltype.BindDatetimeFormat(ctx, signature.name, result.Usage(), source.Usage(), &template)
		if err != nil {
			t.Fatal(err)
		}
		node := datetimeFormattingExpression{expressionInfo: expressionInfo{usage: result.Usage(), properties: ExprProperties{Volatility: Stable}}, plan: plan, args: [2]BoundExpr{
			literalExpression{expressionInfo{usage: source.Usage()}, sqltype.Null(source)},
			literalExpression{expressionInfo{usage: text.Usage()}, sqltype.String(text, template)},
		}}
		if plannedVolatility(node) != Stable || generationImmutable(node) {
			t.Fatal("lost intrinsic STABLE classification", signature)
		}
		if !b.sameGroupExpression(node, node) {
			t.Fatal("semantic identity lost", signature)
		}
		other := node
		other.args[1] = literalExpression{expressionInfo{usage: text.Usage()}, sqltype.String(text, "MM")}
		if b.sameGroupExpression(node, other) {
			t.Fatal("template omitted from semantic identity", signature)
		}
		value, err := node.Eval(&EvalContext{Context: ctx})
		if err != nil || !value.IsNull() || value.Type() != result {
			t.Fatal(value, err)
		}
		replacements := map[sqltype.ID]*sqltype.Descriptor{}
		for _, d := range sqltype.Builtins() {
			replacements[d.ID()] = d
		}
		rebound, changed, err := retagBoundTypes(node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacements[d.ID()] })
		if err != nil || !changed {
			t.Fatal("provider rebind", rebound, changed, err)
		}
		retained := rebound.(datetimeFormattingExpression)
		value, err = retained.Eval(&EvalContext{Context: ctx})
		if err != nil || value.Type() != replacements[signature.result] || node.ResultUsage().Type != result {
			t.Fatal("rebind changed source authority or retained obsolete result", value, err)
		}
	}
}

func TestDatetimeFormattingChildDemand(t *testing.T) {
	e := New()
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	source, text := b.typ(sqltype.Timestamp), b.typ(sqltype.Text)
	template := "YYYY"
	plan, err := sqltype.BindDatetimeFormat(ctx, "to_char", text.Usage(), source.Usage(), &template)
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	node := datetimeFormattingExpression{expressionInfo: expressionInfo{usage: text.Usage(), properties: ExprProperties{Volatility: Volatile, RowDependent: true, ParameterDependent: true}}, plan: plan, args: [2]BoundExpr{
		mathObservedArgument{expressionInfo: expressionInfo{usage: source.Usage()}, value: sqltype.Null(source), order: &order, index: 0},
		mathObservedArgument{expressionInfo: expressionInfo{usage: text.Usage()}, value: sqltype.String(text, template), order: &order, index: 1},
	}}
	value, err := node.Eval(&EvalContext{Context: ctx})
	if err != nil || !value.IsNull() || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("row NULL suppressed demanded sibling", value, order, err)
	}
	failure := errors.New("demanded formatting sibling")
	node.args[1] = mathObservedArgument{expressionInfo: expressionInfo{usage: text.Usage()}, order: &order, index: 1, err: failure}
	order = nil
	if _, err := node.Eval(&EvalContext{Context: ctx}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(order, err)
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
		bound, err := b.bindDatetimeFormatting(raw(signature.name, input, text("YYYY")), true)
		if err != nil {
			t.Fatal(signature, err)
		}
		node, ok := bound.(datetimeFormattingExpression)
		if !ok || node.ResultUsage().Type.ID() != signature.result || node.ResultUsage().Typmod != -1 || node.Properties().Volatility != Stable {
			t.Fatal("signature authority", signature, bound)
		}
	}
	integer := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}
	for _, c := range []struct {
		call    parser.FunctionExpr
		visible bool
		code    string
	}{
		{raw("to_char", parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}, text("YYYY")), true, "42725"},
		{raw("to_char", integer, text("YYYY")), true, "0A000"},
		{raw("to_char", parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "time"}, Text: "12:00"}, text("HH24")), true, "0A000"},
		{raw("to_timestamp", integer), true, "0A000"},
		{raw("to_date", integer, text("YYYY")), true, "42883"},
		{raw("to_date", text("2006"), text("YYYY")), false, "42883"},
		{raw("to_date", text("2006"), text("YYYY"), text("extra")), true, "42883"},
	} {
		_, err := b.bindDatetimeFormatting(c.call, c.visible)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != c.code || diagnostic.Position != "8" {
			t.Fatal("binder diagnostic/ownership", c, err)
		}
	}
}
