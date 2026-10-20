package engine

import "stuntgres/internal/engine/expr"

// Specialization borrows this operation's already imported typed frame. It does
// not evaluate operators, functions, domains or producers, and never mutates the
// analyzed recipe. The ordinary mapper rederives only changed child ancestry.
func specializeRuleParameterExpression(c *EvalContext, expression expr.Expr) (expr.Expr, bool, error) {
	return rewriteViewExpression(c.catalog, expression, nil, func(node expr.Expr) (expr.Expr, bool, error) {
		parameter, ok := node.(parameterExpression)
		if !ok {
			return node, false, nil
		}
		value, err := parameter.Eval(c)
		if err != nil {
			return nil, false, err
		}
		info := parameter.Info
		info.Property.ParameterDependent = false
		return literalExpression{Info: info, Value: value}, true, nil
	}, func(root LogicalNode, _ func(expr.Expr) (expr.Expr, bool, error)) (LogicalNode, error) {
		// A query in a discarded conditional branch is not scalar demand.
		// Its own preparation entry specializes retained expressions only
		// after canonical conditional/projection demand selects this query.
		return root, nil
	})
}
