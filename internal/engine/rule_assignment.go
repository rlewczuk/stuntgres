package engine

import (
	"slices"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func mapRuleAssignmentTargets(targets []*arrayAssignmentTarget, expression func(expr.Expr) (expr.Expr, bool, error), usage func(sqltype.Usage) (sqltype.Usage, error)) ([]*arrayAssignmentTarget, error) {
	var target func(*arrayAssignmentTarget) (*arrayAssignmentTarget, error)
	target = func(old *arrayAssignmentTarget) (*arrayAssignmentTarget, error) {
		if old == nil {
			return nil, nil
		}
		node, changed, err := mapSubscript(old.node, expression)
		if err != nil {
			return nil, err
		}
		container, err := usage(old.container)
		if err != nil {
			return nil, err
		}
		destination, err := usage(old.destination)
		if err != nil {
			return nil, err
		}
		child, err := target(old.next)
		if err != nil {
			return nil, err
		}
		if !changed && container == old.container && destination == old.destination && child == old.next {
			return old, nil
		}
		next := *old
		next.node, next.container, next.destination, next.next = node, container, destination, child
		if container != old.container {
			next.reapply, err = sqltype.PlanCoercion(container.BaseUsage(), container, sqltype.Assignment)
			if err != nil {
				return nil, err
			}
		}
		return &next, nil
	}
	var next []*arrayAssignmentTarget
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
