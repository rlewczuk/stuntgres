package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

func (EncodingNameCall) IntrinsicVolatility() expr.Volatility { return expr.Stable }
func (Keywords) IntrinsicVolatility() expr.Volatility { return expr.Stable }
func (EncodingNameCall) PlanningFold() expr.Fold { return expr.FoldNever }
func (Keywords) PlanningFold() expr.Fold { return expr.FoldNever }
func (EncodingNameCall) PreflightFold() expr.Preflight { return expr.PreflightChildren }
func (Keywords) PreflightFold() expr.Preflight { return expr.PreflightKeep }
func (EncodingNameCall) LooksLikeFunction() bool { return true }
func (Keywords) LooksLikeFunction() bool { return true }
func (EncodingNameCall) CallCost() int { return 1 }
func (Keywords) CallCost() int { return 10 }
func (n EncodingNameCall) ClosedDefault(closed func(expr.Expr) bool) bool { return closed(n.Arg) }
func (Keywords) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (n EncodingNameCall) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(EncodingNameCall)
	return ok && n.ToID == o.ToID && same(n.Arg, o.Arg)
}
func (Keywords) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool { _, ok := other.(Keywords); return ok }
func (n EncodingNameCall) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil { return expr.Rendering{}, err }
	name := "pg_encoding_to_char"
	if n.ToID { name = "pg_char_to_encoding" }
	return expr.Rendering{Tree: "EncodingNameCall", Inputs: n.Children(), Form: expr.FormCall, Name: name, Args: []expr.RenderArg{{Expr: n.Arg}}}, nil
}
func (n Keywords) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil { return expr.Rendering{}, err }
	return expr.Rendering{Tree: "Keywords", Form: expr.FormCall, Name: "pg_get_keywords"}, nil
}
func (n EncodingNameCall) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction { return expr.CaptureOperands{}, false, nil }
	return expr.CaptureOperands{Count: 1, Inline: [2]expr.Expr{n.Arg}}, true, nil
}
func (Keywords) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) { return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil }
func (n EncodingNameCall) ValidateStoredProvider() error { return n.Validate() }
func (n Keywords) ValidateStoredProvider() error { return n.Validate() }
func (n EncodingNameCall) Children() []expr.Expr { return []expr.Expr{n.Arg} }
func (Keywords) Children() []expr.Expr { return nil }
func (n EncodingNameCall) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 1 || children[0] == nil { return nil, fmt.Errorf("stuntgres: invalid encoding-name children") }
	n.Info, n.Arg = info, children[0]
	return n, nil
}
func (n EncodingNameCall) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil { return nil, err }
	n = next.(EncodingNameCall)
	return n, n.Validate()
}
func (n Keywords) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 { return nil, fmt.Errorf("stuntgres: invalid keyword children") }
	n.Info = info
	return n, nil
}
func (n Keywords) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil { return nil, err }
	n = next.(Keywords)
	return n, n.Validate()
}
