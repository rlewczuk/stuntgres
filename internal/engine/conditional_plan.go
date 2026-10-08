package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// simplifyConditional folds only immutable SQL constants. Imported parameters
// remain runtime-owned even when the surrounding expression is row-independent.
func simplifyConditional(c *EvalContext, expression BoundExpr, decisiveDefaults bool) (BoundExpr, bool, error) {
	if expression == nil {
		return nil, false, nil
	}
	if err := evaluationContext(c); err != nil {
		return nil, false, err
	}
	changed := false
	child := func(old BoundExpr) (BoundExpr, error) {
		node, did, err := simplifyConditional(c, old, decisiveDefaults)
		changed = changed || did
		return node, err
	}
	var err error
	switch e := expression.(type) {
	case caseExpression:
		if e.test != nil {
			e.test, err = child(e.test)
			if err != nil {
				return nil, false, err
			}
		}
		var arms []caseBranch
		for i, old := range e.arms {
			arm := old
			arm.condition, err = child(arm.condition)
			if err != nil {
				return nil, false, err
			}
			known, match := false, false
			if condition, ok := arm.condition.(literalExpression); ok {
				if e.test == nil {
					known = true
					if !condition.value.IsNull() {
						match, err = condition.value.Boolean()
					}
				} else if test, ok := e.test.(literalExpression); ok {
					known = true
					var view sqltype.Value
					view, err = arm.testPlan.Apply(c.Context, test.value, c.Input, c.Output)
					if err == nil && !view.IsNull() && !condition.value.IsNull() {
						var sign int
						sign, err = arm.equality.Compare(c.Context, view, condition.value, c.Input)
						match = sign == 0
					}
				}
				if err != nil {
					return nil, false, err
				}
			}
			if known && !match {
				if arms == nil {
					arms = append([]caseBranch{}, e.arms[:i]...)
				}
				changed = true
				continue
			}
			arm.result, err = child(arm.result)
			if err != nil {
				return nil, false, err
			}
			if known && match {
				if arms == nil && i == 0 {
					return plannedConditionalResult(e.expressionInfo, arm.result), true, nil
				}
				if arms == nil {
					arms = append([]caseBranch{}, e.arms[:i]...)
				}
				e.arms = arms
				e.otherwise = arm.result
				return e, true, nil
			}
			if changed && arms == nil {
				arms = append([]caseBranch{}, e.arms[:i]...)
			}
			if arms != nil {
				arms = append(arms, arm)
			}
		}
		if arms != nil {
			e.arms = arms
		}
		e.otherwise, err = child(e.otherwise)
		if err != nil {
			return nil, false, err
		}
		if len(e.arms) == 0 {
			return plannedConditionalResult(e.expressionInfo, e.otherwise), true, nil
		}
		if changed {
			return e, true, nil
		}
		return expression, false, nil
	case conditionalExpression:
		var args []BoundExpr
		for i, old := range e.args {
			node, err := child(old)
			if err != nil {
				return nil, false, err
			}
			if e.kind == parser.Coalesce {
				if literal, ok := node.(literalExpression); ok {
					if literal.value.IsNull() {
						if args == nil {
							args = append([]BoundExpr{}, e.args[:i]...)
						}
						changed = true
						continue
					}
					if args == nil && i == 0 {
						return plannedConditionalResult(e.expressionInfo, literal), true, nil
					}
					if args == nil {
						args = append([]BoundExpr{}, e.args[:i]...)
					}
					e.args = append(args, node)
					return e, true, nil
				}
			}
			if changed && args == nil {
				args = append([]BoundExpr{}, e.args[:i]...)
			}
			if args != nil {
				args = append(args, node)
			}
		}
		if args != nil {
			e.args = args
		}
		if len(e.args) == 0 {
			return literalExpression{e.expressionInfo, sqltype.Null(e.usage.Type)}, true, nil
		}
		expression = e
	case timeZoneExpression:
		e.value, err = child(e.value)
		if err == nil && !e.local {
			e.zone, err = child(e.zone)
		}
		expression = e
	case coercionExpression:
		e.child, err = child(e.child)
		expression = e
	case bitwiseExpression:
		e.left, err = child(e.left)
		if err == nil {
			e.right, err = child(e.right)
		}
		if err == nil && (bitwiseConstantNull(e.left) || bitwiseConstantNull(e.right)) {
			return preflightValue(c, e.expressionInfo, sqltype.Null(e.usage.Type), nil)
		}
		expression = e
	case arithmeticExpression:
		e.left, err = child(e.left)
		if err == nil {
			e.right, err = child(e.right)
		}
		expression = e
	case comparisonExpression:
		e.left, err = child(e.left)
		if err == nil {
			e.right, err = child(e.right)
		}
		expression = e
	case logicalExpression:
		if decisiveDefaults {
			// Reuse the existing consumed-default AND/OR policy before
			// visiting a right child that a decisive left constant excludes.
			return preflightCoercionsWithPolicy(c, e, true)
		}
		e.left, err = child(e.left)
		if err == nil {
			e.right, err = child(e.right)
		}
		expression = e
	case nullTestExpression:
		e.child, err = child(e.child)
		expression = e
	case truthTestExpression:
		e.child, err = child(e.child)
		expression = e
	case distinctExpression:
		e.left, err = child(e.left)
		if err == nil {
			e.right, err = child(e.right)
		}
		expression = e
	case sequenceCallExpression:
		var args []BoundExpr
		for i, old := range e.args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = node
				changed = true
			}
		}
		if args != nil {
			e.args = args
		}
		expression = e
	case binaryExpression:
		var args []BoundExpr
		for i, old := range e.args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = node
				changed = true
			}
		}
		if args != nil {
			e.args = args
		}
		for _, arg := range e.args {
			if randomLiteralNull(arg) {
				return literalExpression{e.expressionInfo, sqltype.Null(e.usage.Type)}, true, nil
			}
		}
		expression = e
	case stringExpression:
		var args []BoundExpr
		for i, old := range e.args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = node
				changed = true
			}
		}
		if args != nil {
			e.args = args
		}
		for _, arg := range e.args {
			if randomLiteralNull(arg) {
				return literalExpression{e.expressionInfo, sqltype.Null(e.usage.Type)}, true, nil
			}
		}
		expression = e
	case mathExpression:
		var args []BoundExpr
		for i, old := range e.args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = node
				changed = true
			}
		}
		if args != nil {
			e.args = args
		}
		for _, arg := range e.args {
			if randomLiteralNull(arg) {
				return literalExpression{e.expressionInfo, sqltype.Null(e.usage.Type)}, true, nil
			}
		}
		expression = e
	case randomExpression:
		var args []BoundExpr
		for i, old := range e.args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = node
				changed = true
			}
		}
		if args != nil {
			e.args = args
		}
		expression = e
	case visibilityInquiryExpression:
		node, did, failure := simplifyConditional(c, e.arg, decisiveDefaults)
		if failure != nil {
			return nil, false, failure
		}
		if did {
			e.arg = node
			changed = true
		}
		expression = e
	case currentSchemasExpression:
		node, did, failure := simplifyConditional(c, e.arg, decisiveDefaults)
		if failure != nil {
			return nil, false, failure
		}
		if did {
			e.arg = node
			changed = true
		}
		expression = e
	case deparseExpression:
		var args []BoundExpr
		for i, old := range e.args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = node
				changed = true
			}
		}
		if args != nil {
			e.args = args
		}
		expression = e
	case commentInquiryExpression:
		var args []BoundExpr
		for i, old := range e.args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = node
				changed = true
			}
		}
		if args != nil {
			e.args = args
		}
		expression = e
	case nullCountExpression:
		var args []BoundExpr
		for i, old := range e.args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.args...)
				}
				args[i] = node
				changed = true
			}
		}
		if args != nil {
			e.args = args
		}
		expression = e
	}
	if err != nil {
		return nil, false, err
	}
	p := expression.Properties()
	if _, literal := expression.(literalExpression); !literal && p.Volatility == Immutable && conditionalConstantInputs(expression) {
		value, err := expression.Eval(c)
		if err != nil {
			return nil, false, err
		}
		return literalExpression{expressionInfo{usage: expression.ResultUsage(), pos: expression.SourcePosition(), properties: p}, value}, true, nil
	}
	return expression, changed, nil
}

// Keep the analyzed usage, position and conservative dependence even when a
// decisive constant eliminates runtime branches. Nonliteral selection uses the
// existing CASE control node with no tests, not a second evaluator or a cast.
func plannedConditionalResult(info expressionInfo, node BoundExpr) BoundExpr {
	if literal, ok := node.(literalExpression); ok {
		literal.expressionInfo = info
		return literal
	}
	return caseExpression{expressionInfo: info, otherwise: node}
}

// Analysis properties deliberately retain dead-arm dependence. Physical
// literal children after pruning, not those conservative properties, identify
// a truly immutable constant operation. Volatile/stage/parameter leaves never
// enter this set and sequence producers are never folded.
func conditionalConstantInputs(node BoundExpr) bool {
	literal := func(child BoundExpr) bool { _, ok := child.(literalExpression); return ok }
	switch e := node.(type) {
	case coercionExpression:
		return literal(e.child)
	case bitwiseExpression:
		return literal(e.left) && (e.right == nil || literal(e.right))
	case arithmeticExpression:
		return literal(e.left) && (e.right == nil || literal(e.right))
	case comparisonExpression:
		return literal(e.left) && literal(e.right)
	case logicalExpression:
		return literal(e.left) && (e.right == nil || literal(e.right))
	case nullTestExpression:
		return literal(e.child)
	case truthTestExpression:
		return literal(e.child)
	case distinctExpression:
		return literal(e.left) && literal(e.right)
	case conditionalExpression:
		for _, arg := range e.args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case binaryExpression:
		for _, arg := range e.args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case stringExpression:
		for _, arg := range e.args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case mathExpression:
		for _, arg := range e.args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case nullCountExpression:
		for _, arg := range e.args {
			if !literal(arg) {
				return false
			}
		}
		return true
	}
	return false
}
