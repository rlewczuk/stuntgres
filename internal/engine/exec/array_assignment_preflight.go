package exec

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// AssignmentTargetHasIdentityReads reports whether an array assignment
// target chain demands query source identity.
func AssignmentTargetHasIdentityReads(target *plan.ArrayAssignmentTarget) bool {
	for ; target != nil; target = target.Next {
		if target.Attribute == 0 && BoundHasIdentityReads(target.Node) {
			return true
		}
	}
	return false
}

// PreflightArrayTargets preflights the subscripts of array assignment
// targets without constant planning.
func PreflightArrayTargets(c *Context, targets []*plan.ArrayAssignmentTarget) ([]*plan.ArrayAssignmentTarget, error) {
	return PreflightArrayTargetsWithPlanning(c, targets, false)
}

// PreflightArrayTargetsWithPlanning preflights the subscripts of array
// assignment targets, planning SQL constants first when planConstants is set.
func PreflightArrayTargetsWithPlanning(c *Context, targets []*plan.ArrayAssignmentTarget, planConstants bool) ([]*plan.ArrayAssignmentTarget, error) {
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

func preflightAssignmentTarget(c *Context, target *plan.ArrayAssignmentTarget, planConstants bool) (*plan.ArrayAssignmentTarget, error) {
	if target == nil {
		return nil, nil
	}
	node, changed, err := plan.MapSubscript(target.Node, func(old expr.Expr) (expr.Expr, bool, error) { return PreflightUpdateExpression(c, old, planConstants) })
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

// PreflightUpdateExpression preflights an UPDATE value or subscript,
// planning SQL constants first when planConstants is set.
func PreflightUpdateExpression(c *Context, expression expr.Expr, planConstants bool) (expr.Expr, bool, error) {
	changed := false
	if planConstants {
		// Plan SQL constants before importing execution-frame coercions:
		// imported parameter literals must not become SQL constants.
		next, did, err := SimplifyConditional(c, expression, false)
		if err != nil {
			return nil, false, err
		}
		expression, changed = next, did
	}
	next, did, err := PreflightCoercions(c, expression)
	return next, changed || did, err
}
