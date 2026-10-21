package functions

import "stuntgres/internal/engine/expr"

func (n LargeObjectCall) Children() []expr.Expr { return n.Args }
func (n LargeObjectCall) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("large object", children, len(n.Args)); err != nil {
		return nil, err
	}
	n.Info = info
	n.Args = append([]expr.Expr(nil), children...)
	return n, nil
}
func (LargeObjectCall) PlanningFold() expr.Fold       { return expr.FoldNever }
func (LargeObjectCall) PreflightFold() expr.Preflight { return expr.PreflightChildren }
func (LargeObjectCall) LooksLikeFunction() bool       { return true }
func (n LargeObjectCall) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(LargeObjectCall)
	return ok && n.Name == o.Name && sameChildren(n.Args, o.Args, same)
}
func (n LargeObjectCall) Render() (expr.Rendering, error) {
	args := make([]expr.RenderArg, len(n.Args))
	for i, arg := range n.Args {
		args[i] = expr.RenderArg{Expr: arg}
	}
	return expr.Rendering{Tree: "LargeObject " + n.Name, Inputs: n.Args, Form: expr.FormCall, Name: n.Name, Quote: expr.QuoteOnRequest, Args: args}, nil
}
func (n LargeObjectCall) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return captureShape(len(n.Args), len(n.Args))
}
func (n LargeObjectCall) CaptureOperand(written int) (expr.Expr, error) {
	return captureAt(n.Args, len(n.Args), written)
}
