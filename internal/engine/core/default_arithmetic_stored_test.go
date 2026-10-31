package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestDefaultDescriptorProviderClosedArithmetic(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE d(i INT DEFAULT 1/0,u BIGINT DEFAULT -(CAST(7 AS BIGINT)))")
	view := e.CatalogRead()
	ref := enginetest.RoleTableRef(t, e, "d")
	old := view.MustRelation(ref.Address)
	if err := (core.RelationProvider).Validate(view, old); err != nil {
		t.Fatal("validation must not evaluate division", err)
	}
	foreign := engine.New()
	foreignType, _ := foreign.CatalogRead().LookupType(sqltype.Int4)
	foreignValue, err := sqltype.Integer(foreignType, 1)
	if err != nil {
		t.Fatal(err)
	}
	foreignChild := plan.LiteralExpr{Info: expr.Info{Usage: foreignType.Usage()}, Value: foreignValue}
	for _, tc := range []struct {
		name      string
		attribute int32
		mutate    func(plan.ArithmeticExpr) plan.ArithmeticExpr
	}{
		{"missing-left", 1, func(n plan.ArithmeticExpr) plan.ArithmeticExpr { n.Left = nil; return n }},
		{"missing-right", 1, func(n plan.ArithmeticExpr) plan.ArithmeticExpr { n.Right = nil; return n }},
		{"invalid-operator", 1, func(n plan.ArithmeticExpr) plan.ArithmeticExpr {
			n.Operator = sqltype.ArithmeticOperator(255)
			return n
		}},
		{"operator-provider-mismatch", 1, func(n plan.ArithmeticExpr) plan.ArithmeticExpr { n.Operator = sqltype.ArithmeticAdd; return n }},
		{"empty-provider", 1, func(n plan.ArithmeticExpr) plan.ArithmeticExpr { n.Provider = sqltype.Arithmetic{}; return n }},
		{"foreign-child", 1, func(n plan.ArithmeticExpr) plan.ArithmeticExpr { n.Left = foreignChild; return n }},
		{"wrong-child-width", 1, func(n plan.ArithmeticExpr) plan.ArithmeticExpr {
			n.Left = old.ColumnMeta.Defaults[2].Expression
			return n
		}},
		{"dependent-child", 1, func(n plan.ArithmeticExpr) plan.ArithmeticExpr {
			child := n.Left.(plan.LiteralExpr)
			child.Property.RowDependent = true
			n.Left = child
			return n
		}},
		{"invalid-unary-operator", 2, func(n plan.ArithmeticExpr) plan.ArithmeticExpr { n.Operator = sqltype.ArithmeticMultiply; return n }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := *old
			next.ColumnMeta.Defaults = make(map[int32]catalog.ColumnDefault, len(old.ColumnMeta.Defaults))
			for attribute, definition := range old.ColumnMeta.Defaults {
				next.ColumnMeta.Defaults[attribute] = definition
			}
			bad := next.ColumnMeta.Defaults[tc.attribute]
			bad.Expression = tc.mutate(bad.Expression.(plan.ArithmeticExpr))
			next.ColumnMeta.Defaults[tc.attribute] = bad
			if err := (core.RelationProvider).Validate(view, &next); err == nil {
				t.Fatal("invalid arithmetic accepted")
			}
		})
	}
}
