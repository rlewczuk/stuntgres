package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

func (ListeningChannels) IntrinsicVolatility() expr.Volatility         { return expr.Stable }
func (NotificationQueueUsage) IntrinsicVolatility() expr.Volatility    { return expr.Volatile }
func (ListeningChannels) PlanningFold() expr.Fold                      { return expr.FoldNever }
func (NotificationQueueUsage) PlanningFold() expr.Fold                 { return expr.FoldNever }
func (ListeningChannels) PreflightFold() expr.Preflight                { return expr.PreflightKeep }
func (NotificationQueueUsage) PreflightFold() expr.Preflight           { return expr.PreflightKeep }
func (ListeningChannels) LooksLikeFunction() bool                      { return true }
func (NotificationQueueUsage) LooksLikeFunction() bool                 { return true }
func (ListeningChannels) ClosedDefault(func(expr.Expr) bool) bool      { return false }
func (NotificationQueueUsage) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (ListeningChannels) SourceAdmission() (bool, bool)                { return true, true }
func (NotificationQueueUsage) SourceAdmission() (bool, bool)           { return true, false }
func (ListeningChannels) Children() []expr.Expr                        { return nil }
func (NotificationQueueUsage) Children() []expr.Expr                   { return nil }
func (ListeningChannels) GroupLeaf()                                   {}
func (NotificationQueueUsage) GroupLeaf()                              {}
func (n ListeningChannels) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(ListeningChannels)
	return ok && n.ResultUsage() == o.ResultUsage()
}
func (ListeningChannels) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool {
	_, ok := other.(ListeningChannels)
	return ok
}
func (NotificationQueueUsage) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool {
	_, ok := other.(NotificationQueueUsage)
	return ok
}
func (n ListeningChannels) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 {
		return nil, fmt.Errorf("stuntgres: invalid listening channels arguments")
	}
	n.Info = info
	return n, nil
}
func (n NotificationQueueUsage) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 {
		return nil, fmt.Errorf("stuntgres: invalid notification queue usage arguments")
	}
	n.Info = info
	return n, nil
}
func (n ListeningChannels) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(ListeningChannels)
	return rebuilt, rebuilt.Validate()
}
func (n NotificationQueueUsage) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(NotificationQueueUsage)
	return rebuilt, rebuilt.Validate()
}
func (n ListeningChannels) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "ListeningChannels", Form: expr.FormCall, Name: "pg_listening_channels"}, nil
}
func (n NotificationQueueUsage) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "NotificationQueueUsage", Form: expr.FormCall, Name: "pg_notification_queue_usage"}, nil
}
func (ListeningChannels) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil
}
func (NotificationQueueUsage) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil
}
func (n ListeningChannels) ValidateStoredProvider() error      { return n.Validate() }
func (n NotificationQueueUsage) ValidateStoredProvider() error { return n.Validate() }
