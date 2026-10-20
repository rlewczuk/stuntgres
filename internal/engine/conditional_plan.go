package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Searched CASE has a physical-literal decision requiring no expression,
// conversion, provider or domain evaluation. Demand and simplification share it.
func literalSearchedCaseMatch(condition expr.Expr) (known, match bool, err error) {
	literal, ok := condition.(literalExpression)
	if !ok {
		return false, false, nil
	}
	if literal.value.IsNull() {
		return true, false, nil
	}
	match, err = literal.value.Boolean()
	return err == nil, match, err
}

// This walk discovers payload reads without advancing any scalar preflight
// frontier. Unknown branches retain conservative demand; only the canonical
// physical literal policies exclude children.
func walkDemandedBoundExpression(node expr.Expr, visit func(expr.Expr)) {
	if node == nil {
		return
	}
	visit(node)
	walk := func(child expr.Expr) { walkDemandedBoundExpression(child, visit) }
	switch e := node.(type) {
	case caseExpression:
		if e.test != nil {
			boundExpressionChildren(node, walk)
			return
		}
		for _, arm := range e.arms {
			walk(arm.condition)
			known, match, err := literalSearchedCaseMatch(arm.condition)
			if err != nil || !known {
				walk(arm.result)
			} else if match {
				walk(arm.result)
				return
			}
		}
		walk(e.otherwise)
	case conditionalExpression:
		if e.kind != parser.Coalesce {
			boundExpressionChildren(node, walk)
			return
		}
		for _, arg := range e.args {
			walk(arg)
			if literal, ok := arg.(literalExpression); ok && !literal.value.IsNull() {
				return
			}
		}
	case logicalExpression:
		walk(e.left)
		if _, decisive, err := decisiveLogicalValue(&e); err != nil || !decisive {
			walk(e.right)
		}
	default:
		boundExpressionChildren(node, walk)
	}
}

// Conditional planning folds immutable SQL constants; the common post-child
// strict-NULL rule can additionally eliminate a call using an execution-known
// parameter. It neither substitutes parameters generally nor runs domain truth.
func simplifyConditional(c *EvalContext, expression expr.Expr, decisiveDefaults bool) (expr.Expr, bool, error) {
	node, changed, err := simplifyConditionalNode(c, expression, decisiveDefaults)
	if err != nil {
		return nil, false, err
	}
	node, folded, err := foldStrictParameterNull(c, node)
	return node, changed || folded, err
}

func simplifyConditionalNode(c *EvalContext, expression expr.Expr, decisiveDefaults bool) (expr.Expr, bool, error) {
	if expression == nil {
		return nil, false, nil
	}
	if err := evaluationContext(c); err != nil {
		return nil, false, err
	}
	recipe, recipeErr := routine.ExpressionRecipeDescriptor(expression)
	if recipeErr != nil {
		return nil, false, recipeErr
	}
	if recipe != nil && (!c.routineGraft || c.routineConsumer != recipe) || recipe == nil && c.routineGraft {
		parent := c
		local := *c
		if err := local.selectExpressionRecipe(recipe); err != nil {
			return nil, false, err
		}
		c = &local
		defer func() { parent.preparedDomainChecks = c.preparedDomainChecks }()
	}
	changed := false
	child := func(old expr.Expr) (expr.Expr, error) {
		node, did, err := simplifyConditional(c, old, decisiveDefaults)
		changed = changed || did
		return node, err
	}
	var err error
	switch e := expression.(type) {
	case rowExpression:
		var did bool
		e, did, err = mapRow(e, func(old expr.Expr) (expr.Expr, bool, error) { return simplifyConditional(c, old, decisiveDefaults) })
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
			source := row.fieldSourceIndex(index)
			if valid && source == e.index && shape.CompositeMember(index).Usage == e.ResultUsage() &&
				row.fields[source].ResultUsage() == e.ResultUsage() {
				return plannedConditionalResult(e.Info, row.fields[source]), true, nil
			}
		}
		expression = e
	case arrayExpression:
		var elements []expr.Expr
		for i, old := range e.elements {
			next, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				changed = true
				if elements == nil {
					elements = append([]expr.Expr(nil), e.elements...)
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
		e, did, err = mapSubscript(e, func(old expr.Expr) (expr.Expr, bool, error) { return simplifyConditional(c, old, decisiveDefaults) })
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
					known, match, err = literalSearchedCaseMatch(arm.condition)
				} else if test, ok := e.test.(literalExpression); ok {
					known = true
					var view sqltype.Value
					view, err = arm.testPlan.Apply(c.Context, test.value, c.Input, c.Output)
					if err == nil && !view.IsNull() && !condition.value.IsNull() {
						if err := admitCaseEquality(c, arm); err != nil {
							return nil, false, err
						}
						match, err = arm.equality.EqualValues(c.Context, view, condition.value, c.Input)
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
					return plannedConditionalResult(e.Info, arm.result), true, nil
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
			return plannedConditionalResult(e.Info, e.otherwise), true, nil
		}
		if changed {
			return e, true, nil
		}
		return expression, false, nil
	case conditionalExpression:
		if err := e.extremaError(); err != nil {
			return nil, false, err
		}
		var args []expr.Expr
		for i, old := range e.args {
			node, err := child(old)
			if err != nil {
				return nil, false, err
			}
			if e.kind == parser.Coalesce {
				if literal, ok := node.(literalExpression); ok {
					if literal.value.IsNull() {
						if args == nil {
							args = append([]expr.Expr{}, e.args[:i]...)
						}
						changed = true
						continue
					}
					if args == nil && i == 0 {
						return plannedConditionalResult(e.Info, literal), true, nil
					}
					if args == nil {
						args = append([]expr.Expr{}, e.args[:i]...)
					}
					e.args = append(args, node)
					return e, true, nil
				}
			}
			if changed && args == nil {
				args = append([]expr.Expr{}, e.args[:i]...)
			}
			if args != nil {
				args = append(args, node)
			}
		}
		if args != nil {
			e.args = args
		}
		if len(e.args) == 0 {
			return literalExpression{e.Info, sqltype.Null(e.Usage.Type)}, true, nil
		}
		expression = e
	case collationExpression:
		e.child, err = child(e.child)
		expression = e
	case coercionExpression:
		e.child, err = child(e.child)
		if err == nil && e.plan.ChecksDomain() {
			err = c.prepareDomainConversion(e.child.ResultUsage().Type, e.Usage.Type)
		}
		expression = e
	case bitwiseExpression:
		e.left, err = child(e.left)
		if err == nil {
			e.right, err = child(e.right)
		}
		if err == nil && (bitwiseConstantNull(e.left) || bitwiseConstantNull(e.right)) {
			return preflightValue(c, e.Info, sqltype.Null(e.Usage.Type), nil)
		}
		expression = e
	case selectedOperatorExpression:
		e.provider, err = child(e.provider)
		expression = e
	case arithmeticExpression:
		e.left, err = child(e.left)
		if err == nil {
			e.right, err = child(e.right)
		}
		if err == nil && arithmeticSetConstantNull(expression.(arithmeticExpression), e) {
			return preflightValue(c, e.Info, sqltype.Null(e.Usage.Type), nil)
		}
		expression = e
	case likeExpression:
		e.left, err = child(e.left)
		if err == nil {
			e.right, err = child(e.right)
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
	case scalarArrayComparisonExpression:
		var replaced bool
		e, replaced, err = mapScalarArrayChildren(e, func(old expr.Expr) (expr.Expr, bool, error) {
			return simplifyConditional(c, old, decisiveDefaults)
		})
		changed = changed || replaced
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
	case extensionPathsExpression:
		arg, did, failure := simplifyConditional(c, e.arg, decisiveDefaults)
		if failure != nil {
			return nil, false, failure
		}
		e.arg = arg
		changed = changed || did
		expression = e
	case installedSoundexFunctionExpression:
		var args []expr.Expr
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]expr.Expr(nil), e.Args...)
				}
				args[i], changed = node, true
			}
		}
		if args != nil {
			next, failure := e.Reconstruct(e.Info, args)
			if failure != nil {
				return nil, false, failure
			}
			e.Soundex = next
		}
		expression = e
	case installedMetaphoneFunctionExpression:
		var args []expr.Expr
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]expr.Expr(nil), e.Args...)
				}
				args[i], changed = node, true
			}
		}
		if args != nil {
			next, failure := e.Reconstruct(e.Info, args)
			if failure != nil {
				return nil, false, failure
			}
			e.Metaphone = next
		}
		expression = e
	case installedLevenshteinFunctionExpression:
		var args []expr.Expr
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]expr.Expr(nil), e.Args...)
				}
				args[i], changed = node, true
			}
		}
		if args != nil {
			next, failure := e.Reconstruct(e.Info, args)
			if failure != nil {
				return nil, false, failure
			}
			e.Levenshtein = next
		}
		expression = e
	case installedUUIDOsspFunctionExpression:
		expression = e
	case installedCryptoFunctionExpression:
		var args []expr.Expr
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]expr.Expr(nil), e.Args...)
				}
				args[i] = node
				changed = true
			}
		}
		if args != nil {
			next, failure := e.Reconstruct(e.Info, args)
			if failure != nil {
				return nil, false, failure
			}
			e.Pgcrypto = next
		}
		expression = e
	case installedFunctionExpression:
		next, did, failure := simplifyConditional(c, e.Arg, decisiveDefaults)
		if failure != nil {
			return nil, false, failure
		}
		e.Arg = next
		changed = changed || did
		expression = e
	case expr.Composite:
		// Last, so the explicit engine wrappers above keep precedence.
		next, did, done, failure := simplifyCompositeNode(c, e, decisiveDefaults)
		if failure != nil {
			return nil, false, failure
		}
		if done {
			return next, changed || did, nil
		}
		changed = changed || did
		expression = next
	}
	if err != nil {
		return nil, false, err
	}
	p := expression.Properties()
	if _, literal := expression.(literalExpression); !literal && p.Volatility == expr.Immutable && !p.RuntimeDomainConversion && conditionalConstantInputs(expression) {
		value, err := evaluatePlanningExpression(c, expression)
		if err != nil {
			return nil, false, err
		}
		return literalExpression{foldedExpressionInfo(expression), value}, true, nil
	}
	return expression, changed, nil
}

// Keep the analyzed usage, position and conservative dependence even when a
// decisive constant eliminates runtime branches. Nonliteral selection uses the
// existing CASE control node with no tests, not a second evaluator or a cast.
func plannedConditionalResult(info expr.Info, node expr.Expr) expr.Expr {
	if literal, ok := node.(literalExpression); ok {
		literal.Info = info
		return literal
	}
	return caseExpression{Info: info, otherwise: node}
}

// Analysis properties retain dead-arm dependence; literal children after
// pruning identify ordinary immutable constants. A runtime domain barrier is
// different: its literal physical child has not consumed CHECK truth. Neither
// checked conversions nor enclosing barriers belong in this foldable set.
func conditionalConstantInputs(node expr.Expr) bool {
	if node.Properties().RuntimeDomainConversion {
		return false
	}
	literal := func(child expr.Expr) bool { _, ok := child.(literalExpression); return ok }
	switch e := node.(type) {
	case collationExpression:
		return literal(e.child)
	case coercionExpression:
		return !e.plan.ChecksDomain() && literal(e.child)
	case bitwiseExpression:
		return literal(e.left) && (e.right == nil || literal(e.right))
	case selectedOperatorExpression:
		return conditionalConstantInputs(e.provider)
	case arithmeticExpression:
		return literal(e.left) && (e.right == nil || literal(e.right))
	case likeExpression:
		return literal(e.left) && literal(e.right)
	case rowComparisonExpression:
		for _, pair := range e.comparisons {
			if !literal(pair.left) || !literal(pair.right) {
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
	case extensionPathsExpression:
		return false
	case installedSoundexFunctionExpression:
		for _, arg := range e.Args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case installedMetaphoneFunctionExpression:
		for _, arg := range e.Args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case installedLevenshteinFunctionExpression:
		for _, arg := range e.Args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case installedUUIDOsspFunctionExpression:
		return e.procedure != nil && e.procedure.Volatility == expr.Immutable
	case installedCryptoFunctionExpression:
		for _, arg := range e.Args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case installedFunctionExpression:
		return e.procedure != nil && e.procedure.Volatility == expr.Immutable && literal(e.Arg)
	case expr.Composite:
		// Last, so the explicit engine wrappers above keep precedence.
		return compositeConstantInputs(e)
	}
	return false
}
