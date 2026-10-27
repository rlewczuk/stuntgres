package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

func (GenericFileList) IntrinsicVolatility() expr.Volatility    { return expr.Volatile }
func (GenericFileList) PlanningFold() expr.Fold                 { return expr.FoldNever }
func (GenericFileList) PreflightFold() expr.Preflight           { return expr.PreflightChildren }
func (GenericFileList) LooksLikeFunction() bool                 { return true }
func (GenericFileList) CallCost() int                           { return 10 }
func (GenericFileList) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (GenericFileList) SourceAdmission() (bool, bool)           { return true, true }
func (n GenericFileList) Children() []expr.Expr                 { return n.Args[:n.Arity] }

func (n GenericFileList) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(GenericFileList)
	return ok && n.Kind == o.Kind && n.Arity == o.Arity && n.ResultUsage() == o.ResultUsage()
}

func (n GenericFileList) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(GenericFileList)
	if !ok || n.Kind != o.Kind || n.Arity != o.Arity {
		return false
	}
	for i := range int(n.Arity) {
		if !same(n.Args[i], o.Args[i]) {
			return false
		}
	}
	return true
}

func (n GenericFileList) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != int(n.Arity) {
		return nil, fmt.Errorf("stuntgres: invalid generic file listing reconstruction")
	}
	n.Info = info
	for i, child := range children {
		if child == nil {
			return nil, fmt.Errorf("stuntgres: nil generic file listing operand")
		}
		n.Args[i] = child
	}
	return n, nil
}

func (n GenericFileList) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(GenericFileList)
	if err := rebuilt.Validate(); err != nil {
		return nil, err
	}
	return rebuilt, nil
}

func (n GenericFileList) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	args := make([]expr.RenderArg, n.Arity)
	for i := range args {
		args[i] = expr.RenderArg{Expr: n.Args[i]}
	}
	return expr.Rendering{Tree: "GenericFileList", Inputs: n.Children(), Form: expr.FormCall, Name: n.Kind.Name(), Args: args}, nil
}

func (n GenericFileList) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: int(n.Arity), Indexed: true}, true, nil
}

func (n GenericFileList) CaptureOperand(written int) (expr.Expr, error) {
	if written < 0 || written >= int(n.Arity) {
		return nil, fmt.Errorf("stuntgres: invalid generic file listing capture")
	}
	return n.Args[written], nil
}

func (n GenericFileList) ValidateStoredProvider() error { return n.Validate() }
