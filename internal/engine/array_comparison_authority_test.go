package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
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
	var source, destination *catalog.Collation
	for _, descriptor := range view.Descriptors() {
		if d, ok := descriptor.(*catalog.Collation); ok {
			if d.Name() == "ac_source" {
				source = d
			}
			if d.Name() == "ac_destination" {
				destination = d
			}
		}
	}
	if source == nil || destination == nil {
		t.Fatal("missing collation fixture")
	}
	remap := remappedCollationCapabilities(view.State(), map[ids.ObjectAddress]ids.ObjectAddress{source.Address(): destination.Address()})
	ref := catalogRef(t, e, view, "ac_authority")
	table := view.MustRelation(ref.Address)
	for _, attribute := range table.Attributes {
		original := table.ColumnMeta.Defaults[attribute].Expression.(scalarArrayComparisonExpression)
		input, conflict := original.predicate.InputCollation()
		if input.ID != uint32(source.OID) || conflict || original.ResultUsage().Collation.ID != 0 {
			t.Fatal("BOOL result leaked or hid input authority", input, conflict, original.ResultUsage())
		}
		mapped, changed, err := retagBoundTypes(view, original, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, remap)
		if err != nil || !changed {
			t.Fatal(mapped, changed, err)
		}
		node := mapped.(scalarArrayComparisonExpression)
		input, conflict = node.predicate.InputCollation()
		if input.ID != uint32(destination.OID) || !input.Valid() || conflict {
			t.Fatal("predicate retained stale collation", input, conflict)
		}
		value, err := node.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}})
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
	left := literalExpression{Info: b.info(oid.Usage(), parser.Position{}, expr.Properties{}), value: sqltype.Null(oid)}
	late := tableOIDExpression{Info: b.info(oid.Usage(), parser.Position{}, expr.Properties{RowDependent: true})}
	predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, oid, oid, boolean, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range []scalarArrayComparisonExpression{
		{Info: b.info(boolean.Usage(), parser.Position{}, expr.Properties{RowDependent: true}), left: left, items: []expr.Expr{left, late}, predicate: predicate, operator: "="},
		{Info: b.info(boolean.Usage(), parser.Position{}, expr.Properties{RowDependent: true}), left: left, array: arrayExpression{Info: b.info(b.typ(oid.AssociatedArray()).Usage(), parser.Position{}, expr.Properties{RowDependent: true}), elements: []expr.Expr{left, late}}, predicate: predicate, operator: "="},
	} {
		if !generationHasTableOID(node) || !generationImmutable(node) {
			t.Fatal("late RHS owner-dependent generation lost", node)
		}
		seen := false
		walkBoundExpression(node, func(child expr.Expr) {
			if _, ok := child.(tableOIDExpression); ok {
				seen = true
			}
		})
		if !seen {
			t.Fatal("lexical walk dropped late RHS owner")
		}
	}
}
