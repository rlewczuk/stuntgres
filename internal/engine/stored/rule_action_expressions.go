package stored

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// VisitRuleActionExpressions enumerates the scalar roots owned by a mutation's
// synthetic presentation, in presentation order. Source queries are separate
// query owners; scalar subqueries remain children of their owning expression.
func VisitRuleActionExpressions(b analyze.Binding, visit func(expr.Expr)) {
	visitRuleActionValues(b, visit)
	targets := func(paths []*plan.ArrayAssignmentTarget) {
		for _, path := range paths {
			for ; path != nil; path = path.Next {
				for _, dimension := range path.Node.Subscripts {
					if dimension.Lower != nil {
						visit(dimension.Lower)
					}
					if dimension.Upper != nil {
						visit(dimension.Upper)
					}
				}
			}
		}
	}
	targets(b.ArrayTargets)
	if b.Update != nil {
		targets(b.Update.ArrayTargets)
	}
	if b.Merge != nil {
		for _, arm := range b.Merge.Arms {
			targets(arm.Input.ArrayTargets)
			if arm.Input.Update != nil {
				targets(arm.Input.Update.ArrayTargets)
			}
		}
	}
	if conflict := b.Insert.Conflict; conflict != nil && conflict.Update != nil {
		targets(conflict.Update.ArrayTargets)
	}
}

// Values have separate structural associations from assignment destinations.
func visitRuleActionValues(b analyze.Binding, visit func(expr.Expr)) {
	for _, row := range b.Expressions {
		for _, node := range row {
			visit(node)
		}
	}
	if b.Update != nil {
		for _, node := range b.Update.Assignments {
			visit(node)
		}
		if b.Update.Predicate != nil {
			visit(b.Update.Predicate)
		}
	}
	if b.Predicate != nil {
		visit(b.Predicate)
	}
	if b.Merge != nil {
		visit(b.Merge.On)
		for _, arm := range b.Merge.Arms {
			if arm.Condition != nil {
				visit(arm.Condition)
			}
			for _, row := range arm.Input.Expressions {
				for _, node := range row {
					visit(node)
				}
			}
			if arm.Input.Update != nil {
				for _, node := range arm.Input.Update.Assignments {
					visit(node)
				}
			}
		}
	}
	if b.Returning != nil {
		for _, node := range b.Returning.Expressions {
			visit(node)
		}
	}
}
