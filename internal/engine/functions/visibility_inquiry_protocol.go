package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

func (n VisibilityInquiry) Children() []expr.Expr { return []expr.Expr{n.Arg} }
func (n VisibilityInquiry) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("visibility inquiry", children, 1); err != nil {
		return nil, err
	}
	n.Info, n.Arg = info, children[0]
	return n, nil
}

// Keep the historical analyzed-property fallback, including eliminated arms.
func (n VisibilityInquiry) IntrinsicVolatility() expr.Volatility { return n.Property.Volatility }
func (VisibilityInquiry) PlanningFold() expr.Fold                { return expr.FoldNever }
func (VisibilityInquiry) PreflightFold() expr.Preflight          { return expr.PreflightChildren }
func (VisibilityInquiry) LooksLikeFunction() bool                { return true }
func (VisibilityInquiry) CallCost() int                          { return 10 }
func (VisibilityInquiry) SnapshotDemand(func(expr.Expr) bool) (bool, bool, bool) {
	return false, false, true
}
func (n VisibilityInquiry) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(VisibilityInquiry)
	return ok && n.Kind == o.Kind && same(n.Arg, o.Arg)
}
func (n VisibilityInquiry) Validate() error {
	u := n.ResultUsage()
	if n.Kind.Class() == 0 || u.Type == nil || u != u.Type.Usage() || u.Type.ID() != n.Kind.result() || n.Arg == nil {
		return fmt.Errorf("stuntgres: invalid stored visibility inquiry signature")
	}
	argument := n.Arg.ResultUsage()
	if argument.Type == nil || argument != argument.Type.Usage() || argument.Type.ID() != n.Kind.formal() {
		return fmt.Errorf("stuntgres: invalid stored visibility inquiry argument")
	}
	properties := n.Arg.Properties()
	properties.Volatility = max(n.Kind.volatility(), properties.Volatility)
	if n.Properties() != properties {
		return fmt.Errorf("stuntgres: invalid stored visibility inquiry properties")
	}
	return nil
}
func (n VisibilityInquiry) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: fmt.Sprintf("VisibilityInquiry %d", n.Kind), Inputs: n.Children(), Form: expr.FormCall, Name: n.Kind.name(), Args: []expr.RenderArg{{Expr: n.Arg}}, Replay: expr.Replay{RelationOnly: true, AlwaysVisibilitySnapshot: true}}, nil
}
func (n VisibilityInquiry) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: 1, Inline: [2]expr.Expr{n.Arg}}, true, nil
}
