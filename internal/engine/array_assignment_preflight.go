package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

func assignmentTargetHasVirtualReads(target *plan.ArrayAssignmentTarget) bool {
	for ; target != nil; target = target.Next {
		if target.Attribute == 0 && exec.BoundHasVirtualReads(target.Node) {
			return true
		}
	}
	return false
}

func preflightArrayTargets(c *exec.Context, targets []*plan.ArrayAssignmentTarget) ([]*plan.ArrayAssignmentTarget, error) {
	return preflightArrayTargetsWithPlanning(c, targets, false)
}

func preflightArrayTargetsWithPlanning(c *exec.Context, targets []*plan.ArrayAssignmentTarget, planConstants bool) ([]*plan.ArrayAssignmentTarget, error) {
	var replacements []*plan.ArrayAssignmentTarget
	for i, target := range targets {
		if target == nil {
			continue
		}
		next, err := preflightAssignmentTarget(c, target, planConstants)
		if err != nil {
			return nil, err
		}
		if next != target {
			if replacements == nil {
				replacements = append([]*plan.ArrayAssignmentTarget(nil), targets...)
			}
			replacements[i] = next
		}
	}
	if replacements == nil {
		return targets, nil
	}
	return replacements, nil
}

func preflightAssignmentTarget(c *exec.Context, target *plan.ArrayAssignmentTarget, planConstants bool) (*plan.ArrayAssignmentTarget, error) {
	if target == nil {
		return nil, nil
	}
	node, changed, err := mapSubscript(target.Node, func(old expr.Expr) (expr.Expr, bool, error) { return preflightUpdateExpression(c, old, planConstants) })
	if err != nil {
		return nil, err
	}
	next, err := preflightAssignmentTarget(c, target.Next, planConstants)
	if err != nil {
		return nil, err
	}
	if !changed && next == target.Next {
		return target, nil
	}
	copy := *target
	copy.Node, copy.Next = node, next
	return &copy, nil
}
