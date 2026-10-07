package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestDefaultDescriptorProviderClosedArithmetic(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE d(i INT DEFAULT 1/0,u BIGINT DEFAULT -(CAST(7 AS BIGINT)))")
	view := e.CatalogRead()
	ref := roleTableRef(t, e, "d")
	old := view.state.descriptors[ref.Address].(*relationDescriptor)
	if err := (relationProvider{}).Validate(view, old); err != nil {
		t.Fatal("validation must not evaluate division", err)
	}
	foreign := New()
	foreignType, _ := foreign.CatalogRead().LookupType(sqltype.Int4)
	foreignValue, err := sqltype.Integer(foreignType, 1)
	if err != nil {
		t.Fatal(err)
	}
	foreignChild := literalExpression{expressionInfo: expressionInfo{usage: foreignType.Usage()}, value: foreignValue}
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
		{"wrong-child-width", 1, func(n arithmeticExpression) arithmeticExpression { n.left = old.defaults[2].expression; return n }},
		{"dependent-child", 1, func(n arithmeticExpression) arithmeticExpression {
			child := n.left.(literalExpression)
			child.properties.RowDependent = true
			n.left = child
			return n
		}},
		{"invalid-unary-operator", 2, func(n arithmeticExpression) arithmeticExpression { n.operator = sqltype.ArithmeticMultiply; return n }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := *old
			next.defaults = make(map[int32]columnDefault, len(old.defaults))
			for attribute, definition := range old.defaults {
				next.defaults[attribute] = definition
			}
			bad := next.defaults[tc.attribute]
			bad.expression = tc.mutate(bad.expression.(arithmeticExpression))
			next.defaults[tc.attribute] = bad
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
	node := view.state.descriptors[ref.Address].(*relationDescriptor).defaults[1].expression
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
	if _, ok := view.state.descriptors[ref.Address].(*relationDescriptor).defaults[1].expression.(arithmeticExpression); !ok {
		t.Fatal("catalog expression changed during execution planning")
	}
}
