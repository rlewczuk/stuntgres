package dml

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
)

// Reuse the shared coercion traversal; only changed execution branches are
// copied. In particular, imported-frame literals never replace analyzed nodes.
func PreflightUpdate(c *exec.Context, original *analyze.UpdateBinding, sourceDemand ...func(*analyze.UpdateBinding) (plan.Node, error)) (*analyze.UpdateBinding, error) {
	return PreflightUpdateWithPlanning(c, original, true, sourceDemand...)
}

func PreflightUpdateWithPlanning(c *exec.Context, original *analyze.UpdateBinding, planConstants bool, sourceDemand ...func(*analyze.UpdateBinding) (plan.Node, error)) (*analyze.UpdateBinding, error) {
	result := original
	var assignments []expr.Expr
	targets, err := exec.PreflightArrayTargetsWithPlanning(c, original.ArrayTargets, planConstants)
	if err != nil {
		return nil, err
	}
	if len(targets) != 0 && &targets[0] != &original.ArrayTargets[0] {
		private := *original
		private.ArrayTargets = targets
		result = &private
	}
	for i, expression := range original.Assignments {
		owner := c
		var recipe exec.Context
		if len(original.DefaultOrigins) != 0 && original.DefaultOrigins[i].Requested {
			recipe = *c
			analyze.ApplyDefaultOrigin(original.DefaultOrigins[i], &recipe)
			owner = &recipe
		}
		folded, changed, err := exec.PreflightUpdateExpression(owner, expression, planConstants)
		if err != nil {
			return nil, err
		}
		if changed {
			if assignments == nil {
				assignments = append([]expr.Expr(nil), original.Assignments...)
			}
			assignments[i] = folded
		}
	}
	if assignments != nil {
		private := *result
		private.Assignments = assignments
		result = &private
	}
	if original.Predicate != nil {
		folded, changed, err := exec.PreflightUpdateExpression(c, original.Predicate, planConstants)
		if err != nil {
			return nil, err
		}
		if changed {
			if result == original {
				private := *original
				result = &private
			}
			result.Predicate = folded
		}
	}
	if original.From != nil {
		from := original.From
		if c.PlanStaticOperators && len(sourceDemand) != 0 {
			from, err = sourceDemand[0](result)
			if err != nil {
				return nil, err
			}
		}
		folded, err := view.PreflightQuery(c, from)
		if err != nil {
			return nil, err
		}
		if folded != original.From {
			if result == original {
				private := *original
				result = &private
			}
			result.From = folded
		}
	}
	return result, nil
}
