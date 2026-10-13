package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// A physical non-NULL BOOL left operand decides AND/OR without its right
// operand. Coercion preflight and planning demand share this literal policy.
func decisiveLogicalValue(e *logicalExpression) (sqltype.Value, bool, error) {
	literal, ok := e.left.(literalExpression)
	if !ok || literal.value.Type() == nil || !literal.value.Type().Concrete() ||
		literal.value.Type().ID() != sqltype.Bool || literal.value.IsNull() {
		return sqltype.Value{}, false, nil
	}
	truth, err := TruthOf(literal.value)
	if err != nil {
		return sqltype.Value{}, false, err
	}
	if (e.op == parser.LogicalOr && truth == True) || (e.op == parser.LogicalAnd && truth == False) {
		return literal.value, true, nil
	}
	return sqltype.Value{}, false, nil
}

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
	case rowExpression:
		var did bool
		e, did, err = mapRow(e, func(old BoundExpr) (BoundExpr, bool, error) { return simplifyConditional(c, old, decisiveDefaults) })
		if err != nil {
			return nil, false, err
		}
		changed = changed || did
		expression = e
	case fieldExpression:
		e.base, err = child(e.base)
		if err != nil {
			return nil, false, err
		}
		// Simplify every constructor child before narrowing physical demand:
		// unused immutable failures still precede selected-field execution.
		// Only genuine ROW constructors can discard their other children;
		// source rows retain occurrence absence and transition semantics.
		if row, ok := e.base.(rowExpression); ok && row.raw != nil && e.index >= 0 && e.index < len(row.fields) {
			shape := row.ResultUsage().Type.BaseType()
			index, valid := shape.CompositeAttributeIndex(e.attribute)
			if valid && index == e.index && shape.CompositeMember(index).Usage == e.ResultUsage() &&
				row.fields[index].ResultUsage() == e.ResultUsage() {
				return plannedConditionalResult(e.expressionInfo, row.fields[index]), true, nil
			}
		}
		expression = e
	case arrayExpression:
		var elements []BoundExpr
		for i, old := range e.elements {
			next, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				changed = true
				if elements == nil {
					elements = append([]BoundExpr(nil), e.elements...)
				}
				elements[i] = next
			}
		}
		if elements != nil {
			e.elements = elements
		}
		expression = e
	case subscriptExpression:
		var did bool
		e, did, err = mapSubscript(e, func(old BoundExpr) (BoundExpr, bool, error) { return simplifyConditional(c, old, decisiveDefaults) })
		if err != nil {
			return nil, false, err
		}
		changed = changed || did
		expression = e
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
		if err := e.extremaError(); err != nil {
			return nil, false, err
		}
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
		if randomLiteralNull(e.value) || !e.local && randomLiteralNull(e.zone) {
			return literalExpression{e.expressionInfo, sqltype.Null(e.usage.Type)}, true, nil
		}
		if !e.local {
			e.zone, err = child(e.zone)
		}
		if err == nil {
			e.value, err = child(e.value)
		}
		expression = e
	case collationExpression:
		e.child, err = child(e.child)
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
	case likeExpression:
		e.left, err = child(e.left)
		if err == nil {
			e.right, err = child(e.right)
		}
		expression = e
	case likeEscapeExpression:
		e.pattern, err = child(e.pattern)
		if err == nil {
			e.escape, err = child(e.escape)
		}
		expression = e
	case posixExpression:
		e.left, err = child(e.left)
		if err == nil {
			e.right, err = child(e.right)
		}
		expression = e
	case similarExpression:
		e.left, err = child(e.left)
		if err == nil {
			e.right, err = child(e.right)
		}
		expression = e
	case similarEscapeExpression:
		e.pattern, err = child(e.pattern)
		if err == nil {
			e.escape, err = child(e.escape)
		}
		expression = e
	case posixSubstringExpression:
		for i, arg := range e.args {
			e.args[i], err = child(arg)
			if err != nil {
				break
			}
		}
		expression = e
	case similarSubstringExpression:
		for i, arg := range e.args {
			e.args[i], err = child(arg)
			if err != nil {
				break
			}
		}
		expression = e
	case rowComparisonExpression:
		e.comparisons = append([]comparisonExpression(nil), e.comparisons...)
		for i, pair := range e.comparisons {
			pair.left, err = child(pair.left)
			if err == nil {
				pair.right, err = child(pair.right)
			}
			if err != nil {
				break
			}
			e.comparisons[i] = pair
		}
		expression = e
	case regexpExpression:
		e.args = append([]BoundExpr(nil), e.args...)
		for i, arg := range e.args {
			e.args[i], err = child(arg)
			if err != nil {
				break
			}
		}
		expression = e
	case regexpSetExpression:
		e.args = append([]BoundExpr(nil), e.args...)
		for i, arg := range e.args {
			e.args[i], err = child(arg)
			if err != nil {
				break
			}
		}
		return e, changed, err // Sets are never constant-folded without an input row.
	case scalarArrayComparisonExpression:
		var replaced bool
		e, replaced, err = mapScalarArrayChildren(e, func(old BoundExpr) (BoundExpr, bool, error) {
			return simplifyConditional(c, old, decisiveDefaults)
		})
		changed = changed || replaced
		expression = e
	case jsonKeysExpression:
		arg, failure := child(e.args[0])
		err = failure
		if changed {
			e.args = []BoundExpr{arg}
		}
		return e, changed, err // Cardinality belongs to its query stage.
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
	case datetimeJustifyExpression:
		node, did, failure := simplifyConditional(c, e.arg, decisiveDefaults)
		if failure != nil {
			return nil, false, failure
		}
		if did {
			e.arg, changed = node, true
		}
		if randomLiteralNull(e.arg) {
			return literalExpression{e.expressionInfo, sqltype.Null(e.usage.Type)}, true, nil
		}
		expression = e
	case datetimeExtractExpression:
		for i, old := range e.args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.args[i], changed = node, true
			}
		}
		expression = e
	case datetimeOverlapsExpression:
		for i, old := range e.args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.args[i], changed = node, true
			}
		}
		next, did, failure := simplifyOverlapsRoute(c, e)
		if failure != nil {
			return nil, false, failure
		}
		e, changed = next, changed || did
		expression = e
	case datetimeBinExpression:
		for i, old := range e.args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.args[i], changed = node, true
			}
		}
		for _, arg := range e.args {
			if randomLiteralNull(arg) {
				return literalExpression{e.expressionInfo, sqltype.Null(e.usage.Type)}, true, nil
			}
		}
		expression = e
	case datetimeTruncExpression:
		for i, old := range e.children() {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.args[i], changed = node, true
			}
		}
		for _, arg := range e.children() {
			if randomLiteralNull(arg) {
				return literalExpression{e.expressionInfo, sqltype.Null(e.usage.Type)}, true, nil
			}
		}
		expression = e
	case datetimeZoneCalendarExpression:
		for _, arg := range e.children() {
			if randomLiteralNull(arg) {
				return literalExpression{e.expressionInfo, sqltype.Null(e.usage.Type)}, true, nil
			}
		}
		for i, old := range e.children() {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.args[i], changed = node, true
			}
		}
		expression = e
	case datetimeAgeFiniteExpression:
		for i, old := range e.children() {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.args[i], changed = node, true
			}
		}
		for _, arg := range e.children() {
			if randomLiteralNull(arg) {
				return literalExpression{e.expressionInfo, sqltype.Null(e.usage.Type)}, true, nil
			}
		}
		expression = e
	case datetimeMakeExpression:
		for i, old := range e.children() {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.args[i] = node
				changed = true
			}
		}
		for _, arg := range e.children() {
			if randomLiteralNull(arg) {
				return literalExpression{e.expressionInfo, sqltype.Null(e.usage.Type)}, true, nil
			}
		}
		expression = e
	case datetimeFormattingExpression:
		for i, old := range e.args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.args[i] = node
				changed = true
			}
		}
		for _, arg := range e.args {
			if randomLiteralNull(arg) {
				return literalExpression{e.expressionInfo, sqltype.Null(e.usage.Type)}, true, nil
			}
		}
		expression = e
	case numericFormattingExpression:
		for i, old := range e.args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.args[i] = node
				changed = true
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
		if e.plan.Strict() {
			for _, arg := range e.args {
				if randomLiteralNull(arg) {
					return literalExpression{e.expressionInfo, sqltype.Null(e.usage.Type)}, true, nil
				}
			}
		}
		expression = e
	case arraySearchExpression:
		var did bool
		e, did, err = mapArraySearchChildren(e, func(old BoundExpr) (BoundExpr, bool, error) {
			return simplifyConditional(c, old, decisiveDefaults)
		})
		if err != nil {
			return nil, false, err
		}
		changed = changed || did
		expression = e
	case enumFunctionExpression:
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
	case uuidGenerationExpression:
		if len(e.args) != 0 {
			node, did, failure := simplifyConditional(c, e.args[0], decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.args = []BoundExpr{node}
				changed = true
			}
			if randomLiteralNull(node) {
				// Strictness eliminates demand even for a volatile provider.
				// Preserve analyzed metadata; the surviving literal is immutable.
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
	case datetimeSleepExpression:
		node, did, failure := simplifyConditional(c, e.arg, decisiveDefaults)
		if failure != nil {
			return nil, false, failure
		}
		e.arg = node
		if randomLiteralNull(node) {
			return literalExpression{expressionInfo: expressionInfo{usage: e.usage, pos: e.pos}, value: sqltype.Null(e.usage.Type)}, true, nil
		}
		changed = changed || did
		expression = e
	case advisoryExpression:
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
		if folded, did := foldAdvisoryNull(e); did {
			return folded, true, nil
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
	case indexPropertyExpression:
		e.properties = ExprProperties{Volatility: Stable}
		for i, old := range e.children() {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.args[i] = node
				changed = true
			}
			e.properties = mergeExpressionProperties(e.properties, e.args[i].Properties())
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
	case installedFunctionExpression:
		next, did, failure := simplifyConditional(c, e.arg, decisiveDefaults)
		if failure != nil {
			return nil, false, failure
		}
		e.arg = next
		changed = changed || did
		expression = e
	case jsonCreateExpression:
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
			if err := e.replan(); err != nil {
				return nil, false, err
			}
		}
		expression = e
	case stringVariadicExpression:
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
		node, folded, failure := foldStringVariadic(c, e)
		return node, changed || folded, failure
	case formatExpression:
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
		return literalExpression{boundExpressionInfo(expression), value}, true, nil
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
	case timeZoneExpression:
		return literal(e.value) && (e.local || literal(e.zone))
	case collationExpression:
		return literal(e.child)
	case coercionExpression:
		return literal(e.child)
	case bitwiseExpression:
		return literal(e.left) && (e.right == nil || literal(e.right))
	case arithmeticExpression:
		return literal(e.left) && (e.right == nil || literal(e.right))
	case likeExpression:
		return literal(e.left) && literal(e.right)
	case likeEscapeExpression:
		return literal(e.pattern) && literal(e.escape)
	case posixExpression:
		return literal(e.left) && literal(e.right)
	case similarExpression:
		return literal(e.left) && literal(e.right)
	case similarEscapeExpression:
		return literal(e.pattern) && literal(e.escape)
	case posixSubstringExpression:
		return literal(e.args[0]) && literal(e.args[1])
	case similarSubstringExpression:
		return literal(e.args[0]) && literal(e.args[1]) && literal(e.args[2])
	case rowComparisonExpression:
		for _, pair := range e.comparisons {
			if !literal(pair.left) || !literal(pair.right) {
				return false
			}
		}
		return true
	case regexpExpression:
		for _, arg := range e.args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case scalarArrayComparisonExpression:
		if !literal(e.left) || e.array != nil && !literal(e.array) {
			return false
		}
		for _, item := range e.items {
			if !literal(item) {
				return false
			}
		}
		return true
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
	case datetimeJustifyExpression:
		return literal(e.arg)
	case datetimeExtractExpression:
		return literal(e.args[0]) && literal(e.args[1])
	case datetimeOverlapsExpression:
		for _, arg := range e.args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case datetimeBinExpression:
		for _, arg := range e.args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case datetimeTruncExpression:
		for _, arg := range e.children() {
			if !literal(arg) {
				return false
			}
		}
		return true
	case datetimeZoneCalendarExpression:
		for _, arg := range e.children() {
			if !literal(arg) {
				return false
			}
		}
		return true
	case datetimeAgeFiniteExpression:
		for _, arg := range e.children() {
			if !literal(arg) {
				return false
			}
		}
		return true
	case datetimeMakeExpression:
		for _, arg := range e.children() {
			if !literal(arg) {
				return false
			}
		}
		return true
	case datetimeFormattingExpression:
		return literal(e.args[0]) && literal(e.args[1])
	case numericFormattingExpression:
		return literal(e.args[0]) && literal(e.args[1])
	case stringExpression:
		if e.plan.Name() == "string_to_table" {
			return false
		}
		for _, arg := range e.args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case arraySearchExpression:
		for _, arg := range e.args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case enumFunctionExpression:
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
	case installedFunctionExpression:
		return false // Installed unaccent is intrinsically STABLE.
	case jsonCreateExpression:
		return false // All eight JSON creation functions are intrinsically STABLE.
	case stringVariadicExpression:
		if e.intrinsic != Immutable {
			return false
		}
		for _, arg := range e.args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case formatExpression:
		return false // Intrinsically STABLE, even with literal children.
	}
	return false
}
