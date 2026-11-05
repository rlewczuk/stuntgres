package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

func (BackupControl) IntrinsicVolatility() expr.Volatility    { return expr.Volatile }
func (BackupControl) PlanningFold() expr.Fold                 { return expr.FoldNever }
func (BackupControl) PreflightFold() expr.Preflight           { return expr.PreflightKeep }
func (BackupControl) LooksLikeFunction() bool                 { return true }
func (BackupControl) CallCost() int                           { return 1 }
func (BackupControl) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (BackupControl) SourceAdmission() (bool, bool)           { return true, false }
func (n BackupControl) Children() []expr.Expr                 { return n.Args[:min(n.Arity, 2)] }
func (n BackupControl) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(BackupControl)
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
func (n BackupControl) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != int(n.Arity) {
		return nil, fmt.Errorf("stuntgres: invalid backup control reconstruction")
	}
	n.Info = info
	for i, child := range children {
		if i >= 2 || child == nil {
			return nil, fmt.Errorf("stuntgres: invalid backup control child")
		}
		n.Args[i] = child
	}
	if err := n.Validate(); err != nil {
		return nil, err
	}
	return n, nil
}
func (n BackupControl) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Substitute(info, children)
}
func (n BackupControl) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	labels, _, _ := n.Kind.formals()
	args := make([]expr.RenderArg, n.Arity)
	named := false
	for i := range int(n.Arity) {
		named = named || n.Slots[i] != uint8(i)
	}
	for i := range args {
		args[i] = expr.RenderArg{Expr: n.Args[i]}
		if named {
			args[i].Label = labels[n.Slots[i]]
		}
	}
	return expr.Rendering{Tree: "BackupControl", Inputs: n.Children(), Form: expr.FormCall, Name: n.Kind.Name(), Args: args}, nil
}
func (n BackupControl) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if err := n.Validate(); err != nil {
		return expr.CaptureOperands{}, false, err
	}
	return expr.CaptureOperands{Count: int(n.Arity), Indexed: true}, kind == expr.CaptureFunction, nil
}
func (n BackupControl) CaptureOperand(written int) (expr.Expr, error) {
	if written < 0 || written >= int(n.Arity) || written >= 2 {
		return nil, fmt.Errorf("stuntgres: invalid backup control capture")
	}
	return n.Args[written], nil
}
func (n BackupControl) ValidateStoredProvider() error { return n.Validate() }

func (BackupControl) JumbleFields(s expr.StatementFields) {
	s.JumbleText("functions.BackupControl")
}
