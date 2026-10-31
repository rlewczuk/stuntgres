package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

func (SessionSignal) IntrinsicVolatility() expr.Volatility    { return expr.Volatile }
func (SessionSignal) PlanningFold() expr.Fold                 { return expr.FoldNever }
func (SessionSignal) StrictNull() expr.Strict                 { return expr.StrictAfterBare }
func (SessionSignal) PreflightFold() expr.Preflight           { return expr.PreflightNullBare }
func (SessionSignal) LooksLikeFunction() bool                 { return true }
func (SessionSignal) CallCost() int                           { return 1 }
func (SessionSignal) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (SessionSignal) SourceAdmission() (bool, bool)           { return true, false }
func (n SessionSignal) Children() []expr.Expr                 { return n.Args[:min(n.Arity, 2)] }

func (n SessionSignal) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(SessionSignal)
	if !ok || n.Kind != o.Kind || n.Arity != o.Arity || n.Slots != o.Slots {
		return false
	}
	for i := range int(n.Arity) {
		if !same(n.Args[i], o.Args[i]) {
			return false
		}
	}
	return true
}

func (n SessionSignal) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != int(n.Arity) || len(children) > 2 {
		return nil, fmt.Errorf("stuntgres: invalid session signal reconstruction")
	}
	n.Info = info
	for i, child := range children {
		n.Args[i] = child
	}
	if err := n.Validate(); err != nil {
		return nil, err
	}
	return n, nil
}

func (n SessionSignal) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Substitute(info, children)
}

func (n SessionSignal) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	args := make([]expr.RenderArg, n.Arity)
	named := false
	for i := range int(n.Arity) {
		named = named || n.Slots[i] != uint8(i)
	}
	for i := range args {
		args[i] = expr.RenderArg{Expr: n.Args[i]}
		if named {
			args[i].Label = sessionSignalLabels[n.Slots[i]]
		}
	}
	return expr.Rendering{Tree: "SessionSignal", Inputs: n.Children(), Form: expr.FormCall, Name: n.Kind.Name(), Args: args}, nil
}

func (n SessionSignal) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if err := n.Validate(); err != nil {
		return expr.CaptureOperands{}, false, err
	}
	return expr.CaptureOperands{Count: int(n.Arity), Indexed: true}, kind == expr.CaptureFunction, nil
}

func (n SessionSignal) CaptureOperand(written int) (expr.Expr, error) {
	if written < 0 || written >= int(n.Arity) || written >= 2 {
		return nil, fmt.Errorf("stuntgres: invalid session signal capture")
	}
	return n.Args[written], nil
}

func (n SessionSignal) ValidateStoredProvider() error { return n.Validate() }
