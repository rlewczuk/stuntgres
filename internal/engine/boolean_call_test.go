package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestBooleanCallPredicateBinding(t *testing.T) {
	e := New()
	pos := parser.Position{Offset: 15, Character: 16}
	for _, name := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
		for _, op := range []parser.LogicalOperator{parser.LogicalAnd, parser.LogicalOr} {
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			call := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name, Pos: pos}}}, Args: []parser.Expr{parser.ParameterExpr{Number: 1, Pos: parser.Position{Character: 40}}}, Pos: pos}
			raw := parser.LogicalExpr{Op: op, Left: parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: op == parser.LogicalOr}}, Right: call}
			node, err := b.Bind(raw, expressionPolicy{Context: ProjectionContext})
			if err != nil {
				t.Fatal(name, err)
			}
			p := node.Properties()
			if node.ResultUsage() != b.Type(sqltype.Bool).Usage() || !p.ParameterDependent || p.RowDependent || p.Volatility != expr.Stable {
				t.Fatal(name, node.ResultUsage(), p)
			}
			info, err := b.Finish()
			if err != nil || info.Count != 1 || info.Types[0] != b.Type(sqltype.Oid).Usage() {
				t.Fatal(name, info, err)
			}
			found := false
			plan.WalkExpression(node, func(child expr.Expr) {
				if _, ok := child.(functions.VisibilityInquiry); ok {
					found = true
				}
			})
			if !found {
				t.Fatal("decisive constant lost analyzed inquiry", name)
			}
			call.Args = []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue, Pos: pos}}}
			null, err := b.BindBoolean(call, FilterContext, "WHERE")
			if err != nil || null.ResultUsage() != b.Type(sqltype.Bool).Usage() {
				t.Fatal(name, null, err)
			}
			literal, ok := null.(plan.LiteralExpr)
			if !ok || !literal.Value.IsNull() {
				t.Fatal("strict NULL did not retain typed BOOL", name, null)
			}
		}
	}
}

func TestBooleanCallPredicateSetRejection(t *testing.T) {
	e := New()
	pos := parser.Position{Offset: 15, Character: 16}
	for _, boolean := range []bool{true, false} {
		value := parser.Value{Kind: parser.BooleanValue, Boolean: true, Pos: pos}
		if !boolean {
			value = parser.Value{Kind: parser.IntegerValue, Integer: 1, Pos: pos}
		}
		call := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "unnest", Pos: pos}}}, Args: []parser.Expr{parser.ArrayExpr{Elements: []parser.Expr{parser.LiteralExpr{Value: value}}, Pos: pos}}, Pos: pos}
		for _, construct := range []string{"AND", "OR", "NOT"} {
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			b.Clause.AggregateClause = "SELECT"
			node, err := b.BindBoolean(call, ProjectionContext, construct)
			message := "argument of " + construct + " must not return a set"
			if !boolean {
				message = "argument of " + construct + " must be type boolean, not type integer"
			}
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: message, Position: "16"}
			var got *sqlstate.Error
			if node != nil || !errors.As(err, &got) || *got != want {
				t.Fatal(node, err, got, want)
			}
		}
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		b.Clause.AggregateClause = "SELECT"
		node, err := b.Bind(call, expressionPolicy{Context: ProjectionContext})
		if err != nil || !exec.SourceReturnsSet(node) {
			t.Fatal("standalone set projection changed", node, err)
		}
	}
}
