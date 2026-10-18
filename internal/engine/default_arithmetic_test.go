package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestDefaultDescriptorProviderClosedArithmetic(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE d(i INT DEFAULT 1/0,u BIGINT DEFAULT -(CAST(7 AS BIGINT)))")
	view := e.CatalogRead()
	ref := roleTableRef(t, e, "d")
	old := view.MustRelation(ref.Address)
	if err := (relationProvider{}).Validate(view, old); err != nil {
		t.Fatal("validation must not evaluate division", err)
	}
	foreign := New()
	foreignType, _ := foreign.CatalogRead().LookupType(sqltype.Int4)
	foreignValue, err := sqltype.Integer(foreignType, 1)
	if err != nil {
		t.Fatal(err)
	}
	foreignChild := literalExpression{Info: expr.Info{Usage: foreignType.Usage()}, value: foreignValue}
	for _, tc := range []struct {
		name      string
		attribute int32
		mutate    func(arithmeticExpression) arithmeticExpression
	}{
		{"missing-left", 1, func(n arithmeticExpression) arithmeticExpression { n.left = nil; return n }},
		{"missing-right", 1, func(n arithmeticExpression) arithmeticExpression { n.right = nil; return n }},
		{"invalid-operator", 1, func(n arithmeticExpression) arithmeticExpression {
			n.operator = sqltype.ArithmeticOperator(255)
			return n
		}},
		{"operator-provider-mismatch", 1, func(n arithmeticExpression) arithmeticExpression { n.operator = sqltype.ArithmeticAdd; return n }},
		{"empty-provider", 1, func(n arithmeticExpression) arithmeticExpression { n.provider = sqltype.Arithmetic{}; return n }},
		{"foreign-child", 1, func(n arithmeticExpression) arithmeticExpression { n.left = foreignChild; return n }},
		{"wrong-child-width", 1, func(n arithmeticExpression) arithmeticExpression { n.left = old.Defaults[2].Expression; return n }},
		{"dependent-child", 1, func(n arithmeticExpression) arithmeticExpression {
			child := n.left.(literalExpression)
			child.Property.RowDependent = true
			n.left = child
			return n
		}},
		{"invalid-unary-operator", 2, func(n arithmeticExpression) arithmeticExpression { n.operator = sqltype.ArithmeticMultiply; return n }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := *old
			next.Defaults = make(map[int32]columnDefault, len(old.Defaults))
			for attribute, definition := range old.Defaults {
				next.Defaults[attribute] = definition
			}
			bad := next.Defaults[tc.attribute]
			bad.Expression = tc.mutate(bad.Expression.(arithmeticExpression))
			next.Defaults[tc.attribute] = bad
			if err := (relationProvider{}).Validate(view, &next); err == nil {
				t.Fatal("invalid arithmetic accepted")
			}
		})
	}
}

func TestDefaultArithmeticPlanningScopedPolicy(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE d(i INT DEFAULT 1/0)")
	view := e.CatalogRead()
	ref := roleTableRef(t, e, "d")
	node := view.MustRelation(ref.Address).Defaults[1].Expression
	ctx := &EvalContext{Context: context.Background()}
	ordinary, changed, err := preflightCoercions(ctx, node)
	if err != nil || changed {
		t.Fatal("ordinary arithmetic must remain row-owned", changed, err)
	}
	if _, ok := ordinary.(arithmeticExpression); !ok {
		t.Fatal("ordinary arithmetic was folded")
	}
	_, _, err = preflightCoercionsWithPolicy(ctx, node, true)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" || diagnostic.Message != "division by zero" {
		t.Fatal(err)
	}
	if _, ok := view.MustRelation(ref.Address).Defaults[1].Expression.(arithmeticExpression); !ok {
		t.Fatal("catalog expression changed during execution planning")
	}
}

func TestDefaultSmallintArithmeticWidths(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE d(s SMALLINT DEFAULT (7::smallint/2::smallint),b BIGINT DEFAULT (7::smallint+2147483648::bigint),u SMALLINT DEFAULT (32767::smallint+1::smallint))")
	view := e.CatalogRead()
	ref := roleTableRef(t, e, "d")
	desc := view.MustRelation(ref.Address)
	if err := (relationProvider{}).Validate(view, desc); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		attribute           int32
		left, right, result sqltype.ID
	}{{1, sqltype.Int2, sqltype.Int2, sqltype.Int2}, {2, sqltype.Int2, sqltype.Int8, sqltype.Int8}, {3, sqltype.Int2, sqltype.Int2, sqltype.Int2}} {
		n, ok := desc.Defaults[tc.attribute].Expression.(arithmeticExpression)
		if !ok || n.left.ResultUsage().Type.ID() != tc.left || n.right.ResultUsage().Type.ID() != tc.right || n.provider.ResultType().ID() != tc.result {
			t.Fatal(tc, n)
		}
	}
	// Validation above must not consume the unused overflowing default.
	run(t, e, "INSERT INTO d(u) VALUES(7)")
	rows := run(t, e, "SELECT s,b,u FROM d")
	defer rows.Rows.Close()
	got := tuples(rows)
	if len(got) != 1 || got[0][0] != int64(3) || got[0][1] != int64(2147483655) || got[0][2] != int64(7) {
		t.Fatal(got)
	}
}
