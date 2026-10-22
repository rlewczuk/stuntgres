package stored

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
)

// VisitRuleActionExpressions enumerates the scalar roots owned by a mutation's
// synthetic presentation, in presentation order. Source queries are separate
// query owners; scalar subqueries remain children of their owning expression.
func VisitRuleActionExpressions(b analyze.Binding, visit func(expr.Expr)) {
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
