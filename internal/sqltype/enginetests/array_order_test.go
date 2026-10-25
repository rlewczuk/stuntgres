package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestArrayOrderOwnerRebinding(t *testing.T) {
	e := engine.New()
	ctx := context.Background()
	b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	array, boolean := b.Type(1007), b.Type(sqltype.Bool)
	value, err := array.Input(ctx, "{1,NULL}", -1)
	if err != nil {
		t.Fatal(err)
	}
	operand := plan.LiteralExpr{Info: b.Info(array.Usage(), parser.Position{}, expr.Properties{}), Value: value}
	predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, array, array, boolean, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	original := plan.ComparisonExpr{Info: b.Info(boolean.Usage(), parser.Position{}, expr.Properties{}), Left: operand, Right: operand, Predicate: predicate, Operator: "="}
	target := engine.New()
	fresh := map[sqltype.ID]*sqltype.Descriptor{
		1007:         eType(t, target, 1007),
		sqltype.Int4: eType(t, target, sqltype.Int4),
		sqltype.Bool: eType(t, target, sqltype.Bool),
	}
	mapped, changed, err := stored.RetagTypes(e.CatalogRead(), original, func(d *sqltype.Descriptor) *sqltype.Descriptor {
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
		owner *engine.Engine
		node  plan.ComparisonExpr
	}{{e, original}, {target, rebound}} {
		node := entry.node
		result, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
		if err != nil {
			t.Fatal(err)
		}
		truth, err := plan.TruthOf(result)
		if err != nil || truth != plan.True {
			t.Fatal(truth, err)
		}
		if _, err := stored.DefaultSQL(ctx, entry.owner.CatalogRead(), node); err != nil {
			t.Fatal("independently rebound stored validation", err)
		}
	}
	if original.Left.ResultUsage().Type != array {
		t.Fatal("remap mutated source")
	}
}
