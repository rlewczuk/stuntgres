package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (EventTriggerDroppedObjects) IntrinsicVolatility() expr.Volatility { return expr.Stable }
func (EventTriggerDroppedObjects) PlanningFold() expr.Fold              { return expr.FoldNever }
func (EventTriggerDroppedObjects) PreflightFold() expr.Preflight        { return expr.PreflightKeep }
func (EventTriggerDroppedObjects) LooksLikeFunction() bool              { return true }
func (EventTriggerDroppedObjects) ClosedDefault(func(expr.Expr) bool) bool {
	return false
}
func (EventTriggerDroppedObjects) SourceAdmission() (bool, bool) { return true, true }
func (EventTriggerDroppedObjects) Children() []expr.Expr         { return nil }
func (EventTriggerDroppedObjects) GroupLeaf()                    {}
func (n EventTriggerDroppedObjects) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(EventTriggerDroppedObjects)
	return ok && sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage())
}
func (EventTriggerDroppedObjects) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool {
	_, ok := other.(EventTriggerDroppedObjects)
	return ok
}
func (n EventTriggerDroppedObjects) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 {
		return nil, fmt.Errorf("stuntgres: invalid dropped objects arguments")
	}
	n.Info = info
	return n, nil
}
func (n EventTriggerDroppedObjects) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(EventTriggerDroppedObjects)
	return rebuilt, rebuilt.Validate()
}
func (n EventTriggerDroppedObjects) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "EventTriggerDroppedObjects", Form: expr.FormCall, Name: eventTriggerDroppedName}, nil
}
func (EventTriggerDroppedObjects) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil
}
func (n EventTriggerDroppedObjects) ValidateStoredProvider() error { return n.Validate() }
