package engine

import (
	"stuntgres/internal/engine/functions"
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

// Searched CASE has a physical-literal decision requiring no expression,
// conversion, provider or domain evaluation. Demand and simplification share it.
func literalSearchedCaseMatch(condition BoundExpr) (known, match bool, err error) {
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
func walkDemandedBoundExpression(node BoundExpr, visit func(BoundExpr)) {
	if node == nil {
		return
	}
	visit(node)
	walk := func(child BoundExpr) { walkDemandedBoundExpression(child, visit) }
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

// simplifyConditional folds only immutable SQL constants, never runtime domain
// truth. Imported parameters remain runtime-owned even without row dependence.
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
					known, match, err = literalSearchedCaseMatch(arm.condition)
				} else if test, ok := e.test.(literalExpression); ok {
					known = true
					var view sqltype.Value
					view, err = arm.testPlan.Apply(c.Context, test.value, c.Input, c.Output)
					if err == nil && !view.IsNull() && !condition.value.IsNull() {
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
	case functions.Timezone[BoundExpr, *EvalContext, ExprProperties]:
		if randomLiteralNull(e.Value) || !e.Local && randomLiteralNull(e.Zone) {
			return literalExpression{boundExpressionInfo(e), sqltype.Null(e.Usage.Type)}, true, nil
		}
		if !e.Local {
			e.Zone, err = child(e.Zone)
		}
		if err == nil {
			e.Value, err = child(e.Value)
		}
		expression = e
	case collationExpression:
		e.child, err = child(e.child)
		expression = e
	case coercionExpression:
		e.child, err = child(e.child)
		if err == nil && e.plan.ChecksDomain() {
			err = c.prepareDomainConversion(e.child.ResultUsage().Type, e.usage.Type)
		}
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
		if err == nil && arithmeticSetConstantNull(expression.(arithmeticExpression), e) {
			return preflightValue(c, e.expressionInfo, sqltype.Null(e.usage.Type), nil)
		}
		expression = e
	case likeExpression:
		e.left, err = child(e.left)
		if err == nil {
			e.right, err = child(e.right)
		}
		expression = e
	case functions.LikeEscape[BoundExpr, *EvalContext, ExprProperties]:
		e.Pattern, err = child(e.Pattern)
		if err == nil {
			e.Escape, err = child(e.Escape)
		}
		expression = e
	case functions.Posix[BoundExpr, *EvalContext, ExprProperties]:
		e.Left, err = child(e.Left)
		if err == nil {
			e.Right, err = child(e.Right)
		}
		expression = e
	case functions.Similar[BoundExpr, *EvalContext, ExprProperties]:
		e.Left, err = child(e.Left)
		if err == nil {
			e.Right, err = child(e.Right)
		}
		expression = e
	case functions.SimilarEscape[BoundExpr, *EvalContext, ExprProperties]:
		e.Pattern, err = child(e.Pattern)
		if err == nil {
			e.Escape, err = child(e.Escape)
		}
		expression = e
	case functions.PosixSubstring[BoundExpr, *EvalContext, ExprProperties]:
		for i, arg := range e.Args {
			e.Args[i], err = child(arg)
			if err != nil {
				break
			}
		}
		expression = e
	case functions.SimilarSubstring[BoundExpr, *EvalContext, ExprProperties]:
		for i, arg := range e.Args {
			e.Args[i], err = child(arg)
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
	case functions.Regexp[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i, arg := range e.Args {
			e.Args[i], err = child(arg)
			if err != nil {
				break
			}
		}
		expression = e
	case functions.RegexpSet[BoundExpr, *EvalContext, ExprProperties]:
		e.Args = append([]BoundExpr(nil), e.Args...)
		for i, arg := range e.Args {
			e.Args[i], err = child(arg)
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
	case seriesExpression:
		return e.mapChildren(func(arg BoundExpr) (BoundExpr, bool, error) {
			return simplifyConditional(c, arg, decisiveDefaults)
		})
	case subscriptsExpression:
		return e.mapChildren(func(arg BoundExpr) (BoundExpr, bool, error) {
			return simplifyConditional(c, arg, decisiveDefaults)
		})
	case unnestExpression:
		return e.mapChildren(func(arg BoundExpr) (BoundExpr, bool, error) {
			return simplifyConditional(c, arg, decisiveDefaults)
		})
	case functions.JSONKeys[BoundExpr, *EvalContext, ExprProperties]:
		arg, failure := child(e.Args[0])
		err = failure
		if changed {
			e.Args = []BoundExpr{arg}
		}
		return e, changed, err // Cardinality belongs to its query stage.
	case functions.JSONBRecord[BoundExpr, *EvalContext, ExprProperties]:
		return e.MapChildren(func(arg BoundExpr) (BoundExpr, bool, error) { return simplifyConditional(c, arg, decisiveDefaults) })
	case functions.JSONBElements[BoundExpr, *EvalContext, ExprProperties]:
		arg, failure := child(e.Args[0])
		err = failure
		if changed {
			e.Args = []BoundExpr{arg}
		}
		return e, changed, err // Cardinality belongs to its query stage.
	case functions.JSONRecord[BoundExpr, *EvalContext, ExprProperties]:
		var args []BoundExpr
		for i, old := range e.Args {
			arg, replaced, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if replaced {
				if args == nil {
					args = append([]BoundExpr(nil), e.Args...)
				}
				args[i] = arg
				changed = true
			}
		}
		if args != nil {
			e.Args = args
		}
		return e, changed, nil
	case functions.JSONEach[BoundExpr, *EvalContext, ExprProperties]:
		arg, failure := child(e.Args[0])
		err = failure
		if changed {
			e.Args = []BoundExpr{arg}
		}
		return e, changed, err // Cardinality belongs to its query stage.
	case functions.JSONElements[BoundExpr, *EvalContext, ExprProperties]:
		arg, failure := child(e.Args[0])
		err = failure
		if changed {
			e.Args = []BoundExpr{arg}
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
	case functions.Binary[BoundExpr, *EvalContext, ExprProperties]:
		var args []BoundExpr
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.Args...)
				}
				args[i] = node
				changed = true
			}
		}
		if args != nil {
			e.Args = args
		}
		for _, arg := range e.Args {
			if randomLiteralNull(arg) {
				return literalExpression{boundExpressionInfo(e), sqltype.Null(e.Usage.Type)}, true, nil
			}
		}
		expression = e
	case functions.DatetimeJustify[BoundExpr, *EvalContext, ExprProperties]:
		node, did, failure := simplifyConditional(c, e.Arg, decisiveDefaults)
		if failure != nil {
			return nil, false, failure
		}
		if did {
			e.Arg, changed = node, true
		}
		if randomLiteralNull(e.Arg) {
			return literalExpression{boundExpressionInfo(e), sqltype.Null(e.Usage.Type)}, true, nil
		}
		expression = e
	case functions.DatetimeExtract[BoundExpr, *EvalContext, ExprProperties]:
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.Args[i], changed = node, true
			}
		}
		expression = e
	case functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties]:
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.Args[i], changed = node, true
			}
		}
		next, did, failure := simplifyOverlapsRoute(c, e)
		if failure != nil {
			return nil, false, failure
		}
		e, changed = next, changed || did
		expression = e
	case functions.DatetimeBin[BoundExpr, *EvalContext, ExprProperties]:
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.Args[i], changed = node, true
			}
		}
		for _, arg := range e.Args {
			if randomLiteralNull(arg) {
				return literalExpression{boundExpressionInfo(e), sqltype.Null(e.Usage.Type)}, true, nil
			}
		}
		expression = e
	case functions.DatetimeTrunc[BoundExpr, *EvalContext, ExprProperties]:
		for i, old := range e.Arguments() {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.Args[i], changed = node, true
			}
		}
		for _, arg := range e.Arguments() {
			if randomLiteralNull(arg) {
				return literalExpression{boundExpressionInfo(e), sqltype.Null(e.Usage.Type)}, true, nil
			}
		}
		expression = e
	case functions.DatetimeZoneCalendar[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Arguments() {
			if randomLiteralNull(arg) {
				return literalExpression{boundExpressionInfo(e), sqltype.Null(e.Usage.Type)}, true, nil
			}
		}
		for i, old := range e.Arguments() {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.Args[i], changed = node, true
			}
		}
		expression = e
	case functions.DatetimeAgeFinite[BoundExpr, *EvalContext, ExprProperties]:
		for i, old := range e.Arguments() {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.Args[i], changed = node, true
			}
		}
		for _, arg := range e.Arguments() {
			if randomLiteralNull(arg) {
				return literalExpression{boundExpressionInfo(e), sqltype.Null(e.Usage.Type)}, true, nil
			}
		}
		expression = e
	case functions.JSONStripNulls[BoundExpr, *EvalContext, ExprProperties]:
		for i, old := range e.Arguments() {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.Args[i] = node
				changed = true
			}
		}
		expression = e
	case functions.JSONExtract[BoundExpr, *EvalContext, ExprProperties]:
		next, did, failure := mapJSONExtractChildren(e, func(arg BoundExpr) (BoundExpr, bool, error) {
			return simplifyConditional(c, arg, decisiveDefaults)
		})
		if failure != nil {
			return nil, false, failure
		}
		changed = changed || did
		expression = next
	case functions.JSONBSet[BoundExpr, *EvalContext, ExprProperties]:
		for i, old := range e.Arguments() {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.Args[i] = node
				changed = true
			}
		}
		expression = e
	case functions.DatetimeMake[BoundExpr, *EvalContext, ExprProperties]:
		for i, old := range e.Arguments() {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.Args[i] = node
				changed = true
			}
		}
		for _, arg := range e.Arguments() {
			if randomLiteralNull(arg) {
				return literalExpression{boundExpressionInfo(e), sqltype.Null(e.Usage.Type)}, true, nil
			}
		}
		expression = e
	case functions.DatetimeFormatting[BoundExpr, *EvalContext, ExprProperties]:
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.Args[i] = node
				changed = true
			}
		}
		for _, arg := range e.Args {
			if randomLiteralNull(arg) {
				return literalExpression{boundExpressionInfo(e), sqltype.Null(e.Usage.Type)}, true, nil
			}
		}
		expression = e
	case functions.NumericFormatting[BoundExpr, *EvalContext, ExprProperties]:
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.Args[i] = node
				changed = true
			}
		}
		expression = e
	case functions.String[BoundExpr, *EvalContext, ExprProperties]:
		var args []BoundExpr
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.Args...)
				}
				args[i] = node
				changed = true
			}
		}
		if args != nil {
			e.Args = args
		}
		if e.Plan.Strict() {
			for _, arg := range e.Args {
				if randomLiteralNull(arg) {
					return literalExpression{boundExpressionInfo(e), sqltype.Null(e.Usage.Type)}, true, nil
				}
			}
		}
		expression = e
	case functions.ArrayRandom[BoundExpr, *EvalContext, ExprProperties]:
		var did bool
		e, did, err = mapArrayRandomChildren(e, func(old BoundExpr) (BoundExpr, bool, error) { return simplifyConditional(c, old, decisiveDefaults) })
		if err != nil {
			return nil, false, err
		}
		changed = changed || did
		expression = e
	case functions.ArrayTransform[BoundExpr, *EvalContext, ExprProperties]:
		var did bool
		e, did, err = mapArrayTransformChildren(e, func(old BoundExpr) (BoundExpr, bool, error) {
			return simplifyConditional(c, old, decisiveDefaults)
		})
		if err != nil {
			return nil, false, err
		}
		changed = changed || did
		expression = e
	case functions.ArrayInspect[BoundExpr, *EvalContext, ExprProperties]:
		var did bool
		e, did, err = mapArrayInspectChildren(e, func(old BoundExpr) (BoundExpr, bool, error) {
			return simplifyConditional(c, old, decisiveDefaults)
		})
		if err != nil {
			return nil, false, err
		}
		changed = changed || did
		expression = e
	case functions.ArraySearch[BoundExpr, *EvalContext, ExprProperties]:
		var did bool
		e, did, err = mapArraySearchChildren(e, func(old BoundExpr) (BoundExpr, bool, error) {
			return simplifyConditional(c, old, decisiveDefaults)
		})
		if err != nil {
			return nil, false, err
		}
		changed = changed || did
		expression = e
	case commitTimestampExpression:
		if e.arg != nil {
			arg, did, failure := simplifyConditional(c, e.arg, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			e.arg = arg
			changed = changed || did
		}
		expression = e
	case functions.UUIDExtract[BoundExpr, *EvalContext, ExprProperties]:
		arg, did, failure := simplifyConditional(c, e.Arg, decisiveDefaults)
		if failure != nil {
			return nil, false, failure
		}
		e.Arg = arg
		changed = changed || did
		expression = e
	case functions.RangeOperator[BoundExpr, *EvalContext, ExprProperties]:
		var did bool
		e, did, err = mapRangeOperatorChildren(e, func(old BoundExpr) (BoundExpr, bool, error) {
			return simplifyConditional(c, old, decisiveDefaults)
		})
		if err != nil {
			return nil, false, err
		}
		changed = changed || did
		expression = e
	case functions.RangeInspect[BoundExpr, *EvalContext, ExprProperties]:
		var args []BoundExpr
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.Args...)
				}
				args[i] = node
				changed = true
			}
		}
		if args != nil {
			e.Args = args
		}
		expression = e
	case functions.Enum[BoundExpr, *EvalContext, ExprProperties]:
		var args []BoundExpr
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.Args...)
				}
				args[i] = node
				changed = true
			}
		}
		if args != nil {
			e.Args = args
		}
		expression = e
	case functions.Math[BoundExpr, *EvalContext, ExprProperties]:
		var args []BoundExpr
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.Args...)
				}
				args[i] = node
				changed = true
			}
		}
		if args != nil {
			e.Args = args
		}
		if !selectedLogPowerName(e.Plan.Name()) {
			for _, arg := range e.Args {
				if randomLiteralNull(arg) {
					return literalExpression{boundExpressionInfo(e), sqltype.Null(e.Usage.Type)}, true, nil
				}
			}
		}
		expression = e
	case functions.UUIDGeneration[BoundExpr, *EvalContext, ExprProperties]:
		if len(e.Args) != 0 {
			node, did, failure := simplifyConditional(c, e.Args[0], decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.Args = []BoundExpr{node}
				changed = true
			}
			if randomLiteralNull(node) {
				// Strictness eliminates demand even for a volatile provider.
				// Preserve analyzed metadata; the surviving literal is immutable.
				return literalExpression{boundExpressionInfo(e), sqltype.Null(e.Usage.Type)}, true, nil
			}
		}
		expression = e
	case functions.Random[BoundExpr, *EvalContext, ExprProperties]:
		var args []BoundExpr
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.Args...)
				}
				args[i] = node
				changed = true
			}
		}
		if args != nil {
			e.Args = args
		}
		expression = e
	case functions.DatetimeSleep[BoundExpr, *EvalContext, ExprProperties]:
		node, did, failure := simplifyConditional(c, e.Arg, decisiveDefaults)
		if failure != nil {
			return nil, false, failure
		}
		e.Arg = node
		if randomLiteralNull(node) {
			return literalExpression{expressionInfo: expressionInfo{usage: e.Usage, pos: e.Pos}, value: sqltype.Null(e.Usage.Type)}, true, nil
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
	case objectAddressExpression:
		e.properties = ExprProperties{Volatility: Stable}
		for i, arg := range e.args {
			node, did, failure := simplifyConditional(c, arg, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.args[i], changed = node, true
			}
			e.properties = mergeExpressionProperties(e.properties, e.args[i].Properties())
		}
		// PostgreSQL eliminates strict scalar calls with a constant NULL,
		// but leaves RECORD calls and nonconstant NULL inputs intact.
		// Simplify every child first so reached planning errors retain precedence.
		if e.op == describeObject {
			for _, arg := range e.args {
				if randomLiteralNull(arg) {
					return literalExpression{boundExpressionInfo(e), sqltype.Null(e.usage.Type)}, true, nil
				}
			}
		}
		expression = e
	case aclValueExpression:
		e.properties = e.baseProperties()
		for i, arg := range e.arguments() {
			node, did, failure := simplifyConditional(c, arg, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.args[i], changed = node, true
			}
			e.properties = mergeExpressionProperties(e.properties, e.args[i].Properties())
		}
		expression = e
	case objectACLExpression:
		e.properties = ExprProperties{Volatility: Stable}
		for i, arg := range e.args {
			node, did, failure := simplifyConditional(c, arg, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.args[i], changed = node, true
			}
			e.properties = mergeExpressionProperties(e.properties, e.args[i].Properties())
		}
		expression = e
	case accessInquiryExpression:
		e.properties = ExprProperties{Volatility: Stable}
		for i, arg := range e.arguments() {
			node, did, failure := simplifyConditional(c, arg, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.args[i], changed = node, true
			}
			e.properties = mergeExpressionProperties(e.properties, e.args[i].Properties())
		}
		expression = e
	case namespaceInquiryExpression:
		if e.arg != nil {
			node, did, failure := simplifyConditional(c, e.arg, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				e.arg, changed = node, true
			}
		}
		expression = e
	case blockingInquiryExpression:
		node, did, failure := simplifyConditional(c, e.arg, decisiveDefaults)
		if failure != nil {
			return nil, false, failure
		}
		if did {
			e.arg, changed = node, true
		}
		e.properties = e.arg.Properties()
		e.properties.Volatility = Volatile
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
	case functions.NullCount[BoundExpr, *EvalContext, ExprProperties]:
		var args []BoundExpr
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.Args...)
				}
				args[i] = node
				changed = true
			}
		}
		if args != nil {
			e.Args = args
		}
		expression = e
	case installedSoundexFunctionExpression:
		var args []BoundExpr
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.Args...)
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
		var args []BoundExpr
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.Args...)
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
		var args []BoundExpr
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.Args...)
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
	case installedCryptoFunctionExpression:
		var args []BoundExpr
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.Args...)
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
		next, did, failure := simplifyConditional(c, e.arg, decisiveDefaults)
		if failure != nil {
			return nil, false, failure
		}
		e.arg = next
		changed = changed || did
		expression = e
	case sqlJSONExpression:
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
				args[i], changed = node, true
			}
		}
		if args != nil {
			next, failure := e.reconstruct(e.expressionInfo, args)
			if failure != nil {
				return nil, false, failure
			}
			expression = next
		}
	case jsonPredicateExpression:
		next, did, failure := simplifyConditional(c, e.child, decisiveDefaults)
		if failure != nil {
			return nil, false, failure
		}
		e.child = next
		changed = changed || did
		expression = e
	case functions.JSONCreate[BoundExpr, *EvalContext, ExprProperties]:
		var args []BoundExpr
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.Args...)
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
			e = next
		}
		expression = e
	case functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin]:
		var args []BoundExpr
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.Args...)
				}
				args[i] = node
				changed = true
			}
		}
		if args != nil {
			e.Args = args
		}
		node, folded, failure := foldStringVariadic(c, e)
		return node, changed || folded, failure
	case functions.Format[BoundExpr, *EvalContext, ExprProperties]:
		var args []BoundExpr
		for i, old := range e.Args {
			node, did, failure := simplifyConditional(c, old, decisiveDefaults)
			if failure != nil {
				return nil, false, failure
			}
			if did {
				if args == nil {
					args = append([]BoundExpr(nil), e.Args...)
				}
				args[i] = node
				changed = true
			}
		}
		if args != nil {
			e.Args = args
		}
		expression = e
	}
	if err != nil {
		return nil, false, err
	}
	p := expression.Properties()
	if _, literal := expression.(literalExpression); !literal && p.Volatility == Immutable && !p.RuntimeDomainConversion && conditionalConstantInputs(expression) {
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

// Analysis properties retain dead-arm dependence; literal children after
// pruning identify ordinary immutable constants. A runtime domain barrier is
// different: its literal physical child has not consumed CHECK truth. Neither
// checked conversions nor enclosing barriers belong in this foldable set.
func conditionalConstantInputs(node BoundExpr) bool {
	if node.Properties().RuntimeDomainConversion {
		return false
	}
	literal := func(child BoundExpr) bool { _, ok := child.(literalExpression); return ok }
	switch e := node.(type) {
	case functions.Timezone[BoundExpr, *EvalContext, ExprProperties]:
		return literal(e.Value) && (e.Local || literal(e.Zone))
	case collationExpression:
		return literal(e.child)
	case coercionExpression:
		return !e.plan.ChecksDomain() && literal(e.child)
	case bitwiseExpression:
		return literal(e.left) && (e.right == nil || literal(e.right))
	case arithmeticExpression:
		return literal(e.left) && (e.right == nil || literal(e.right))
	case likeExpression:
		return literal(e.left) && literal(e.right)
	case functions.LikeEscape[BoundExpr, *EvalContext, ExprProperties]:
		return literal(e.Pattern) && literal(e.Escape)
	case functions.Posix[BoundExpr, *EvalContext, ExprProperties]:
		return literal(e.Left) && literal(e.Right)
	case functions.Similar[BoundExpr, *EvalContext, ExprProperties]:
		return literal(e.Left) && literal(e.Right)
	case functions.SimilarEscape[BoundExpr, *EvalContext, ExprProperties]:
		return literal(e.Pattern) && literal(e.Escape)
	case functions.PosixSubstring[BoundExpr, *EvalContext, ExprProperties]:
		return literal(e.Args[0]) && literal(e.Args[1])
	case functions.SimilarSubstring[BoundExpr, *EvalContext, ExprProperties]:
		return literal(e.Args[0]) && literal(e.Args[1]) && literal(e.Args[2])
	case rowComparisonExpression:
		for _, pair := range e.comparisons {
			if !literal(pair.left) || !literal(pair.right) {
				return false
			}
		}
		return true
	case functions.Regexp[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
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
	case functions.Binary[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case functions.DatetimeJustify[BoundExpr, *EvalContext, ExprProperties]:
		return literal(e.Arg)
	case functions.DatetimeExtract[BoundExpr, *EvalContext, ExprProperties]:
		return literal(e.Args[0]) && literal(e.Args[1])
	case functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case functions.DatetimeBin[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case functions.DatetimeTrunc[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Arguments() {
			if !literal(arg) {
				return false
			}
		}
		return true
	case functions.DatetimeZoneCalendar[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Arguments() {
			if !literal(arg) {
				return false
			}
		}
		return true
	case functions.DatetimeAgeFinite[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Arguments() {
			if !literal(arg) {
				return false
			}
		}
		return true
	case functions.JSONStripNulls[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Arguments() {
			if !literal(arg) {
				return false
			}
		}
		return true
	case functions.JSONExtract[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case functions.JSONBSet[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Arguments() {
			if !literal(arg) {
				return false
			}
		}
		return true
	case functions.DatetimeMake[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Arguments() {
			if !literal(arg) {
				return false
			}
		}
		return true
	case functions.DatetimeFormatting[BoundExpr, *EvalContext, ExprProperties]:
		return literal(e.Args[0]) && literal(e.Args[1])
	case functions.NumericFormatting[BoundExpr, *EvalContext, ExprProperties]:
		return literal(e.Args[0]) && literal(e.Args[1])
	case functions.String[BoundExpr, *EvalContext, ExprProperties]:
		if e.Plan.Name() == "string_to_table" {
			return false
		}
		for _, arg := range e.Args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case functions.ArrayRandom[BoundExpr, *EvalContext, ExprProperties]:
		return false // Volatile even for strict NULL, empty and singleton arrays.
	case functions.ArrayTransform[BoundExpr, *EvalContext, ExprProperties]:
		if e.IntrinsicStable() {
			return false
		}
		for _, arg := range e.Args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case functions.ArrayInspect[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case functions.ArraySearch[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case aclValueExpression:
		// Keep constructors symbolic: their inert identities need the statement
		// authority, and stored definitions must retain their child dependencies.
		return false
	case commitTimestampExpression, objectAddressExpression:
		return false
	case functions.UUIDExtract[BoundExpr, *EvalContext, ExprProperties]:
		return literal(e.Arg)
	case functions.RangeOperator[BoundExpr, *EvalContext, ExprProperties]:
		return literal(e.Args[0]) && literal(e.Args[1])
	case functions.RangeInspect[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case functions.Enum[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case functions.Math[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case functions.NullCount[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			if !literal(arg) {
				return false
			}
		}
		return true
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
	case installedCryptoFunctionExpression:
		for _, arg := range e.Args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case installedFunctionExpression:
		return e.procedure != nil && e.procedure.volatility == Immutable && literal(e.arg)
	case sqlJSONExpression:
		if e.properties.Volatility != Immutable {
			return false
		}
		for _, arg := range e.args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case jsonPredicateExpression:
		return literal(e.child)
	case functions.JSONCreate[BoundExpr, *EvalContext, ExprProperties]:
		return false // All eight JSON creation functions are intrinsically STABLE.
	case functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin]:
		if e.IntrinsicStable() {
			return false
		}
		for _, arg := range e.Args {
			if !literal(arg) {
				return false
			}
		}
		return true
	case functions.Format[BoundExpr, *EvalContext, ExprProperties]:
		return false // Intrinsically STABLE, even with literal children.
	}
	return false
}
