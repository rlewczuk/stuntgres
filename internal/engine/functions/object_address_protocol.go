package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

func (ObjectAddress) IntrinsicVolatility() expr.Volatility { return expr.Stable }
func (ObjectAddress) PlanningFold() expr.Fold              { return expr.FoldNever }
func (n ObjectAddress) StrictNull() expr.Strict {
	if n.op == DescribeObject {
		return expr.StrictAfter
	}
	return expr.StrictNone
}
func (n ObjectAddress) PreflightFold() expr.Preflight {
	if n.op == DescribeObject {
		return expr.PreflightSimplify
	}
	return expr.PreflightChildren
}
func (ObjectAddress) LooksLikeFunction() bool         { return true }
func (n ObjectAddress) ValidateStoredProvider() error { return n.Validate() }
func (ObjectAddress) SnapshotDemand(func(expr.Expr) bool) (bool, bool, bool) {
	return true, false, false
}
func (n ObjectAddress) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(ObjectAddress)
	if !ok || n.op != o.op || n.Slots != o.Slots || n.Labels != o.Labels {
		return false
	}
	for i, arg := range n.Args {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}
func (n ObjectAddress) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	args := make([]expr.RenderArg, len(n.Args))
	for i, arg := range n.Args {
		args[i] = expr.RenderArg{Expr: arg, Label: n.Labels[i]}
	}
	return expr.Rendering{Tree: fmt.Sprintf("ObjectAddress %d slots %v labels %v", n.op, n.Slots, n.Labels), Inputs: n.Children(), Form: expr.FormCall, Name: n.op.name(), Quote: expr.QuoteIdentifier, LabelQuote: expr.QuoteVerbatim, Args: args, Replay: expr.Replay{CatalogSnapshot: true}}, nil
}
func (n ObjectAddress) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return captureShape(3, len(n.Args))
}
func (n ObjectAddress) CaptureOperand(written int) (expr.Expr, error) {
	return captureAt(n.Args[:], 3, written)
}
