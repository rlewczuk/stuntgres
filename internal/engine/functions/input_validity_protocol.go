package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

func (InputValidity) IntrinsicVolatility() expr.Volatility { return expr.Stable }
func (InputValidity) PlanningFold() expr.Fold              { return expr.FoldNever }
func (InputValidity) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (InputValidity) LooksLikeFunction() bool              { return true }
func (InputValidity) CallCost() int                        { return 1 }
func (n InputValidity) ClosedDefault(closed func(expr.Expr) bool) bool {
	return closed(n.Args[0]) && closed(n.Args[1])
}
func (n InputValidity) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(InputValidity)
	return ok && n.ErrorInfo == o.ErrorInfo && n.Slots == o.Slots && same(n.Args[0], o.Args[0]) && same(n.Args[1], o.Args[1])
}
func (n InputValidity) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	name := "pg_input_is_valid"
	if n.ErrorInfo {
		name = "pg_input_error_info"
	}
	return expr.Rendering{Tree: "InputValidity", Inputs: n.Children(), Form: expr.FormCall, Name: name, Args: []expr.RenderArg{{Expr: n.Args[0], Label: n.Labels[0]}, {Expr: n.Args[1], Label: n.Labels[1]}}}, nil
}
func (n InputValidity) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: 2, Inline: n.Args}, true, nil
}
func (n InputValidity) ValidateStoredProvider() error { return n.Validate() }
func (n InputValidity) Children() []expr.Expr         { return n.Args[:] }
func (n InputValidity) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 2 || children[0] == nil || children[1] == nil {
		return nil, fmt.Errorf("stuntgres: invalid input-validity children")
	}
	n.Info = info
	n.Args = [2]expr.Expr{children[0], children[1]}
	return n, nil
}
func (n InputValidity) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	n = next.(InputValidity)
	return n, n.Validate()
}
