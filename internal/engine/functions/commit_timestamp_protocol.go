package functions

import "stuntgres/internal/engine/expr"

func (n commitTimestampExpression) IntrinsicVolatility() expr.Volatility {
	return expr.Volatile
}
func (n commitTimestampExpression) PlanningFold() expr.Fold       { return expr.FoldNever }
func (n commitTimestampExpression) PreflightFold() expr.Preflight { return expr.PreflightChildren }
func (n commitTimestampExpression) LooksLikeFunction() bool       { return true }
func (n commitTimestampExpression) ValidateStoredProvider() error { return n.validate() }

func (n commitTimestampExpression) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(commitTimestampExpression)
	return ok && n.kind == o.kind && ((n.arg == nil && o.arg == nil) ||
		(n.arg != nil && o.arg != nil && same(n.arg, o.arg)))
}

// Conditional closure deliberately does not change UUID DEFAULT policy.
func (n commitTimestampExpression) ClosedDefault(closed func(expr.Expr) bool) bool {
	return n.arg == nil || closed(n.arg)
}

func (n commitTimestampExpression) Render() (expr.Rendering, error) {
	var args []expr.RenderArg
	if n.arg != nil {
		args = []expr.RenderArg{{Expr: n.arg}}
	}
	return expr.Rendering{
		Tree: "CommitTimestamp " + n.kind.name(), Inputs: n.Children(),
		Form: expr.FormCall, Name: n.kind.name(), Quote: expr.QuoteOnRequest,
		Args: args, Replay: expr.Replay{Validate: n.validate},
	}, nil
}

func (n commitTimestampExpression) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	count := 0
	if n.arg != nil {
		count = 1
	}
	return expr.CaptureOperands{Count: count, Inline: [2]expr.Expr{n.arg}}, true, nil
}
