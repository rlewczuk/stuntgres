package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestDefaultDescriptorProviderClosedDefinitions(t *testing.T) {
	e := New()
	run(t, e, `CREATE TABLE d(i INT DEFAULT 2147483648,b BOOL DEFAULT (1 < 2))`)
	view := e.CatalogRead()
	ref := roleTableRef(t, e, "d")
	old := view.MustRelation(ref.Address)
	if err := (relationProvider{}).Validate(view, old); err != nil {
		t.Fatal("validation evaluated narrowing", err)
	}
	names := []string{"missing-attribute", "wrong-destination", "missing-dependencies", "parameter-dependent-valid-oid", "nonnull-literal-type-mismatch-valid-oid", "missing-oid"}
	for i, mutate := range []func(*catalog.Relation){
		func(d *catalog.Relation) {
			d.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{99: old.ColumnMeta.Defaults[1]}
		},
		func(d *catalog.Relation) {
			d.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: old.ColumnMeta.Defaults[2]}
		},
		func(d *catalog.Relation) {
			bad := old.ColumnMeta.Defaults[1]
			bad.Dependencies = nil
			d.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: bad}
		},
		func(d *catalog.Relation) {
			u := old.Columns[0].Usage
			node := plan.ParameterExpr{Info: expr.Info{Usage: u, Property: expr.Properties{ParameterDependent: true}}, Number: 1}
			bad := old.ColumnMeta.Defaults[1]
			bad.Expression = node
			d.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: bad}
			if _, err := (scalarRenderer{}).sql(context.Background(), view, node); err == nil || err.Error() != "stuntgres: invalid stored default properties" {
				t.Fatal("parameter invariant not reached", err)
			}
		},
		func(d *catalog.Relation) {
			u := old.Columns[0].Usage
			node := plan.LiteralExpr{Info: expr.Info{Usage: u}, Value: sqltype.Boolean(old.Columns[1].Usage.Type, true)}
			bad := old.ColumnMeta.Defaults[1]
			bad.Expression = node
			d.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: bad}
			if _, err := (scalarRenderer{}).sql(context.Background(), view, node); err == nil || err.Error() != "stuntgres: invalid stored literal" {
				t.Fatal("literal type invariant not reached", err)
			}
		},
		func(d *catalog.Relation) {
			bad := old.ColumnMeta.Defaults[1]
			bad.OID = 0
			d.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: bad}
			if err := (relationProvider{}).Validate(view, d); err == nil || err.Error() != "stuntgres: invalid column default destination" {
				t.Fatal("missing identity invariant not reached", err)
			}
		},
	} {
		t.Run(names[i], func(t *testing.T) {
			next := *old
			mutate(&next)
			want := "stuntgres: invalid column default expression"
			if i < 2 || i == 5 {
				want = "stuntgres: invalid column default destination"
			}
			if err := (relationProvider{}).Validate(view, &next); err == nil || err.Error() != want {
				t.Fatal("wrong invariant failure", next.ColumnMeta.Defaults, err, want)
			}
		})
	}
}

func TestDefaultDescriptorProviderClosedPredicates(t *testing.T) {
	e := New()
	run(t, e, `CREATE TABLE d(n BOOL DEFAULT (NULL IS NULL),t BOOL DEFAULT (TRUE IS TRUE),v BOOL DEFAULT (1 IS DISTINCT FROM 2),i INT DEFAULT 1)`)
	view := e.CatalogRead()
	ref := roleTableRef(t, e, "d")
	old := view.MustRelation(ref.Address)
	if err := (relationProvider{}).Validate(view, old); err != nil {
		t.Fatal(err)
	}
	foreign := New()
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
			if err := (relationProvider{}).Validate(view, &next); err == nil {
				t.Fatal("invalid predicate definition accepted", tc.name)
			}
		})
	}
}

func TestDefaultDescriptorProviderClosedCasts(t *testing.T) {
	e := New()
	run(t, e, `CREATE TABLE d(t TEXT DEFAULT CAST(1 AS TEXT),i INT DEFAULT CAST('oops'::text AS INT))`)
	view := e.CatalogRead()
	ref := roleTableRef(t, e, "d")
	old := view.MustRelation(ref.Address)
	if err := (relationProvider{}).Validate(view, old); err != nil {
		t.Fatal("definition validation evaluated an explicit cast", err)
	}
	text, err := defaultSQL(context.Background(), view, old.ColumnMeta.Defaults[2].Expression)
	if err != nil || !strings.Contains(text, "CAST(CAST('oops' AS TEXT) AS ") {
		t.Fatal("lost known TEXT input type", text, err)
	}
	for _, mutate := range []func(plan.CoercionExpr) plan.CoercionExpr{
		func(n plan.CoercionExpr) plan.CoercionExpr { n.Mode = sqltype.CoercionContext(255); return n },
		func(n plan.CoercionExpr) plan.CoercionExpr { n.Mode = sqltype.Implicit; return n },
		func(n plan.CoercionExpr) plan.CoercionExpr { n.Mode = sqltype.Assignment; return n },
		func(n plan.CoercionExpr) plan.CoercionExpr { n.Plan = sqltype.CoercionPlan{}; return n },
		func(n plan.CoercionExpr) plan.CoercionExpr { n.Child = nil; return n },
	} {
		next := *old
		bad := old.ColumnMeta.Defaults[1]
		bad.Expression = mutate(bad.Expression.(plan.CoercionExpr))
		next.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: bad, 2: old.ColumnMeta.Defaults[2]}
		if err := (relationProvider{}).Validate(view, &next); err == nil {
			t.Fatal("invalid cast definition accepted", bad)
		}
	}
}

func TestDefaultDescriptorProviderUnfoldedDecisiveBranches(t *testing.T) {
	e := New()
	run(t, e, `CREATE TABLE d(t BOOL DEFAULT (TRUE OR CAST('oops'::text AS INT)=1),f BOOL DEFAULT (FALSE AND CAST('oops'::text AS INT)=1))`)
	view := e.CatalogRead()
	ref := roleTableRef(t, e, "d")
	d := view.MustRelation(ref.Address)
	if err := (relationProvider{}).Validate(view, d); err != nil {
		t.Fatal("provider evaluated a discarded conversion", err)
	}
	for _, definition := range d.ColumnMeta.Defaults {
		text, err := defaultSQL(context.Background(), view, definition.Expression)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := definition.Expression.(plan.LogicalExpr); !ok ||
			!strings.Contains(text, "'oops'") || !strings.Contains(text, "CAST(") {
			t.Fatal("stored default was simplified", definition)
		}
	}
}
