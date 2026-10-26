package exec

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// foldRandomNullInputs runs only after ordinary planning has visited every
// child. Required immutable computations, including typed-frame inputs, precede
// known-NULL elimination; runtime-only siblings are not evaluated. handled also
// covers deferral, so neither a composite fold nor its parameter-NULL wrapper
// can erase a call whose required inputs have not been imported yet.
func foldRandomNullInputs(c *Context, node expr.Expr) (next expr.Expr, changed, handled bool, err error) {
	call, ok := node.(functions.Random)
	if !ok {
		return node, false, false, nil
	}
	knownNull, literalNull := false, false
	for _, arg := range call.Args {
		if RandomLiteralNull(arg) {
			knownNull, literalNull = true, true
			continue
		}
		null, err := ExecutionKnownNullArgument(c, arg)
		if err != nil {
			return nil, false, true, err
		}
		knownNull = knownNull || null
	}
	if !knownNull {
		return node, false, false, nil
	}
	for _, arg := range call.Args {
		if !randomNullInputComputation(arg) {
			continue
		}
		ready := true
		plan.WalkExpression(arg, func(child expr.Expr) {
			if parameter, ok := child.(plan.ParameterExpr); ok {
				if c == nil || parameter.Number <= 0 || parameter.Number > len(c.Parameters) || c.Parameters[parameter.Number-1].Type() == nil {
					ready = false
				}
			}
		})
		if !ready {
			return node, false, true, nil
		}
	}
	for _, arg := range call.Args {
		if randomNullInputComputation(arg) {
			// A typed NULL can erase a strict child without acquiring that
			// child's EXECUTE demand. This proof never evaluates a provider.
			if _, folded, err := FoldStrictParameterNull(c, arg); err != nil {
				return nil, false, true, err
			} else if folded {
				continue
			}
			if _, err := FoldPlanningExpression(c, arg); err != nil {
				return nil, false, true, err
			}
		}
	}
	if literalNull {
		return strictNullLiteral(call, expr.StrictAfterBare), true, true, nil
	}
	// Preserve the existing typed-parameter NULL metadata contract. Only the
	// literal-NULL route uses the binder-compatible bare result.
	info := foldedExpressionInfo(call)
	return plan.LiteralExpr{Info: info, Value: sqltype.ResultNull(info.Usage)}, true, true, nil
}

func randomNullInputComputation(node expr.Expr) bool {
	if _, literal := node.(plan.LiteralExpr); literal {
		return false
	}
	p := node.Properties()
	return p.Volatility == expr.Immutable && !p.RowDependent && !p.StageDependent && !p.DomainDependent && !p.RuntimeDomainConversion
}
