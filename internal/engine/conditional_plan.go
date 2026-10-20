package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
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
	if literal.Value.IsNull() {
		return true, false, nil
	}
	match, err = literal.Value.Boolean()
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
		if e.Test != nil {
			boundExpressionChildren(node, walk)
			return
		}
		for _, arm := range e.Arms {
			walk(arm.Condition)
			known, match, err := literalSearchedCaseMatch(arm.Condition)
			if err != nil || !known {
				walk(arm.Result)
			} else if match {
				walk(arm.Result)
				return
			}
		}
		walk(e.Otherwise)
	case conditionalExpression:
		if e.Kind != parser.Coalesce {
			boundExpressionChildren(node, walk)
			return
		}
		for _, arg := range e.Args {
			walk(arg)
			if literal, ok := arg.(literalExpression); ok && !literal.Value.IsNull() {
				return
			}
		}
	case logicalExpression:
		walk(e.Left)
		if _, decisive, err := plan.DecisiveLogicalValue(&e); err != nil || !decisive {
			walk(e.Right)
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
		e.Base, err = child(e.Base)
		if err != nil {
			return nil, false, err
		}
		// Simplify every constructor child before narrowing physical demand:
		// unused immutable failures still precede selected-field execution.
		// Only genuine ROW constructors can discard their other children;
		// source rows retain occurrence absence and transition semantics.
		if row, ok := e.Base.(rowExpression); ok && row.Raw != nil && e.Index >= 0 && e.Index < len(row.Fields) {
			shape := row.ResultUsage().Type.BaseType()
			index, valid := shape.CompositeAttributeIndex(e.Attribute)
			source := row.FieldSourceIndex(index)
			if valid && source == e.Index && shape.CompositeMember(index).Usage == e.ResultUsage() &&
				row.Fields[source].ResultUsage() == e.ResultUsage() {
				return plannedConditionalResult(e.Info, row.Fields[source]), true, nil
			}
		}
		expression = e
	case arrayExpression:
		var elements []expr.Expr
		for i, old := range e.Elements {
			next, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				changed = true
				if elements == nil {
					elements = append([]expr.Expr(nil), e.Elements...)
				}
				elements[i] = next
			}
		}
		if elements != nil {
			e.Elements = elements
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
		if e.Test != nil {
			e.Test, err = child(e.Test)
			if err != nil {
				return nil, false, err
			}
		}
		var arms []caseBranch
		for i, old := range e.Arms {
			arm := old
			arm.Condition, err = child(arm.Condition)
			if err != nil {
				return nil, false, err
			}
			known, match := false, false
			if condition, ok := arm.Condition.(literalExpression); ok {
				if e.Test == nil {
					known, match, err = literalSearchedCaseMatch(arm.Condition)
				} else if test, ok := e.Test.(literalExpression); ok {
					known = true
					var view sqltype.Value
					view, err = arm.TestPlan.Apply(c.Context, test.Value, c.Input, c.Output)
					if err == nil && !view.IsNull() && !condition.Value.IsNull() {
						if err := admitCaseEquality(c, arm); err != nil {
							return nil, false, err
						}
						match, err = arm.Equality.EqualValues(c.Context, view, condition.Value, c.Input)
					}
				}
				if err != nil {
					return nil, false, err
				}
			}
			if known && !match {
				if arms == nil {
					arms = append([]caseBranch{}, e.Arms[:i]...)
				}
				changed = true
				continue
			}
			arm.Result, err = child(arm.Result)
			if err != nil {
				return nil, false, err
			}
			if known && match {
				if arms == nil && i == 0 {
					return plannedConditionalResult(e.Info, arm.Result), true, nil
				}
				if arms == nil {
					arms = append([]caseBranch{}, e.Arms[:i]...)
				}
				e.Arms = arms
				e.Otherwise = arm.Result
				return e, true, nil
			}
			if changed && arms == nil {
				arms = append([]caseBranch{}, e.Arms[:i]...)
			}
			if arms != nil {
				arms = append(arms, arm)
			}
		}
		if arms != nil {
			e.Arms = arms
		}
		e.Otherwise, err = child(e.Otherwise)
		if err != nil {
			return nil, false, err
		}
		if len(e.Arms) == 0 {
			return plannedConditionalResult(e.Info, e.Otherwise), true, nil
		}
		if changed {
			return e, true, nil
		}
		return expression, false, nil
	case conditionalExpression:
		if err := e.ExtremaError(); err != nil {
			return nil, false, err
		}
		var args []expr.Expr
		for i, old := range e.Args {
			node, err := child(old)
			if err != nil {
				return nil, false, err
			}
			if e.Kind == parser.Coalesce {
				if literal, ok := node.(literalExpression); ok {
					if literal.Value.IsNull() {
						if args == nil {
							args = append([]expr.Expr{}, e.Args[:i]...)
						}
						changed = true
						continue
					}
					if args == nil && i == 0 {
						return plannedConditionalResult(e.Info, literal), true, nil
					}
					if args == nil {
						args = append([]expr.Expr{}, e.Args[:i]...)
					}
					e.Args = append(args, node)
					return e, true, nil
				}
			}
			if changed && args == nil {
				args = append([]expr.Expr{}, e.Args[:i]...)
			}
			if args != nil {
				args = append(args, node)
			}
		}
		if args != nil {
			e.Args = args
		}
		if len(e.Args) == 0 {
			return literalExpression{e.Info, sqltype.Null(e.Usage.Type)}, true, nil
		}
		expression = e
	case collationExpression:
		e.Child, err = child(e.Child)
		expression = e
	case coercionExpression:
		e.Child, err = child(e.Child)
		if err == nil && e.Plan.ChecksDomain() {
			err = c.prepareDomainConversion(e.Child.ResultUsage().Type, e.Usage.Type)
		}
		expression = e
	case bitwiseExpression:
		e.Left, err = child(e.Left)
		if err == nil {
			e.Right, err = child(e.Right)
		}
		if err == nil && (bitwiseConstantNull(e.Left) || bitwiseConstantNull(e.Right)) {
			return preflightValue(c, e.Info, sqltype.Null(e.Usage.Type), nil)
		}
		expression = e
	case selectedOperatorExpression:
		e.Provider, err = child(e.Provider)
		expression = e
	case arithmeticExpression:
		e.Left, err = child(e.Left)
		if err == nil {
			e.Right, err = child(e.Right)
		}
		if err == nil && arithmeticSetConstantNull(expression.(arithmeticExpression), e) {
			return preflightValue(c, e.Info, sqltype.Null(e.Usage.Type), nil)
		}
		expression = e
	case likeExpression:
		e.Left, err = child(e.Left)
		if err == nil {
			e.Right, err = child(e.Right)
		}
		expression = e
	case rowComparisonExpression:
		e.Comparisons = append([]comparisonExpression(nil), e.Comparisons...)
		for i, pair := range e.Comparisons {
			pair.Left, err = child(pair.Left)
			if err == nil {
				pair.Right, err = child(pair.Right)
			}
			if err != nil {
				break
			}
			e.Comparisons[i] = pair
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
		e.Left, err = child(e.Left)
		if err == nil {
			e.Right, err = child(e.Right)
		}
		expression = e
	case logicalExpression:
		if decisiveDefaults {
			// Reuse the existing consumed-default AND/OR policy before
			// visiting a right child that a decisive left constant excludes.
			return preflightCoercionsWithPolicy(c, e, true)
		}
		e.Left, err = child(e.Left)
		if err == nil {
			e.Right, err = child(e.Right)
		}
		expression = e
	case nullTestExpression:
		e.Child, err = child(e.Child)
		expression = e
	case truthTestExpression:
		e.Child, err = child(e.Child)
		expression = e
	case distinctExpression:
		e.Left, err = child(e.Left)
		if err == nil {
			e.Right, err = child(e.Right)
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
	return caseExpression{Info: info, Otherwise: node}
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
		return literal(e.Child)
	case coercionExpression:
		return !e.Plan.ChecksDomain() && literal(e.Child)
	case bitwiseExpression:
		return literal(e.Left) && (e.Right == nil || literal(e.Right))
	case selectedOperatorExpression:
		return conditionalConstantInputs(e.Provider)
	case arithmeticExpression:
		return literal(e.Left) && (e.Right == nil || literal(e.Right))
	case likeExpression:
		return literal(e.Left) && literal(e.Right)
	case rowComparisonExpression:
		for _, pair := range e.Comparisons {
			if !literal(pair.Left) || !literal(pair.Right) {
				return false
			}
		}
		return true
	case scalarArrayComparisonExpression:
		if !literal(e.Left) || e.Array != nil && !literal(e.Array) {
			return false
		}
		for _, item := range e.Items {
			if !literal(item) {
				return false
			}
		}
		return true
	case comparisonExpression:
		return literal(e.Left) && literal(e.Right)
	case logicalExpression:
		return literal(e.Left) && (e.Right == nil || literal(e.Right))
	case nullTestExpression:
		return literal(e.Child)
	case truthTestExpression:
		return literal(e.Child)
	case distinctExpression:
		return literal(e.Left) && literal(e.Right)
	case conditionalExpression:
		for _, arg := range e.Args {
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
