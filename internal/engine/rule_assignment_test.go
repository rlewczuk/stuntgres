package engine

import (
	"context"
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
	index := literalExpression{Info: binder.info(integer.Usage(), parser.Position{}, ExprProperties{}), value: value}
	plan, err := sqltype.PlanCoercion(array.Usage(), array.Usage(), sqltype.Assignment)
	if err != nil {
		t.Fatal(err)
	}
	tail := &arrayAssignmentTarget{container: array.Usage(), destination: integer.Usage(), reapply: plan, node: subscriptExpression{subscripts: []boundArraySubscript{{upper: index}}}}
	head := &arrayAssignmentTarget{container: array.Usage(), destination: array.Usage(), reapply: plan, next: tail}
	targets := []*arrayAssignmentTarget{head, nil}
	identity := func(expr BoundExpr) (BoundExpr, bool, error) { return expr, false, nil }
	usage := func(usage sqltype.Usage) (sqltype.Usage, error) { return usage, nil }
	mapped, err := mapRuleAssignmentTargets(targets, identity, usage)
	if err != nil || &mapped[0] != &targets[0] || mapped[0] != head || mapped[0].next != tail {
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
	replacement := parameterExpression{Info: binder.info(integer.Usage(), parser.Position{}, ExprProperties{ParameterDependent: true}), number: 1}
	changed, err := mapRuleAssignmentTargets(targets, func(expr BoundExpr) (BoundExpr, bool, error) {
		if expr == nil {
			return nil, false, nil
		}
		return replacement, true, nil
	}, usage)
	if err != nil || changed[0] == head || changed[0].next == tail || changed[1] != nil {
		t.Fatalf("changed bound did not copy its target path: %v", err)
	}
	if head.next != tail || tail.node.subscripts[0].upper != index {
		t.Fatal("mapping mutated original assignment")
	}
	if changed[0].next.node.subscripts[0].upper != replacement {
		t.Fatal("mapped bound lost replacement")
	}
}
