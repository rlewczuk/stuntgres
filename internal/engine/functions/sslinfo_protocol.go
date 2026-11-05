package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

func (SSLInfo) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (SSLInfo) PlanningFold() expr.Fold              { return expr.FoldNever }
func (SSLInfo) PreflightFold() expr.Preflight        { return expr.PreflightKeep }
func (SSLInfo) LooksLikeFunction() bool              { return true }
func (n SSLInfo) SourceAdmission() (bool, bool) {
	member, ok := SSLInfoLookup(n.Name)
	return ok, member.Set
}
func (n SSLInfo) Children() []expr.Expr {
	if n.Arg == nil {
		return nil
	}
	return []expr.Expr{n.Arg}
}
func (n SSLInfo) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(SSLInfo)
	return ok && n.Name == o.Name && n.Usage == o.Usage && (n.Arg == nil && o.Arg == nil || n.Arg != nil && o.Arg != nil && same(n.Arg, o.Arg))
}
func (n SSLInfo) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	member, ok := SSLInfoLookup(n.Name)
	arity := 0
	if member.Signature != "" {
		arity = 1
	}
	if !ok || len(children) != arity {
		return nil, fmt.Errorf("stuntgres: invalid sslinfo children")
	}
	n.Info, n.Arg = info, nil
	if arity == 1 {
		n.Arg = children[0]
	}
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
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	rendering := expr.Rendering{Tree: "SSLInfo", Inputs: n.Children(), Form: expr.FormCall, Name: n.Name}
	if n.Arg != nil {
		rendering.Args = []expr.RenderArg{{Expr: n.Arg}}
	}
	return rendering, nil
}
func (n SSLInfo) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if err := n.Validate(); err != nil {
		return expr.CaptureOperands{}, false, err
	}
	if n.Arg != nil {
		return expr.CaptureOperands{Count: 1, Inline: [2]expr.Expr{n.Arg}}, kind == expr.CaptureFunction, nil
	}
	return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil
}
func (n SSLInfo) ValidateStoredProvider() error { return n.Validate() }
