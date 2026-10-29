package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (EventTriggerDDLCommands) IntrinsicVolatility() expr.Volatility { return expr.Stable }
func (EventTriggerDDLCommands) PlanningFold() expr.Fold              { return expr.FoldNever }
func (EventTriggerDDLCommands) PreflightFold() expr.Preflight        { return expr.PreflightKeep }
func (EventTriggerDDLCommands) LooksLikeFunction() bool              { return true }
func (EventTriggerDDLCommands) ClosedDefault(func(expr.Expr) bool) bool {
	return false
}
func (EventTriggerDDLCommands) SourceAdmission() (bool, bool) { return true, true }
func (EventTriggerDDLCommands) Children() []expr.Expr         { return nil }
func (EventTriggerDDLCommands) GroupLeaf()                    {}
func (n EventTriggerDDLCommands) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(EventTriggerDDLCommands)
	return ok && sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage())
}
func (EventTriggerDDLCommands) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool {
	_, ok := other.(EventTriggerDDLCommands)
	return ok
}
func (n EventTriggerDDLCommands) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 {
		return nil, fmt.Errorf("stuntgres: invalid DDL commands arguments")
	}
	n.Info = info
	return n, nil
}
func (n EventTriggerDDLCommands) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(EventTriggerDDLCommands)
	return rebuilt, rebuilt.Validate()
}
func (n EventTriggerDDLCommands) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "EventTriggerDDLCommands", Form: expr.FormCall, Name: eventTriggerDDLCommandsName}, nil
}
func (EventTriggerDDLCommands) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil
}
func (n EventTriggerDDLCommands) ValidateStoredProvider() error { return n.Validate() }
