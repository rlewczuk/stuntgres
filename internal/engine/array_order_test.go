package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestArrayOrderOwnerRebinding(t *testing.T) {
	e := New()
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	array, boolean := b.typ(1007), b.typ(sqltype.Bool)
	value, err := array.Input(ctx, "{1,NULL}", -1)
	if err != nil {
		t.Fatal(err)
	}
	operand := plan.LiteralExpr{Info: b.info(array.Usage(), parser.Position{}, expr.Properties{}), Value: value}
	predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, array, array, boolean, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	original := plan.ComparisonExpr{Info: b.info(boolean.Usage(), parser.Position{}, expr.Properties{}), Left: operand, Right: operand, Predicate: predicate, Operator: "="}
	target := New()
	fresh := map[sqltype.ID]*sqltype.Descriptor{
		1007:         eType(t, target, 1007),
		sqltype.Int4: eType(t, target, sqltype.Int4),
		sqltype.Bool: eType(t, target, sqltype.Bool),
	}
	mapped, changed, err := retagBoundTypes(e.CatalogRead(), original, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if next := fresh[d.ID()]; next != nil {
			return next
		}
		return d
	}, nil)
	if err != nil || !changed {
		t.Fatal("comparison remap", changed, err)
	}
	rebound := mapped.(plan.ComparisonExpr)
	if rebound.ResultUsage().Type != fresh[sqltype.Bool] || rebound.Left.ResultUsage().Type != fresh[1007] || predicate.Equal(rebound.Predicate) {
		t.Fatal("comparison retained old authority")
	}
	for _, entry := range []struct {
		owner *Engine
		node  plan.ComparisonExpr
	}{{e, original}, {target, rebound}} {
		node := entry.node
		result, err := node.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}})
		if err != nil {
			t.Fatal(err)
		}
		truth, err := plan.TruthOf(result)
		if err != nil || truth != plan.True {
			t.Fatal(truth, err)
		}
		if _, err := defaultSQL(ctx, entry.owner.CatalogRead(), node); err != nil {
			t.Fatal("independently rebound stored validation", err)
		}
	}
	if original.Left.ResultUsage().Type != array {
		t.Fatal("remap mutated source")
	}
}
