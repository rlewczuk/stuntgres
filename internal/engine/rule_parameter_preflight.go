package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

// Specialization borrows this operation's already imported typed frame. It does
// not evaluate operators, functions, domains or producers, and never mutates the
// analyzed recipe. The ordinary mapper rederives only changed child ancestry.
func SpecializeRuleParameterExpression(c *exec.Context, expression expr.Expr) (expr.Expr, bool, error) {
	return stored.RewriteViewExpression(c.Catalog, expression, nil, func(node expr.Expr) (expr.Expr, bool, error) {
		parameter, ok := node.(plan.ParameterExpr)
		if !ok {
			return node, false, nil
		}
		value, err := parameter.Eval(c)
		if err != nil {
			return nil, false, err
		}
		info := parameter.Info
		info.Property.ParameterDependent = false
		return plan.LiteralExpr{Info: info, Value: value}, true, nil
	}, func(root plan.Node, _ func(expr.Expr) (expr.Expr, bool, error)) (plan.Node, error) {
		// A query in a discarded conditional branch is not scalar demand.
		// Its own preparation entry specializes retained expressions only
		// after canonical conditional/projection demand selects this query.
		return root, nil
	})
}
