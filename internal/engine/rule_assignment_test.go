package engine

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestRuleAssignmentMapping(t *testing.T) {
	e := New()
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	integer := binder.typ(sqltype.Int4)
	array := binder.typ(integer.AssociatedArray())
	value, err := sqltype.Integer(integer, 1)
	if err != nil {
		t.Fatal(err)
	}
	index := plan.LiteralExpr{Info: binder.info(integer.Usage(), parser.Position{}, expr.Properties{}), Value: value}
	coercionPlan, err := sqltype.PlanCoercion(array.Usage(), array.Usage(), sqltype.Assignment)
	if err != nil {
		t.Fatal(err)
	}
	tail := &plan.ArrayAssignmentTarget{Container: array.Usage(), Destination: integer.Usage(), Reapply: coercionPlan, Node: plan.SubscriptExpr{Subscripts: []plan.BoundArraySubscript{{Upper: index}}}}
	head := &plan.ArrayAssignmentTarget{Container: array.Usage(), Destination: array.Usage(), Reapply: coercionPlan, Next: tail}
	targets := []*plan.ArrayAssignmentTarget{head, nil}
	identity := func(expr expr.Expr) (expr.Expr, bool, error) { return expr, false, nil }
	usage := func(usage sqltype.Usage) (sqltype.Usage, error) { return usage, nil }
	mapped, err := mapRuleAssignmentTargets(targets, identity, usage)
	if err != nil || &mapped[0] != &targets[0] || mapped[0] != head || mapped[0].Next != tail {
		t.Fatalf("identity mapping copied immutable target paths: %v", err)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		result, failure := mapRuleAssignmentTargets(targets, identity, usage)
		if failure != nil || &result[0] != &targets[0] {
			panic("identity mapping changed target")
		}
	}); allocations != 0 {
		t.Fatalf("identity mapping allocated %g times", allocations)
	}
	replacement := plan.ParameterExpr{Info: binder.info(integer.Usage(), parser.Position{}, expr.Properties{ParameterDependent: true}), Number: 1}
	changed, err := mapRuleAssignmentTargets(targets, func(expr expr.Expr) (expr.Expr, bool, error) {
		if expr == nil {
			return nil, false, nil
		}
		return replacement, true, nil
	}, usage)
	if err != nil || changed[0] == head || changed[0].Next == tail || changed[1] != nil {
		t.Fatalf("changed bound did not copy its target path: %v", err)
	}
	if head.Next != tail || tail.Node.Subscripts[0].Upper != index {
		t.Fatal("mapping mutated original assignment")
	}
	if changed[0].Next.Node.Subscripts[0].Upper != replacement {
		t.Fatal("mapped bound lost replacement")
	}
}
