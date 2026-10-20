package analyze

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// SameQuerySetOccurrence reports whether a and b are the same set-returning
// call occurrence.
func SameQuerySetOccurrence(a, b expr.Expr) bool {
	if !exec.SourceReturnsSet(a) || !exec.SourceReturnsSet(b) || a.SourcePosition() != b.SourcePosition() {
		return false
	}
	if occurrence, ok := a.(expr.SourceOccurrence); ok {
		return occurrence.SameSourceOccurrence(b)
	}
	return false
}

// QuerySetStageSteps maps original's steps onto a set stage with these call
// and scalar slots, dropping the steps the stage does not evaluate.
func QuerySetStageSteps(original []plan.SetStep, callSlots, scalarSlots []int, scalars int) []plan.SetStep {
	steps := make([]plan.SetStep, 0, len(callSlots)+scalars)
	for _, step := range original {
		if local, present := exec.StringSetLocalStep(step, callSlots, scalarSlots, scalars); present {
			steps = append(steps, local)
		}
	}
	return steps
}

// QuerySetProjectionDepth returns the deepest set-returning nesting among
// expressions and whether an expression at that depth is not itself a
// set-returning call.
func QuerySetProjectionDepth(expressions []expr.Expr) (int, bool) {
	depth, upper := 0, false
	for _, expression := range expressions {
		current := exec.QuerySetDepth(expression)
		if current > depth {
			depth, upper = current, !exec.SourceReturnsSet(expression)
		} else if current > 0 && current == depth && !exec.SourceReturnsSet(expression) {
			upper = true
		}
	}
	return depth, upper
}

// Only the current owner's direct scalar roots or reserved scalar references
// change ownership. Child query frames and unrelated scalar subexpressions do not.
func QueryScalarOwner(child plan.Node, expression expr.Expr) (expr.Expr, bool) {
	for child != nil {
		if node, ok := child.(*plan.SetProjectNode); ok {
			for _, binding := range node.ScalarBindings {
				column := binding.Column.(plan.ColumnExpr)
				existing, isColumn := expression.(plan.ColumnExpr)
				matches := isColumn && existing.Frame == column.Frame && existing.Scope == column.Scope && existing.Slot == column.Slot &&
					existing.SourcePosition() == column.SourcePosition()
				if isColumn && !matches {
					continue
				}
				if _, cached := expression.(plan.CachedOrderExpr); cached {
					continue
				}
				if !matches && expression.SourcePosition() != column.SourcePosition() && expression.SourcePosition() != binding.Original.SourcePosition() {
					continue
				}
				if node.ScalarUpper || !binding.Scheduled {
					return binding.Original, true
				}
				return binding.Column, true
			}
		}
		switch child.(type) {
		case *plan.ProjectNode, *plan.DerivedNode, *plan.SourceNode, *plan.JoinNode, *plan.DistinctNode, *plan.DistinctOnNode, *plan.AggregateNode, *plan.SetNode:
			return expression, false
		}
		next, right := plan.QueryInputs(child)
		if right != nil {
			return expression, false
		}
		child = next
	}
	return expression, false
}

// A demanded level brings its peers and their lower dependencies, not unrelated
// higher target levels. Templates retain complete scalar roots so simplification
// can erase a peer's occurrence rather than making its extracted marker live.
func QuerySetPeerClosure(requests []expr.Expr, companions [][]expr.Expr) ([]expr.Expr, []expr.Expr) {
	depth := 0
	for _, expression := range requests {
		depth = max(depth, exec.QuerySetDepth(expression))
	}
	if depth == 0 {
		return requests, nil
	}
	templates := append([]expr.Expr(nil), requests...)
	for _, expressions := range companions {
		templates = append(templates, expressions...)
		for _, expression := range expressions {
			plan.WalkExpression(expression, func(marker expr.Expr) {
				if exec.SourceReturnsSet(marker) && exec.QuerySetDepth(marker) <= depth {
					requests = append(requests, marker)
				}
			})
		}
	}
	return requests, templates
}

// Selected rendering and binding share a lane; typed admission remains owned
// by the node's SourceAdmission after ordinary function resolution.
func SelectedFunctionSourceName(name string) bool {
	return name == "jsonb_path_query" || parser.RegexpFunctionName(name) || parser.JSONFunctionName(name) ||
		parser.CatalogRelationFunctionName(name) ||
		parser.ObjectAddressFunctionName(name) || parser.CommitTimestampFunctionName(name) || parser.TransactionIdentityFunctionName(name) ||
		name == "age" || name == "aclexplode" || name == "acldefault" || name == "makeaclitem" || name == "int_array_enum" || name == "generate_series" || name == "generate_subscripts" || name == "unnest" || name == "multirange"
}
