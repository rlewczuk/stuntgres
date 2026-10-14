package engine

// Specialization borrows this operation's already imported typed frame. It does
// not evaluate operators, functions, domains or producers, and never mutates the
// analyzed recipe. The ordinary mapper rederives only changed child ancestry.
func specializeRuleParameterExpression(c *EvalContext, expression BoundExpr) (BoundExpr, bool, error) {
	return rewriteViewExpression(c.catalog, expression, func(node BoundExpr) (BoundExpr, bool, error) {
		parameter, ok := node.(parameterExpression)
		if !ok {
			return node, false, nil
		}
		value, err := parameter.Eval(c)
		if err != nil {
			return nil, false, err
		}
		info := parameter.expressionInfo
		info.properties.ParameterDependent = false
		return literalExpression{expressionInfo: info, value: value}, true, nil
	}, func(root LogicalNode, _ func(BoundExpr) (BoundExpr, bool, error)) (LogicalNode, error) {
		// A query in a discarded conditional branch is not scalar demand.
		// Its own preparation entry specializes retained expressions only
		// after canonical conditional/projection demand selects this query.
		return root, nil
	})
}
