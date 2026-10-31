package core_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestDefaultDescriptorProviderClosedPredicates(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, `CREATE TABLE d(n BOOL DEFAULT (NULL IS NULL),t BOOL DEFAULT (TRUE IS TRUE),v BOOL DEFAULT (1 IS DISTINCT FROM 2),i INT DEFAULT 1)`)
	view := e.CatalogRead()
	ref := enginetest.RoleTableRef(t, e, "d")
	old := view.MustRelation(ref.Address)
	if err := (core.RelationProvider).Validate(view, old); err != nil {
		t.Fatal(err)
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
		mutate    func(expr.Expr) expr.Expr
	}{
		{"null-child", 1, func(node expr.Expr) expr.Expr { n := node.(plan.NullTestExpr); n.Child = nil; return n }},
		{"null-result-type", 1, func(node expr.Expr) expr.Expr {
			n := node.(plan.NullTestExpr)
			n.Usage = old.Columns[3].Usage
			return n
		}},
		{"null-foreign-child", 1, func(node expr.Expr) expr.Expr { n := node.(plan.NullTestExpr); n.Child = foreignChild; return n }},
		{"null-dependent-child", 1, func(node expr.Expr) expr.Expr {
			n := node.(plan.NullTestExpr)
			child := old.ColumnMeta.Defaults[4].Expression.(plan.LiteralExpr)
			child.Property.RowDependent = true
			n.Child = child
			return n
		}},
		{"truth-child", 2, func(node expr.Expr) expr.Expr { n := node.(plan.TruthTestExpr); n.Child = nil; return n }},
		{"truth-child-type", 2, func(node expr.Expr) expr.Expr {
			n := node.(plan.TruthTestExpr)
			n.Child = old.ColumnMeta.Defaults[4].Expression
			return n
		}},
		{"truth-enum", 2, func(node expr.Expr) expr.Expr { n := node.(plan.TruthTestExpr); n.Truth = plan.Truth(255); return n }},
		{"distinct-child", 3, func(node expr.Expr) expr.Expr { n := node.(plan.DistinctExpr); n.Left = nil; return n }},
		{"distinct-plan", 3, func(node expr.Expr) expr.Expr {
			n := node.(plan.DistinctExpr)
			n.Equality = sqltype.ComparisonPredicate{}
			return n
		}},
		{"distinct-operand", 3, func(node expr.Expr) expr.Expr {
			n := node.(plan.DistinctExpr)
			n.Right = old.ColumnMeta.Defaults[2].Expression
			return n
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := *old
			next.ColumnMeta.Defaults = make(map[int32]catalog.ColumnDefault, len(old.ColumnMeta.Defaults))
			for attribute, definition := range old.ColumnMeta.Defaults {
				next.ColumnMeta.Defaults[attribute] = definition
			}
			bad := next.ColumnMeta.Defaults[tc.attribute]
			bad.Expression = tc.mutate(bad.Expression)
			next.ColumnMeta.Defaults[tc.attribute] = bad
			if err := (core.RelationProvider).Validate(view, &next); err == nil {
				t.Fatal("invalid predicate definition accepted", tc.name)
			}
		})
	}
}

func TestDefaultDescriptorProviderUnfoldedDecisiveBranches(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, `CREATE TABLE d(t BOOL DEFAULT (TRUE OR CAST('oops'::text AS INT)=1),f BOOL DEFAULT (FALSE AND CAST('oops'::text AS INT)=1))`)
	view := e.CatalogRead()
	ref := enginetest.RoleTableRef(t, e, "d")
	d := view.MustRelation(ref.Address)
	if err := (core.RelationProvider).Validate(view, d); err != nil {
		t.Fatal("provider evaluated a discarded conversion", err)
	}
	for _, definition := range d.ColumnMeta.Defaults {
		text, err := stored.DefaultSQL(context.Background(), view, definition.Expression)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := definition.Expression.(plan.LogicalExpr); !ok ||
			!strings.Contains(text, "'oops'") || !strings.Contains(text, "CAST(") {
			t.Fatal("stored default was simplified", definition)
		}
	}
}
