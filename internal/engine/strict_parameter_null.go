package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

// PostgreSQL leaves RECORD calls unreduced so their concrete tuple description
// survives, even for a strict known-NULL input (evaluate_function, REL_18_0).
// Scalar calls may disappear in either target or FROM position; SRFs may not.
func strictScalarRoutine(routine *catalog.Procedure) bool {
	return routine != nil && routine.Strict && routine.RoutineKind == catalog.FunctionRoutine &&
		routine.Result != 2249 && !procedureReturnsSet(routine)
}

// Child preflight has already preserved ordinary constant errors and parameter
// input validation. A NULL proof cannot cross a domain conversion in that
// argument, but a separate known-NULL argument can discard runtime siblings,
// including domain coercions. No domain truth or provider is sampled here.
// The analyzed tree and its original dependency manifest remain untouched.
func foldStrictParameterNull(c *EvalContext, node expr.Expr) (expr.Expr, bool, error) {
	if node == nil || len(c.Parameters) == 0 || !node.Properties().ParameterDependent {
		return node, false, nil
	}
	switch node.(type) {
	case literalExpression, distinctExpression, conditionalExpression, scalarArrayComparisonExpression, rowComparisonExpression:
		// These carriers can name an internal strict comparison without
		// making the whole NULL-aware or multi-comparison expression strict.
		return node, false, nil
	}
	selected, err := routine.ExpressionRoutineDescriptor(node)
	if err != nil {
		return nil, false, err
	}
	if !strictScalarRoutine(selected) {
		return node, false, nil
	}
	null := false
	var failure error
	strictRoutineArguments(node, func(argument expr.Expr) {
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
	return literalExpression{Info: info, Value: sqltype.ResultNull(info.Usage)}, true, nil
}

// Expanded variadic path elements form one non-NULL array argument. A NULL
// element is not a NULL formal; explicit VARIADIC retains the array child.
func strictRoutineArguments(node expr.Expr, visit func(expr.Expr)) {
	if expression, ok := node.(functions.JSONExtract); ok && !expression.Variadic {
		visit(expression.Args[0])
		return
	}
	boundExpressionChildren(node, visit)
}

func executionKnownNullArgument(c *EvalContext, node expr.Expr) (bool, error) {
	if node == nil || node.Properties().RuntimeDomainConversion {
		return false, nil
	}
	switch e := node.(type) {
	case literalExpression:
		return e.Value.IsNull(), nil
	case parameterExpression:
		// Description and partially imported frames do not supply a value.
		if e.Number <= 0 || e.Number > len(c.Parameters) || c.Parameters[e.Number-1].Type() == nil {
			return false, nil
		}
		value, err := e.Eval(c) // Typed frame access only; no operator/function/domain Eval.
		return value.IsNull(), err
	case collationExpression:
		return executionKnownNullArgument(c, e.Child)
	case coercionExpression:
		if e.Usage.Type == nil || e.Usage.Type.IsDomain() {
			return false, nil
		}
		return executionKnownNullArgument(c, e.Child)
	case caseExpression:
		// Existing conditional planning preserves conservative properties on
		// this already-decided native CASE carrier, rather than adding a cast
		// or substituting parameters throughout the analyzed expression.
		if e.Test == nil && len(e.Arms) == 0 {
			return executionKnownNullArgument(c, e.Otherwise)
		}
	}
	return false, nil
}
