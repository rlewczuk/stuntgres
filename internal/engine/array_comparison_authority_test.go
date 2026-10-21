package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/typedef"
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
	remap := typedef.RemappedCollationCapabilities(view.State(), map[ids.ObjectAddress]ids.ObjectAddress{source.Address(): destination.Address()})
	ref := catalogRef(t, e, view, "ac_authority")
	table := view.MustRelation(ref.Address)
	for _, attribute := range table.Attributes {
		original := table.ColumnMeta.Defaults[attribute].Expression.(plan.ScalarArrayComparisonExpr)
		input, conflict := original.Predicate.InputCollation()
		if input.ID != uint32(source.OID) || conflict || original.ResultUsage().Collation.ID != 0 {
			t.Fatal("BOOL result leaked or hid input authority", input, conflict, original.ResultUsage())
		}
		mapped, changed, err := RetagTypes(view, original, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, remap)
		if err != nil || !changed {
			t.Fatal(mapped, changed, err)
		}
		node := mapped.(plan.ScalarArrayComparisonExpr)
		input, conflict = node.Predicate.InputCollation()
		if input.ID != uint32(destination.OID) || !input.Valid() || conflict {
			t.Fatal("predicate retained stale collation", input, conflict)
		}
		value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
		truth, truthErr := plan.TruthOf(value)
		if err != nil || truthErr != nil || truth != plan.True {
			t.Fatal(truth, err, truthErr)
		}
		if _, err := DefaultSQL(context.Background(), view, node); err != nil {
			t.Fatal("remapped canonical renderer rejected actual predicate", err)
		}
	}
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	oid, boolean := b.Type(sqltype.Oid), b.Type(sqltype.Bool)
	left := plan.LiteralExpr{Info: b.Info(oid.Usage(), parser.Position{}, expr.Properties{}), Value: sqltype.Null(oid)}
	late := plan.TableOIDExpr{Info: b.Info(oid.Usage(), parser.Position{}, expr.Properties{RowDependent: true})}
	predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, oid, oid, boolean, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range []plan.ScalarArrayComparisonExpr{
		{Info: b.Info(boolean.Usage(), parser.Position{}, expr.Properties{RowDependent: true}), Left: left, Items: []expr.Expr{left, late}, Predicate: predicate, Operator: "="},
		{Info: b.Info(boolean.Usage(), parser.Position{}, expr.Properties{RowDependent: true}), Left: left, Array: plan.ArrayExpr{Info: b.Info(b.Type(oid.AssociatedArray()).Usage(), parser.Position{}, expr.Properties{RowDependent: true}), Elements: []expr.Expr{left, late}}, Predicate: predicate, Operator: "="},
	} {
		if !plan.GenerationHasTableOID(node) || !plan.GenerationImmutable(node) {
			t.Fatal("late RHS owner-dependent generation lost", node)
		}
		seen := false
		plan.WalkExpression(node, func(child expr.Expr) {
			if _, ok := child.(plan.TableOIDExpr); ok {
				seen = true
			}
		})
		if !seen {
			t.Fatal("lexical walk dropped late RHS owner")
		}
	}
}
