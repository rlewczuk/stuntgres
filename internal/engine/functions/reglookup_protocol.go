package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

func (RegLookup) IntrinsicVolatility() expr.Volatility { return expr.Stable }
func (RegLookup) PlanningFold() expr.Fold              { return expr.FoldNever }
func (RegLookup) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (RegLookup) LooksLikeFunction() bool              { return true }
func (RegLookup) CallCost() int                        { return 1 }

func (n RegLookup) ClosedDefault(closed func(expr.Expr) bool) bool {
	return closed(n.Arg)
}

func (n RegLookup) Children() []expr.Expr { return []expr.Expr{n.Arg} }

func (n RegLookup) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(RegLookup)
	return ok && n.Kind == o.Kind && n.Usage == o.Usage && n.RoutineReference == o.RoutineReference && same(n.Arg, o.Arg)
}

func (n RegLookup) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 1 || children[0] == nil {
		return nil, fmt.Errorf("stuntgres: invalid registry lookup children")
	}
	if info.Usage != n.Usage || info.RoutineReference != n.RoutineReference || children[0].ResultUsage() != n.Arg.ResultUsage() {
		return nil, fmt.Errorf("stuntgres: changed registry lookup provider")
	}
	n.Info, n.Arg = info, children[0]
	return n, nil
}

func (n RegLookup) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	n = next.(RegLookup)
	return n, n.Validate()
}

// Retag is the existing authorized descriptor/routine-generation mapping path.
func (n RegLookup) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	if err := n.Validate(); err != nil {
		return nil, false, err
	}
	if len(children) != 1 || children[0] == nil {
		return nil, false, fmt.Errorf("stuntgres: invalid registry lookup children")
	}
	result, formal := env.Type(n.Usage.Type), env.Type(n.Arg.ResultUsage().Type)
	if result == nil || formal == nil || info.Usage != result.Usage() || children[0].ResultUsage() != formal.Usage() {
		return nil, false, fmt.Errorf("stuntgres: invalid mapped registry lookup descriptors")
	}
	changed = changed || result != n.Usage.Type || formal != n.Arg.ResultUsage().Type || info.RoutineReference != n.RoutineReference
	n.Info, n.Arg = info, children[0]
	return n, changed, n.Validate()
}

func (n RegLookup) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "RegLookup", Inputs: n.Children(), Form: expr.FormCall, Name: n.name(), Args: []expr.RenderArg{{Expr: n.Arg}}, Replay: expr.Replay{Validate: n.Validate}}, nil
}

func (n RegLookup) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: 1, Inline: [2]expr.Expr{n.Arg}}, true, nil
}

func (n RegLookup) ValidateStoredProvider() error { return n.Validate() }
