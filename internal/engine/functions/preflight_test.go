package functions

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestPreflightTemplateNeedsLiteral(t *testing.T) {
	never := func(expr.Expr) (sqltype.Value, bool) { return sqltype.Value{}, false }
	for _, node := range []expr.ConstantTemplate{DatetimeFormatting{}, NumericFormatting{}} {
		_, changed, err := node.SpecializeTemplate(context.Background(), never)
		if err != nil || changed {
			t.Errorf("%T specialized a non-literal template: %v %v", node, changed, err)
		}
	}
}

func TestArrayTransformEvaluatesFormalSlots(t *testing.T) {
	// Written as (element => e, array => a): the array formal comes first.
	node := ArrayTransform{Args: []expr.Expr{nil, nil}, Slots: [3]uint8{1, 0}}
	if got := node.EvaluationOrder(); len(got) != 2 || got[0] != 1 || got[1] != 0 {
		t.Errorf("evaluation order %v, want [1 0]", got)
	}
}

func TestSubstituteKeepsTheProvider(t *testing.T) {
	types := formatUnitTypes()
	bytea := types[sqltype.Bytea]
	plan, err := sqltype.BindBinary("btrim", bytea.Usage(), bytea.Usage(), bytea.Usage())
	if err != nil {
		t.Fatal(err)
	}
	arg := binaryUnitArgument{Info: expr.Info{Usage: bytea.Usage()}}
	node := Binary{Info: expr.Info{Usage: bytea.Usage()}, Plan: plan, Args: []expr.Expr{arg, arg}}
	// A child whose usage the provider would not bind: Rebuild rejects it,
	// Substitute keeps the bound provider as preflight always did.
	other := binaryUnitArgument{Info: expr.Info{Usage: types[sqltype.Int4].Usage()}}
	if _, err := node.Rebuild(node.Info, []expr.Expr{arg, other}); err == nil {
		t.Fatal("Rebuild bound a mismatched child")
	}
	got, err := node.Substitute(node.Info, []expr.Expr{arg, other})
	if err != nil || got.(Binary).Plan != plan || got.(Binary).Args[1] != other {
		t.Fatalf("Substitute: %v", err)
	}
	if _, err := node.Substitute(node.Info, []expr.Expr{arg}); err == nil {
		t.Fatal("Substitute accepted a different arity")
	}
}
