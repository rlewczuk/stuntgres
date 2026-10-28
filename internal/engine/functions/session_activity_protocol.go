package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

func (n SessionActivity) IntrinsicVolatility() expr.Volatility { if n.Kind == ActivityQuery { return expr.Volatile }; return expr.Stable }
func (SessionActivity) PlanningFold() expr.Fold { return expr.FoldNever }
func (SessionActivity) PreflightFold() expr.Preflight { return expr.PreflightKeep }
func (SessionActivity) LooksLikeFunction() bool { return true }
func (SessionActivity) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (SessionActivity) SourceAdmission() (bool, bool) { return true, false }
func (SessionActivity) Children() []expr.Expr { return nil }
func (SessionActivity) GroupLeaf() {}
func (n SessionActivity) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool { o, ok := other.(SessionActivity); return ok && n.Kind == o.Kind }
func (n SessionActivity) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 { return nil, fmt.Errorf("stuntgres: invalid session activity children") }
	n.Info = info
	return n, nil
}
func (n SessionActivity) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil { return nil, err }
	v := next.(SessionActivity)
	return v, v.Validate()
}
func (n SessionActivity) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil { return expr.Rendering{}, err }
	return expr.Rendering{Tree: "SessionActivity", Form: expr.FormCall, Name: sessionActivityNames[n.Kind]}, nil
}
func (SessionActivity) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) { return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil }
func (n SessionActivity) ValidateStoredProvider() error { return n.Validate() }
