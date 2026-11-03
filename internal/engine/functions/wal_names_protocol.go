package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

func (n WALName) IntrinsicVolatility() expr.Volatility {
	if n.Kind == WALSplitFileName {
		return expr.Stable
	}
	return expr.Immutable
}
func (WALName) PlanningFold() expr.Fold       { return expr.FoldConstant }
func (WALName) PreflightFold() expr.Preflight { return expr.PreflightSimplify }
func (WALName) StrictNull() expr.Strict       { return expr.StrictAfter }
func (WALName) LooksLikeFunction() bool       { return true }
func (WALName) CallCost() int                 { return 1 }
func (n WALName) ClosedDefault(closed func(expr.Expr) bool) bool {
	return n.Arg != nil && closed(n.Arg)
}
func (WALName) SourceAdmission() (bool, bool) { return true, false }
func (n WALName) Children() []expr.Expr       { return []expr.Expr{n.Arg} }
func (n WALName) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(WALName)
	return ok && n.Kind == o.Kind && n.Metadata == o.Metadata && n.Arg != nil && o.Arg != nil && same(n.Arg, o.Arg)
}
func (n WALName) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(WALName)
	return ok && n.Kind == o.Kind && n.Metadata == o.Metadata && n.ResultUsage() == o.ResultUsage() && n.Source == o.Source && n.Occurrence == o.Occurrence && n.RoutineReference == o.RoutineReference
}
func (n WALName) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 1 {
		return nil, fmt.Errorf("stuntgres: invalid WAL name reconstruction")
	}
	n.Info, n.Arg = info, children[0]
	if err := n.Validate(); err != nil {
		return nil, err
	}
	return n, nil
}
func (n WALName) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Substitute(info, children)
}
func (n WALName) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	children := n.Children()
	return callRendering("WALName", children, n.Kind.Name(), children), nil
}
func (n WALName) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if err := n.Validate(); err != nil {
		return expr.CaptureOperands{}, false, err
	}
	return expr.CaptureOperands{Count: 1, Indexed: true}, kind == expr.CaptureFunction, nil
}
func (n WALName) CaptureOperand(written int) (expr.Expr, error) {
	if err := n.Validate(); err != nil {
		return nil, err
	}
	if written != 0 {
		return nil, fmt.Errorf("stuntgres: invalid WAL name capture")
	}
	return n.Arg, nil
}
func (n WALName) ValidateStoredProvider() error { return n.Validate() }
func (n WALName) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.WALName")
	s.JumbleText(n.Kind.Name())
}
