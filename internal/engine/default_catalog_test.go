package engine

import (
	"strings"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestDefaultDescriptorProviderClosedDefinitions(t *testing.T) {
	e := New()
	run(t, e, `CREATE TABLE d(i INT DEFAULT 2147483648,b BOOL DEFAULT (1 < 2))`)
	view := e.CatalogRead()
	ref := roleTableRef(t, e, "d")
	old := view.state.descriptors[ref.Address].(*relationDescriptor)
	if err := (relationProvider{}).Validate(view, old); err != nil {
		t.Fatal("validation evaluated narrowing", err)
	}
	for _, mutate := range []func(*relationDescriptor){
		func(d *relationDescriptor) { d.defaults = map[int32]columnDefault{99: old.defaults[1]} },
		func(d *relationDescriptor) { d.defaults = map[int32]columnDefault{1: old.defaults[2]} },
		func(d *relationDescriptor) {
			bad := old.defaults[1]
			bad.sql = "0"
			d.defaults = map[int32]columnDefault{1: bad}
		},
		func(d *relationDescriptor) {
			u := old.columns[0].Usage
			node := parameterExpression{expressionInfo: expressionInfo{usage: u, properties: ExprProperties{ParameterDependent: true}}, number: 1}
			d.defaults = map[int32]columnDefault{1: {expression: node, sql: "0"}}
		},
		func(d *relationDescriptor) {
			u := old.columns[0].Usage
			node := literalExpression{expressionInfo: expressionInfo{usage: u}, value: sqltype.Null(old.columns[1].Usage.Type)}
			d.defaults = map[int32]columnDefault{1: {expression: node, sql: "NULL"}}
		},
	} {
		next := *old
		mutate(&next)
		if err := (relationProvider{}).Validate(view, &next); err == nil {
			t.Fatal("invalid definition accepted", next.defaults)
		}
	}
}

func TestDefaultDescriptorProviderClosedPredicates(t *testing.T) {
	e := New()
	run(t, e, `CREATE TABLE d(n BOOL DEFAULT (NULL IS NULL),t BOOL DEFAULT (TRUE IS TRUE),v BOOL DEFAULT (1 IS DISTINCT FROM 2),i INT DEFAULT 1)`)
	view := e.CatalogRead()
	ref := roleTableRef(t, e, "d")
	old := view.state.descriptors[ref.Address].(*relationDescriptor)
	if err := (relationProvider{}).Validate(view, old); err != nil {
		t.Fatal(err)
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
		mutate    func(BoundExpr) BoundExpr
	}{
		{"null-child", 1, func(node BoundExpr) BoundExpr { n := node.(nullTestExpression); n.child = nil; return n }},
		{"null-canonical", 1, func(node BoundExpr) BoundExpr { n := node.(nullTestExpression); n.not = !n.not; return n }},
		{"null-result-type", 1, func(node BoundExpr) BoundExpr {
			n := node.(nullTestExpression)
			n.usage = old.columns[3].Usage
			return n
		}},
		{"null-foreign-child", 1, func(node BoundExpr) BoundExpr { n := node.(nullTestExpression); n.child = foreignChild; return n }},
		{"null-dependent-child", 1, func(node BoundExpr) BoundExpr {
			n := node.(nullTestExpression)
			child := old.defaults[4].expression.(literalExpression)
			child.properties.RowDependent = true
			n.child = child
			return n
		}},
		{"truth-child", 2, func(node BoundExpr) BoundExpr { n := node.(truthTestExpression); n.child = nil; return n }},
		{"truth-child-type", 2, func(node BoundExpr) BoundExpr {
			n := node.(truthTestExpression)
			n.child = old.defaults[4].expression
			return n
		}},
		{"truth-enum", 2, func(node BoundExpr) BoundExpr { n := node.(truthTestExpression); n.truth = Truth(255); return n }},
		{"distinct-child", 3, func(node BoundExpr) BoundExpr { n := node.(distinctExpression); n.left = nil; return n }},
		{"distinct-plan", 3, func(node BoundExpr) BoundExpr {
			n := node.(distinctExpression)
			n.equality = sqltype.ComparisonPredicate{}
			return n
		}},
		{"distinct-operand", 3, func(node BoundExpr) BoundExpr {
			n := node.(distinctExpression)
			n.right = old.defaults[2].expression
			return n
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := *old
			next.defaults = make(map[int32]columnDefault, len(old.defaults))
			for attribute, definition := range old.defaults {
				next.defaults[attribute] = definition
			}
			bad := next.defaults[tc.attribute]
			bad.expression = tc.mutate(bad.expression)
			next.defaults[tc.attribute] = bad
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
	old := view.state.descriptors[ref.Address].(*relationDescriptor)
	if err := (relationProvider{}).Validate(view, old); err != nil {
		t.Fatal("definition validation evaluated an explicit cast", err)
	}
	if !strings.Contains(old.defaults[2].sql, "CAST(CAST('oops' AS TEXT) AS ") {
		t.Fatal("lost known TEXT input type", old.defaults[2].sql)
	}
	for _, mutate := range []func(coercionExpression) coercionExpression{
		func(n coercionExpression) coercionExpression { n.mode = sqltype.CoercionContext(255); return n },
		func(n coercionExpression) coercionExpression { n.mode = sqltype.Implicit; return n },
		func(n coercionExpression) coercionExpression { n.mode = sqltype.Assignment; return n },
		func(n coercionExpression) coercionExpression { n.plan = sqltype.CoercionPlan{}; return n },
		func(n coercionExpression) coercionExpression { n.child = nil; return n },
	} {
		next := *old
		bad := old.defaults[1]
		bad.expression = mutate(bad.expression.(coercionExpression))
		next.defaults = map[int32]columnDefault{1: bad, 2: old.defaults[2]}
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
	d := view.state.descriptors[ref.Address].(*relationDescriptor)
	if err := (relationProvider{}).Validate(view, d); err != nil {
		t.Fatal("provider evaluated a discarded conversion", err)
	}
	for _, definition := range d.defaults {
		if _, ok := definition.expression.(logicalExpression); !ok ||
			!strings.Contains(definition.sql, "'oops'") || !strings.Contains(definition.sql, "CAST(") {
			t.Fatal("stored default was simplified", definition)
		}
	}
}
