package stored

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func RewriteViewExpression(view catalog.Read, bound expr.Expr, recipe expr.RoutineRecipeReference, leaf func(expr.Expr) (expr.Expr, bool, error), queries ...func(plan.Node, func(expr.Expr) (expr.Expr, bool, error)) (plan.Node, error)) (expr.Expr, bool, error) {
	var mapper func(expr.Expr) (expr.Expr, bool, error)
	mapper = func(n expr.Expr) (expr.Expr, bool, error) {
		return MapExpression(view, n, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, func(root plan.Node) (plan.Node, error) {
			if len(queries) != 0 {
				return queries[0](root, mapper)
			}
			if recipe != nil {
				// Query-owned aggregate/window calls are not scalar children.
				// Carry this original occurrence's recipe through their shared
				// query mapper before any later producer substitution.
				return NewRuleTreeMapper(view, nil, 0, nil, RuleTreeMapping{Recipe: recipe, Leaf: leaf}).Query(root)
			}
			return plan.RewriteQueryExpressions(root, mapper)
		}, recipe, leaf)
	}
	return mapper(bound)
}
