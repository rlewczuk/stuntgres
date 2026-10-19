package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

func (n Deparse) Children() []expr.Expr { return n.Args }
func (n Deparse) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("deparse", children, len(n.Args)); err != nil {
		return nil, err
	}
	n.Info, n.Args = info, children
	return n, nil
}

// Keep the historical analyzed-property fallback, including eliminated arms.
func (n Deparse) IntrinsicVolatility() expr.Volatility { return n.Property.Volatility }
func (Deparse) PlanningFold() expr.Fold                { return expr.FoldNever }
func (Deparse) PreflightFold() expr.Preflight          { return expr.PreflightChildren }
func (Deparse) LooksLikeFunction() bool                { return true }
func (n Deparse) SnapshotDemand(literalNull func(expr.Expr) bool) (bool, bool, bool) {
	for _, arg := range n.Args {
		if literalNull(arg) {
			return false, false, false
		}
	}
	return true, false, false
}
func (n Deparse) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(Deparse)
	return ok && n.Signature == o.Signature && sameChildren(n.Args, o.Args, same)
}
func (n Deparse) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	args := make([]expr.RenderArg, len(n.Args))
	for i, arg := range n.Args {
		args[i] = expr.RenderArg{Expr: arg}
	}
	return expr.Rendering{Tree: fmt.Sprintf("Deparse signature %d", n.Signature), Inputs: n.Args, Form: expr.FormCall, Name: n.Signature.Name(), Args: args, Replay: expr.Replay{CatalogSnapshot: true}}, nil
}
func (n Deparse) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: len(n.Args), Args: n.Args}, true, nil
}

func (Deparse) GroupProperties(children []expr.Expr) expr.Properties {
	properties := expr.Properties{Volatility: expr.Stable}
	for _, child := range children {
		properties = expr.MergeProperties(properties, child.Properties())
	}
	return properties
}
