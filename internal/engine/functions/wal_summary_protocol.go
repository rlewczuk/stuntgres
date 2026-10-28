package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

func (WALSummary) IntrinsicVolatility() expr.Volatility    { return expr.Volatile }
func (WALSummary) PlanningFold() expr.Fold                 { return expr.FoldNever }
func (WALSummary) PreflightFold() expr.Preflight           { return expr.PreflightChildren }
func (WALSummary) LooksLikeFunction() bool                 { return true }
func (WALSummary) CallCost() int                           { return 1 }
func (WALSummary) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (n WALSummary) SourceAdmission() (bool, bool)         { return true, n.Kind != WALSummaryState }
func (n WALSummary) Children() []expr.Expr                 { return n.Args[:n.Arity] }
func (n WALSummary) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(WALSummary)
	return ok && n.Kind == o.Kind && n.Arity == o.Arity && n.Slots == o.Slots && n.ResultUsage() == o.ResultUsage()
}
func (n WALSummary) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(WALSummary)
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
func (n WALSummary) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != int(n.Arity) {
		return nil, fmt.Errorf("stuntgres: invalid WAL summary reconstruction")
	}
	n.Info = info
	for i, child := range children {
		if child == nil {
			return nil, fmt.Errorf("stuntgres: nil WAL summary operand")
		}
		n.Args[i] = child
	}
	if err := n.Validate(); err != nil {
		return nil, err
	}
	return n, nil
}
func (n WALSummary) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Substitute(info, children)
}
func (n WALSummary) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	args := make([]expr.RenderArg, n.Arity)
	reordered := false
	for i := range int(n.Arity) {
		reordered = reordered || n.Slots[i] != uint8(i)
	}
	for i := range args {
		args[i] = expr.RenderArg{Expr: n.Args[i]}
		if reordered {
			args[i].Label = walSummaryLabels[n.Slots[i]]
		}
	}
	return expr.Rendering{Tree: "WALSummary", Inputs: n.Children(), Form: expr.FormCall, Name: n.Kind.Name(), Args: args}, nil
}
func (n WALSummary) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if err := n.Validate(); err != nil {
		return expr.CaptureOperands{}, false, err
	}
	return expr.CaptureOperands{Count: int(n.Arity), Indexed: true}, kind == expr.CaptureFunction, nil
}
func (n WALSummary) CaptureOperand(written int) (expr.Expr, error) {
	if written < 0 || written >= int(n.Arity) {
		return nil, fmt.Errorf("stuntgres: invalid WAL summary capture")
	}
	return n.Args[written], nil
}
func (n WALSummary) ValidateStoredProvider() error { return n.Validate() }
