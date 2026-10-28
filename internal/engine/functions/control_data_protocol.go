package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

func (ControlData) IntrinsicVolatility() expr.Volatility    { return expr.Volatile }
func (ControlData) PlanningFold() expr.Fold                 { return expr.FoldNever }
func (ControlData) PreflightFold() expr.Preflight           { return expr.PreflightKeep }
func (ControlData) LooksLikeFunction() bool                 { return true }
func (ControlData) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (ControlData) SourceAdmission() (bool, bool)           { return true, false }
func (ControlData) Children() []expr.Expr                   { return nil }
func (ControlData) GroupLeaf()                              {}
func (n ControlData) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(ControlData)
	return ok && n.Kind == o.Kind
}
func (n ControlData) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 {
		return nil, fmt.Errorf("stuntgres: invalid control data children")
	}
	n.Info = info
	if err := n.Validate(); err != nil { return nil, err }
	return n, nil
}
func (n ControlData) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Substitute(info, children)
}
func (n ControlData) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "ControlData", Form: expr.FormCall, Name: n.Kind.Name()}, nil
}
func (n ControlData) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if err := n.Validate(); err != nil { return expr.CaptureOperands{}, false, err }
	return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil
}
func (n ControlData) ValidateStoredProvider() error { return n.Validate() }

func (ControlData) CallCost() int { return 1 }
func (n ControlData) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(ControlData)
	return ok && n.Kind == o.Kind && n.ResultUsage() == o.ResultUsage()
}
