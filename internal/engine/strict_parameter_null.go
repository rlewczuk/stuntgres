package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Child preflight has already preserved ordinary constant errors and parameter
// input validation. A NULL proof cannot cross a domain conversion in that
// argument, but a separate known-NULL argument can discard runtime siblings,
// including domain coercions. No domain truth or provider is sampled here.
// The analyzed tree and its original dependency manifest remain untouched.
func foldStrictParameterNull(c *exec.Context, node expr.Expr) (expr.Expr, bool, error) {
	if node == nil || len(c.Parameters) == 0 || !node.Properties().ParameterDependent {
		return node, false, nil
	}
	switch node.(type) {
	case plan.LiteralExpr, plan.DistinctExpr, plan.ConditionalExpr, plan.ScalarArrayComparisonExpr, plan.RowComparisonExpr:
		// These carriers can name an internal strict comparison without
		// making the whole NULL-aware or multi-comparison expression strict.
		return node, false, nil
	}
	selected, err := exec.ExpressionRoutineDescriptor(node)
	if err != nil {
		return nil, false, err
	}
	if !exec.StrictScalarRoutine(selected) {
		return node, false, nil
	}
	null := false
	var failure error
	exec.StrictRoutineArguments(node, func(argument expr.Expr) {
		if null || failure != nil {
			return
		}
		null, failure = executionKnownNullArgument(c, argument)
	})
	if failure != nil {
		return nil, false, failure
	}
	if !null {
		return node, false, nil
	}
	info := foldedExpressionInfo(node)
	return plan.LiteralExpr{Info: info, Value: sqltype.ResultNull(info.Usage)}, true, nil
}

func executionKnownNullArgument(c *exec.Context, node expr.Expr) (bool, error) {
	if node == nil || node.Properties().RuntimeDomainConversion {
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
		return executionKnownNullArgument(c, e.Child)
	case plan.CoercionExpr:
		if e.Usage.Type == nil || e.Usage.Type.IsDomain() {
			return false, nil
		}
		return executionKnownNullArgument(c, e.Child)
	case plan.CaseExpr:
		// Existing conditional planning preserves conservative properties on
		// this already-decided native CASE carrier, rather than adding a cast
		// or substituting parameters throughout the analyzed expression.
		if e.Test == nil && len(e.Arms) == 0 {
			return executionKnownNullArgument(c, e.Otherwise)
		}
	}
	return false, nil
}
