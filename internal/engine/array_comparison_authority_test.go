package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestArrayCompareCollationAndOwners(t *testing.T) {
	e := New()
	owner := sessionStart(t, e, nil)
	for _, q := range []string{
		`CREATE COLLATION ac_source FROM pg_catalog."C"`,
		`CREATE COLLATION ac_destination FROM pg_catalog."C"`,
		`CREATE TABLE ac_authority(a BOOL DEFAULT ('a' COLLATE ac_source IN('a','b')),b BOOL DEFAULT ('a' COLLATE ac_source=ANY(ARRAY['a','b'])))`,
	} {
		sessionSQL(t, owner, q)
	}
	view := e.CatalogRead()
	var source, destination *collationDescriptor
	for _, descriptor := range view.state.descriptors {
		if d, ok := descriptor.(*collationDescriptor); ok {
			if d.name == "ac_source" {
				source = d
			}
			if d.name == "ac_destination" {
				destination = d
			}
		}
	}
	if source == nil || destination == nil {
		t.Fatal("missing collation fixture")
	}
	remap := remappedCollationCapabilities(view.state, map[ids.ObjectAddress]ids.ObjectAddress{source.Address(): destination.Address()})
	ref := catalogRef(t, e, view, "ac_authority")
	table := view.state.descriptors[ref.Address].(*relationDescriptor)
	for _, attribute := range table.attributes {
		original := table.defaults[attribute].expression.(scalarArrayComparisonExpression)
		input, conflict := original.predicate.InputCollation()
		if input.ID != uint32(source.oid) || conflict || original.ResultUsage().Collation.ID != 0 {
			t.Fatal("BOOL result leaked or hid input authority", input, conflict, original.ResultUsage())
		}
		mapped, changed, err := retagBoundTypes(view, original, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, remap)
		if err != nil || !changed {
			t.Fatal(mapped, changed, err)
		}
		node := mapped.(scalarArrayComparisonExpression)
		input, conflict = node.predicate.InputCollation()
		if input.ID != uint32(destination.oid) || !input.Valid() || conflict {
			t.Fatal("predicate retained stale collation", input, conflict)
		}
		value, err := node.Eval(&EvalContext{Context: context.Background()})
		truth, truthErr := TruthOf(value)
		if err != nil || truthErr != nil || truth != True {
			t.Fatal(truth, err, truthErr)
		}
		if _, err := defaultSQL(context.Background(), view, node); err != nil {
			t.Fatal("remapped canonical renderer rejected actual predicate", err)
		}
	}
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	oid, boolean := b.typ(sqltype.Oid), b.typ(sqltype.Bool)
	left := literalExpression{expressionInfo: b.info(oid.Usage(), parser.Position{}, ExprProperties{}), value: sqltype.Null(oid)}
	late := tableOIDExpression{expressionInfo: b.info(oid.Usage(), parser.Position{}, ExprProperties{RowDependent: true})}
	predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, oid, oid, boolean, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range []scalarArrayComparisonExpression{
		{expressionInfo: b.info(boolean.Usage(), parser.Position{}, ExprProperties{RowDependent: true}), left: left, items: []BoundExpr{left, late}, predicate: predicate, operator: "="},
		{expressionInfo: b.info(boolean.Usage(), parser.Position{}, ExprProperties{RowDependent: true}), left: left, array: arrayExpression{expressionInfo: b.info(b.typ(oid.AssociatedArray()).Usage(), parser.Position{}, ExprProperties{RowDependent: true}), elements: []BoundExpr{left, late}}, predicate: predicate, operator: "="},
	} {
		if !generationHasTableOID(node) || !generationImmutable(node) {
			t.Fatal("late RHS owner-dependent generation lost", node)
		}
		seen := false
		walkBoundExpression(node, func(child BoundExpr) {
			if _, ok := child.(tableOIDExpression); ok {
				seen = true
			}
		})
		if !seen {
			t.Fatal("lexical walk dropped late RHS owner")
		}
	}
}
