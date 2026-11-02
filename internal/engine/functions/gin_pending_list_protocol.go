package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (n GinCleanPendingList) Children() []expr.Expr { return []expr.Expr{n.Arg} }
func (n GinCleanPendingList) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("GIN pending-list cleanup", children, 1); err != nil {
		return nil, err
	}
	n.Info, n.Arg = info, children[0]
	if err := n.Validate(); err != nil {
		return nil, err
	}
	return n, nil
}
func (GinCleanPendingList) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (GinCleanPendingList) StrictNull() expr.Strict              { return expr.StrictAfter }
func (GinCleanPendingList) PlanningFold() expr.Fold              { return expr.FoldNever }
func (GinCleanPendingList) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (GinCleanPendingList) LooksLikeFunction() bool              { return true }
func (GinCleanPendingList) CallCost() int                        { return 1 }
func (GinCleanPendingList) SourceAdmission() (bool, bool)        { return true, false }
func (n GinCleanPendingList) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(GinCleanPendingList)
	return ok && same(n.Arg, o.Arg)
}
func (n GinCleanPendingList) Validate() error {
	u := n.ResultUsage()
	if u.Type == nil || u.Type.ID() != sqltype.Int8 || u != u.Type.Usage() {
		return fmt.Errorf("stuntgres: invalid GIN pending-list cleanup result")
	}
	if n.Arg == nil || n.Arg.ResultUsage().Type == nil || n.Arg.ResultUsage().Type.ID() != sqltype.Regclass || n.Arg.ResultUsage() != n.Arg.ResultUsage().Type.Usage() {
		return fmt.Errorf("stuntgres: invalid GIN pending-list cleanup operand")
	}
	if n.Properties() != expr.MergeProperties(expr.Properties{Volatility: expr.Volatile}, n.Arg.Properties()) {
		return fmt.Errorf("stuntgres: invalid GIN pending-list cleanup properties")
	}
	return nil
}
func (n GinCleanPendingList) ValidateStoredProvider() error { return n.Validate() }
func (n GinCleanPendingList) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "GinCleanPendingList", Inputs: []expr.Expr{n.Arg}, Form: expr.FormCall, Name: "gin_clean_pending_list", Args: []expr.RenderArg{{Expr: n.Arg}}}, nil
}
func (n GinCleanPendingList) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if err := n.Validate(); err != nil {
		return expr.CaptureOperands{}, false, err
	}
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: 1, Inline: [2]expr.Expr{n.Arg}}, true, nil
}
