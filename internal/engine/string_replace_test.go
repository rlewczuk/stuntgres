package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
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
		node, err := functions.BindString(b, raw, true)
		if err != nil {
			t.Fatal(err)
		}
		n := node.(functions.String)
		for i := range args {
			id := sqltype.Text
			if i >= 2 {
				id = sqltype.Int4
			}
			if b.Params.Parameters[i+1].Type != b.Type(id) || n.Plan.ArgumentUsage(i).Type != b.Type(id) || n.Textual[i] != (i < 2) {
				t.Fatal(i, b.Params.Parameters, n.Plan.ArgumentUsage(i))
			}
		}
		if !n.Properties().ParameterDependent || n.ResultUsage() != b.Type(sqltype.Text).Usage() {
			t.Fatal(n)
		}
	}
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	literal := func(text string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: text}}
	}
	small := parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 2}}, TypeName: parser.TypeName{Name: "int2"}, Syntax: parser.PostfixCast}
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "overlay"}}}, Args: []parser.Expr{literal("abc"), literal("X"), small}}
	node, err := functions.BindString(b, raw, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := node.(functions.String).Args[2].(plan.CoercionExpr); !ok || node.(functions.String).Plan.ArgumentUsage(2).Type != b.Type(sqltype.Int4) {
		t.Fatal("INT2 implicit formal", node)
	}
}

func TestStringReplacementFourChildrenAndRetag(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	text, integer := b.Type(sqltype.Text), b.Type(sqltype.Int4)
	two, _ := sqltype.Integer(integer, 2)
	one, _ := sqltype.Integer(integer, 1)
	args := []expr.Expr{
		plan.LiteralExpr{Info: expr.Info{Usage: text.Usage(), Property: expr.Properties{ParameterDependent: true}}, Value: sqltype.Null(text)},
		plan.LiteralExpr{Info: expr.Info{Usage: text.Usage(), Property: expr.Properties{RowDependent: true}}, Value: sqltype.String(text, "X")},
		plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{Volatility: expr.Stable}}, Value: two},
		plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{Volatility: expr.Volatile, StageDependent: true}}, Value: one},
	}
	bound, err := b.StringNode("overlay", text.Usage(), parser.Position{}, args, [4]bool{true, true}, parser.OverlayStringSyntax)
	if err != nil {
		t.Fatal(err)
	}
	n := bound.(functions.String)
	// Keep owner property merging and descriptor retag integration. Four-child
	// runtime NULL/error demand is exercised locally by the extracted carrier.
	if n.Properties() != (expr.Properties{Volatility: expr.Volatile, RowDependent: true, ParameterDependent: true, StageDependent: true}) {
		t.Fatal(n.Properties())
	}
	// Retag executable literal children, retaining all four formal owners.
	original := bound.(functions.String)
	original.Args = []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "abc")}, plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "X")}, plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: two}, plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: one}}
	fresh := map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		fresh[d.ID()] = d
	}
	rebound, changed, err := stored.RetagTypes(New().CatalogRead(), original, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
	if err != nil || !changed {
		t.Fatal(rebound, changed, err)
	}
	retained := rebound.(functions.String)
	for i := range retained.Args {
		id := sqltype.Text
		if i >= 2 {
			id = sqltype.Int4
		}
		if retained.Plan.ArgumentUsage(i).Type != fresh[id] || original.Plan.ArgumentUsage(i).Type == fresh[id] {
			t.Fatal("formal owner mutation", i)
		}
	}
	got, err := retained.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	s, _ := got.Text()
	if err != nil || s != "aXc" || got.Type() != fresh[sqltype.Text] {
		t.Fatal(got, err)
	}
}

func TestStringPrefixSemanticIdentityAndCollation(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	text, boolean := b.Type(sqltype.Text), b.Type(sqltype.Bool)
	args := []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "abc")}, plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "a")}}
	function, err := b.StringNode("starts_with", boolean.Usage(), parser.Position{}, args, [4]bool{true, true}, parser.OrdinaryStringSyntax)
	if err != nil {
		t.Fatal(err)
	}
	operator, err := b.StringNode("starts_with", boolean.Usage(), parser.Position{}, args, [4]bool{true, true}, parser.PrefixStringSyntax)
	if err != nil || !b.SameGroupExpression(function, operator) {
		t.Fatal("syntax changed semantic grouping", operator, err)
	}
	left, right := text.Usage(), text.Usage()
	left.Collation.ID, right.Collation.ID = 950, 951
	args = []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: left}, Value: sqltype.String(text, "")}, plan.LiteralExpr{Info: expr.Info{Usage: right}, Value: sqltype.String(text, "")}}
	conflict, err := b.StringNode("starts_with", boolean.Usage(), parser.Position{}, args, [4]bool{true, true}, parser.PrefixStringSyntax)
	if err != nil {
		t.Fatal(err)
	}
	n := conflict.(functions.String)
	if n.ResultUsage() != boolean.Usage() || n.CollationIndeterminate() || !n.Plan.CollationIndeterminate() {
		t.Fatal("input conflict leaked to BOOL", n)
	}
	_, err = n.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42P22" {
		t.Fatal("empty prefix bypassed demand", err)
	}
}

func TestStringPrefixPrettyLogicalGrouping(t *testing.T) {
	for _, parent := range []pgRenderParent{
		{},
		{Precedence: 1, Operator: "OR"},
		{Precedence: 2, Operator: "AND", Right: true},
		{Precedence: 3, Operator: "NOT"},
	} {
		if stored.OperatorGrouped("^@", true, parent) {
			t.Fatal("prefix operator grouped under Boolean parent", parent)
		}
	}
	if !stored.OperatorGrouped("^@", true, pgRenderParent{Precedence: 5, Operator: "^@", Right: true}) {
		t.Fatal("nested prefix operand lost required grouping")
	}
	if !stored.OperatorGrouped("-", false, pgRenderParent{Precedence: 3, Operator: "NOT"}) {
		t.Fatal("unary scalar operator lost existing grouping")
	}
}
