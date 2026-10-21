package stored

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func RewriteViewExpression(view catalog.Read, bound expr.Expr, recipe *catalog.Relation, leaf func(expr.Expr) (expr.Expr, bool, error), queries ...func(plan.Node, func(expr.Expr) (expr.Expr, bool, error)) (plan.Node, error)) (expr.Expr, bool, error) {
	var mapper func(expr.Expr) (expr.Expr, bool, error)
	mapper = func(n expr.Expr) (expr.Expr, bool, error) {
		return MapExpression(view, n, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, func(root plan.Node) (plan.Node, error) {
			if len(queries) != 0 {
				return queries[0](root, mapper)
			}
			return plan.RewriteQueryExpressions(root, mapper)
		}, recipe, leaf)
	}
	return mapper(bound)
}
