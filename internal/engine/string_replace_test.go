package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/functions"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestStringReplacementFormalInference(t *testing.T) {
	for _, arity := range []int{3, 4} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		args := make([]parser.Expr, arity)
		for i := range args {
			args[i] = parser.ParameterExpr{Number: i + 1, Pos: parser.Position{Character: 20 + i}}
		}
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "overlay"}}}, Args: args, Pos: parser.Position{Character: 8}}
		node, err := b.bindStringFunction(raw, true)
		if err != nil {
			t.Fatal(err)
		}
		n := node.(functions.String[BoundExpr, ExprContext, ExprProperties])
		for i := range args {
			id := sqltype.Text
			if i >= 2 {
				id = sqltype.Int4
			}
			if b.parameters[i+1].Type != b.typ(id) || n.Plan.ArgumentUsage(i).Type != b.typ(id) || n.Textual[i] != (i < 2) {
				t.Fatal(i, b.parameters, n.Plan.ArgumentUsage(i))
			}
		}
		if !n.Properties().ParameterDependent || n.ResultUsage() != b.typ(sqltype.Text).Usage() {
			t.Fatal(n)
		}
	}
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	literal := func(text string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: text}}
	}
	small := parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 2}}, TypeName: parser.TypeName{Name: "int2"}, Syntax: parser.PostfixCast}
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "overlay"}}}, Args: []parser.Expr{literal("abc"), literal("X"), small}}
	node, err := b.bindStringFunction(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := node.(functions.String[BoundExpr, ExprContext, ExprProperties]).Args[2].(coercionExpression); !ok || node.(functions.String[BoundExpr, ExprContext, ExprProperties]).Plan.ArgumentUsage(2).Type != b.typ(sqltype.Int4) {
		t.Fatal("INT2 implicit formal", node)
	}
}

func TestStringReplacementFourChildrenAndRetag(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	text, integer := b.typ(sqltype.Text), b.typ(sqltype.Int4)
	two, _ := sqltype.Integer(integer, 2)
	one, _ := sqltype.Integer(integer, 1)
	args := []BoundExpr{
		literalExpression{expressionInfo{Usage: text.Usage(), Property: ExprProperties{ParameterDependent: true}}, sqltype.Null(text)},
		literalExpression{expressionInfo{Usage: text.Usage(), Property: ExprProperties{RowDependent: true}}, sqltype.String(text, "X")},
		literalExpression{expressionInfo{Usage: integer.Usage(), Property: ExprProperties{Volatility: Stable}}, two},
		literalExpression{expressionInfo{Usage: integer.Usage(), Property: ExprProperties{Volatility: Volatile, StageDependent: true}}, one},
	}
	bound, err := b.stringNode("overlay", text.Usage(), parser.Position{}, args, [4]bool{true, true}, parser.OverlayStringSyntax)
	if err != nil {
		t.Fatal(err)
	}
	n := bound.(functions.String[BoundExpr, ExprContext, ExprProperties])
	// Keep owner property merging and descriptor retag integration. Four-child
	// runtime NULL/error demand is exercised locally by the extracted carrier.
	if n.Properties() != (ExprProperties{Volatility: Volatile, RowDependent: true, ParameterDependent: true, StageDependent: true}) {
		t.Fatal(n.Properties())
	}
	// Retag executable literal children, retaining all four formal owners.
	original := bound.(functions.String[BoundExpr, ExprContext, ExprProperties])
	original.Args = []BoundExpr{literalExpression{expressionInfo{Usage: text.Usage()}, sqltype.String(text, "abc")}, literalExpression{expressionInfo{Usage: text.Usage()}, sqltype.String(text, "X")}, literalExpression{expressionInfo{Usage: integer.Usage()}, two}, literalExpression{expressionInfo{Usage: integer.Usage()}, one}}
	fresh := map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		fresh[d.ID()] = d
	}
	rebound, changed, err := retagBoundTypes(New().CatalogRead(), original, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
	if err != nil || !changed {
		t.Fatal(rebound, changed, err)
	}
	retained := rebound.(functions.String[BoundExpr, ExprContext, ExprProperties])
	for i := range retained.Args {
		id := sqltype.Text
		if i >= 2 {
			id = sqltype.Int4
		}
		if retained.Plan.ArgumentUsage(i).Type != fresh[id] || original.Plan.ArgumentUsage(i).Type == fresh[id] {
			t.Fatal("formal owner mutation", i)
		}
	}
	got, err := retained.Eval(&EvalContext{Context: context.Background()})
	s, _ := got.Text()
	if err != nil || s != "aXc" || got.Type() != fresh[sqltype.Text] {
		t.Fatal(got, err)
	}
}

func TestStringPrefixSemanticIdentityAndCollation(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	text, boolean := b.typ(sqltype.Text), b.typ(sqltype.Bool)
	args := []BoundExpr{literalExpression{expressionInfo{Usage: text.Usage()}, sqltype.String(text, "abc")}, literalExpression{expressionInfo{Usage: text.Usage()}, sqltype.String(text, "a")}}
	function, err := b.stringNode("starts_with", boolean.Usage(), parser.Position{}, args, [4]bool{true, true}, parser.OrdinaryStringSyntax)
	if err != nil {
		t.Fatal(err)
	}
	operator, err := b.stringNode("starts_with", boolean.Usage(), parser.Position{}, args, [4]bool{true, true}, parser.PrefixStringSyntax)
	if err != nil || !b.sameGroupExpression(function, operator) {
		t.Fatal("syntax changed semantic grouping", operator, err)
	}
	left, right := text.Usage(), text.Usage()
	left.Collation.ID, right.Collation.ID = 950, 951
	args = []BoundExpr{literalExpression{expressionInfo{Usage: left}, sqltype.String(text, "")}, literalExpression{expressionInfo{Usage: right}, sqltype.String(text, "")}}
	conflict, err := b.stringNode("starts_with", boolean.Usage(), parser.Position{}, args, [4]bool{true, true}, parser.PrefixStringSyntax)
	if err != nil {
		t.Fatal(err)
	}
	n := conflict.(functions.String[BoundExpr, ExprContext, ExprProperties])
	if n.ResultUsage() != boolean.Usage() || n.CollationIndeterminate() || !n.Plan.CollationIndeterminate() {
		t.Fatal("input conflict leaked to BOOL", n)
	}
	_, err = n.Eval(&EvalContext{Context: context.Background()})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42P22" {
		t.Fatal("empty prefix bypassed demand", err)
	}
}

func TestStringPrefixPrettyLogicalGrouping(t *testing.T) {
	for _, parent := range []pgRenderParent{
		{},
		{precedence: 1, operator: "OR"},
		{precedence: 2, operator: "AND", right: true},
		{precedence: 3, operator: "NOT"},
	} {
		if pgOperatorGrouped("^@", true, parent) {
			t.Fatal("prefix operator grouped under Boolean parent", parent)
		}
	}
	if !pgOperatorGrouped("^@", true, pgRenderParent{precedence: 5, operator: "^@", right: true}) {
		t.Fatal("nested prefix operand lost required grouping")
	}
	if !pgOperatorGrouped("-", false, pgRenderParent{precedence: 3, operator: "NOT"}) {
		t.Fatal("unary scalar operator lost existing grouping")
	}
}
