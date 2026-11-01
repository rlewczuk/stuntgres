package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

func (SSLInfo) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (SSLInfo) PlanningFold() expr.Fold { return expr.FoldNever }
func (SSLInfo) PreflightFold() expr.Preflight { return expr.PreflightKeep }
func (SSLInfo) LooksLikeFunction() bool { return true }
func (SSLInfo) SourceAdmission() (bool, bool) { return true, false }
func (SSLInfo) Children() []expr.Expr { return nil }
func (SSLInfo) GroupLeaf() {}
func (n SSLInfo) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(SSLInfo)
	return ok && n.Name == o.Name && n.Usage == o.Usage
}
func (n SSLInfo) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 { return nil, fmt.Errorf("stuntgres: invalid sslinfo children") }
	n.Info = info
	return n, n.Validate()
}
func (n SSLInfo) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Substitute(info, children)
}
func (n SSLInfo) Retag(info expr.Info, children []expr.Expr, changed bool, _ expr.Retag) (expr.Expr, bool, error) {
	next, err := n.Rebuild(info, children)
	return next, changed, err
}
func (n SSLInfo) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil { return expr.Rendering{}, err }
	return expr.Rendering{Tree: "SSLInfo", Form: expr.FormCall, Name: n.Name}, nil
}
func (n SSLInfo) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if err := n.Validate(); err != nil { return expr.CaptureOperands{}, false, err }
	return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil
}
func (n SSLInfo) ValidateStoredProvider() error { return n.Validate() }
