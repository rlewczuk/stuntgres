package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
)

func TestTriggerDepthCatalog(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	n, _, _ := strictNullUnitBind(t, e, op, scope, "pg_trigger_depth()")
	d, err := exec.ExpressionRoutineDescriptor(n)
	if err != nil || d == nil {
		t.Fatal(d, err)
	}
	// Builtin parallel policy is declaration-owned; Procedure.Parallel is an
	// optional mutable override, not the released builtin's policy field.
	if uint32(d.Address().ObjectID) != 3163 || d.Name() != "pg_trigger_depth" || d.Result != 23 || d.RoutineKind != catalog.FunctionRoutine || !d.Strict || d.Volatility != expr.Stable || d.Builtin == nil || d.Builtin.Parallel != 'r' || d.Cost != 1 || d.Rows != 0 || d.ReturnsSet() || d.SecurityDefiner {
		t.Fatal("released declaration", d)
	}
	if _, ok := n.(functions.TriggerDepth); !ok {
		t.Fatalf("provider selected %T", n)
	}
	// Both qualification routes resolve the same executable identity, rather
	// than identity-only catalog display entries or a spelling-only evaluator.
	qualified, _, _ := strictNullUnitBind(t, e, op, scope, "pg_catalog.pg_trigger_depth()")
	other, err := exec.ExpressionRoutineDescriptor(qualified)
	if err != nil || other != d {
		t.Fatal("qualified identity", other, d, err)
	}
}
