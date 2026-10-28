package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

func (SessionEndpoint) IntrinsicVolatility() expr.Volatility    { return expr.Stable }
func (SessionEndpoint) PlanningFold() expr.Fold                 { return expr.FoldNever }
func (SessionEndpoint) PreflightFold() expr.Preflight           { return expr.PreflightKeep }
func (SessionEndpoint) LooksLikeFunction() bool                 { return true }
func (SessionEndpoint) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (SessionEndpoint) SourceAdmission() (bool, bool)           { return true, false }
func (SessionEndpoint) Children() []expr.Expr                   { return nil }
func (SessionEndpoint) GroupLeaf()                              {}
func (n SessionEndpoint) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(SessionEndpoint)
	return ok && n.Kind == o.Kind
}
func (n SessionEndpoint) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 {
		return nil, fmt.Errorf("stuntgres: invalid session endpoint children")
	}
	n.Info = info
	return n, nil
}
func (n SessionEndpoint) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	v := next.(SessionEndpoint)
	return v, v.Validate()
}
func (n SessionEndpoint) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "SessionEndpoint", Form: expr.FormCall, Name: sessionEndpointNames[n.Kind]}, nil
}
func (SessionEndpoint) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil
}
func (n SessionEndpoint) ValidateStoredProvider() error { return n.Validate() }
