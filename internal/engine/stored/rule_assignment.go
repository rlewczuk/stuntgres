package stored

import (
	"slices"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func MapRuleAssignmentTargets(targets []*plan.ArrayAssignmentTarget, expression func(expr.Expr) (expr.Expr, bool, error), usage func(sqltype.Usage) (sqltype.Usage, error)) ([]*plan.ArrayAssignmentTarget, error) {
	var target func(*plan.ArrayAssignmentTarget) (*plan.ArrayAssignmentTarget, error)
	target = func(old *plan.ArrayAssignmentTarget) (*plan.ArrayAssignmentTarget, error) {
		if old == nil {
			return nil, nil
		}
		node, changed, err := plan.MapSubscript(old.Node, expression)
		if err != nil {
			return nil, err
		}
		container, err := usage(old.Container)
		if err != nil {
			return nil, err
		}
		destination, err := usage(old.Destination)
		if err != nil {
			return nil, err
		}
		child, err := target(old.Next)
		if err != nil {
			return nil, err
		}
		if !changed && container == old.Container && destination == old.Destination && child == old.Next {
			return old, nil
		}
		next := *old
		next.Node, next.Container, next.Destination, next.Next = node, container, destination, child
		if container != old.Container {
			next.Reapply, err = sqltype.PlanCoercion(container.BaseUsage(), container, sqltype.Assignment)
			if err != nil {
				return nil, err
			}
		}
		return &next, nil
	}
	var next []*plan.ArrayAssignmentTarget
	for i, old := range targets {
		mapped, err := target(old)
		if err != nil {
			return nil, err
		}
		if mapped != old {
			if next == nil {
				next = slices.Clone(targets)
			}
			next[i] = mapped
		}
	}
	if next == nil {
		return targets, nil
	}
	return next, nil
}
