package exec

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Child preflight has already preserved ordinary constant errors and parameter
// input validation. Literal NULL and typed parameter NULL share this post-child
// proof. It cannot cross a domain conversion in that argument, but a separate
// known-NULL argument can discard runtime siblings, including domain coercions.
// Random first shares its required immutable-input readiness/admission decision
// with both composite planning paths; the proof itself samples no domain truth
// or runtime-only provider. The analyzed tree and its original manifest remain
// untouched.
func FoldStrictParameterNull(c *Context, node expr.Expr) (expr.Expr, bool, error) {
	return foldStrictParameterNull(c, node, nil)
}

// planChild completes ordinary child planning when the caller has only
// preflighted conversions. Definition eligibility supplies no execution frame,
// so its NULL proof does not compute children. Conditional planning leaves the
// callback nil because it has already planned them.
func foldStrictParameterNull(c *Context, node expr.Expr, planChild func(expr.Expr) error) (expr.Expr, bool, error) {
	if folded, changed, handled, err := foldRandomNullInputs(c, node); handled {
		return folded, changed, err
	}
	if node == nil {
		return node, false, nil
	}
	switch node.(type) {
	case functions.ReplicationSlot, plan.LiteralExpr, plan.DistinctExpr, plan.ConditionalExpr, plan.ScalarArrayComparisonExpr, plan.RowComparisonExpr:
		// Slot calls retain EXECUTE admission even for strict NULL inputs.
		// The other carriers can name an internal strict comparison without
		// making the whole NULL-aware or multi-comparison expression strict.
		return node, false, nil
	}
	selected, err := ExpressionRoutineDescriptor(node)
	if err != nil {
		return nil, false, err
	}
	if !StrictScalarRoutine(selected) {
		return node, false, nil
	}
	null := false
	var failure error
	StrictRoutineArguments(node, func(argument expr.Expr) {
		if null || failure != nil {
			return
		}
		null, failure = ExecutionKnownNullArgument(c, argument)
	})
	if failure != nil {
		return nil, false, failure
	}
	if !null {
		return node, false, nil
	}
	if planChild != nil {
		plan.ExpressionChildren(node, func(argument expr.Expr) {
			if failure == nil {
				failure = planChild(argument)
			}
		})
		if failure != nil {
			return nil, false, failure
		}
	}
	// A custom-plan parameter NULL must not erase an independently demanded
	// arithmetic error in another supplied parameter expression. Ordinary child
	// planning leaves these expressions parameter-dependent; complete only this
	// narrow immutable available-frame frontier before discarding the call.
	if err := planStrictNullArithmetic(c, node); err != nil {
		return nil, false, err
	}
	info := foldedExpressionInfo(node)
	return plan.LiteralExpr{Info: info, Value: sqltype.ResultNull(info.Usage)}, true, nil
}

func planStrictNullArithmetic(c *Context, node expr.Expr) error {
	// Definition eligibility has no execution frame and must never compute.
	if c == nil || c.Context == nil || c.PreserveParameterDemand || len(c.Parameters) == 0 {
		return nil
	}
	var failure error
	plan.ExpressionChildren(node, func(child expr.Expr) {
		if failure != nil || child == nil || !child.Properties().ParameterDependent || !OperatorExecutionConstant(child, c) {
			return
		}
		arithmetic, ready := strictNullArithmeticTree(child, c)
		if arithmetic && ready {
			_, failure = FoldPlanningExpression(c, child)
		}
	})
	return failure
}

// Availability alone must not admit arbitrary immutable functions. Only typed
// arithmetic, its selected operator/coercion wrappers and owned frame leaves
// enter this demand point; row, stage, domain and volatile providers stay out.
func strictNullArithmeticTree(node expr.Expr, c *Context) (arithmetic, ready bool) {
	switch n := node.(type) {
	case plan.LiteralExpr:
		return false, true
	case plan.ParameterExpr:
		return false, n.Number > 0 && n.Number <= len(c.Parameters) && c.Parameters[n.Number-1].Type() != nil
	case plan.CollationExpr:
		return strictNullArithmeticTree(n.Child, c)
	case plan.CoercionExpr:
		if n.Usage.Type == nil || n.Usage.Type.IsDomain() {
			return false, false
		}
		return strictNullArithmeticTree(n.Child, c)
	case plan.SelectedOperatorExpr:
		return strictNullArithmeticTree(n.Provider, c)
	case plan.ArithmeticExpr:
		_, left := strictNullArithmeticTree(n.Left, c)
		right := true
		if n.Right != nil {
			_, right = strictNullArithmeticTree(n.Right, c)
		}
		return true, left && right
	}
	return false, false
}

// ExecutionKnownNullArgument reports whether node is known NULL from a
// literal or the typed parameter frame alone, never by evaluating an
// operator, function or domain.
func ExecutionKnownNullArgument(c *Context, node expr.Expr) (bool, error) {
	if node == nil || node.Properties().RuntimeDomainConversion ||
		c != nil && c.PreserveParameterDemand && node.Properties().ParameterDependent {
		return false, nil
	}
	switch e := node.(type) {
	case plan.LiteralExpr:
		return e.Value.IsNull(), nil
	case plan.ParameterExpr:
		// Description and partially imported frames do not supply a value.
		if e.Number <= 0 || e.Number > len(c.Parameters) || c.Parameters[e.Number-1].Type() == nil {
			return false, nil
		}
		value, err := e.Eval(c) // Typed frame access only; no operator/function/domain Eval.
		return value.IsNull(), err
	case plan.CollationExpr:
		return ExecutionKnownNullArgument(c, e.Child)
	case plan.CoercionExpr:
		if e.Usage.Type == nil || e.Usage.Type.IsDomain() {
			return false, nil
		}
		return ExecutionKnownNullArgument(c, e.Child)
	case plan.CaseExpr:
		// Existing conditional planning preserves conservative properties on
		// this already-decided native CASE carrier, rather than adding a cast
		// or substituting parameters throughout the analyzed expression.
		if e.Test == nil && len(e.Arms) == 0 {
			return ExecutionKnownNullArgument(c, e.Otherwise)
		}
	}
	return false, nil
}
