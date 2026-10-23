package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (n snapshotExpression) validate() error {
	if n.kind < snapshotCurrent || n.kind > snapshotVisible || !n.Usage.Valid() {
		return fmt.Errorf("stuntgres: invalid snapshot provider")
	}
	formals := snapshotFormals(n.kind)
	if len(n.args) != len(formals) {
		return fmt.Errorf("stuntgres: invalid snapshot arity")
	}
	for i, arg := range n.args {
		if arg == nil || arg.ResultUsage().Type.BaseType().ID() != formals[i] {
			return fmt.Errorf("stuntgres: invalid snapshot argument")
		}
	}
	result := sqltype.Xid8
	if n.kind == snapshotCurrent {
		result = sqltype.PgSnapshot
	} else if n.kind == snapshotVisible {
		result = sqltype.Bool
	}
	if n.Usage.Type.ID() != result {
		return fmt.Errorf("stuntgres: invalid snapshot result")
	}
	return nil
}
func (n snapshotExpression) Children() []expr.Expr { return n.args }
func (n snapshotExpression) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(n.args) {
		return nil, fmt.Errorf("stuntgres: invalid snapshot children")
	}
	for _, child := range children {
		if child == nil {
			return nil, fmt.Errorf("stuntgres: nil snapshot child")
		}
	}
	n.Info = info
	n.args = append([]expr.Expr(nil), children...)
	return n, nil
}
func (n snapshotExpression) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(snapshotExpression)
	if err = rebuilt.validate(); err != nil {
		return nil, err
	}
	return rebuilt, nil
}
func (n snapshotExpression) IntrinsicVolatility() expr.Volatility {
	if n.kind == snapshotCurrent {
		return expr.Stable
	}
	return expr.Immutable
}
func (n snapshotExpression) PlanningFold() expr.Fold {
	if n.kind == snapshotCurrent || n.kind == snapshotXip {
		return expr.FoldNever
	}
	return expr.FoldConstant
}
func (n snapshotExpression) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(snapshotExpression)
	return ok && n.kind == snapshotXip && o.kind == snapshotXip && n.ResultUsage() == o.ResultUsage()
}
func (n snapshotExpression) PreflightFold() expr.Preflight { return expr.PreflightChildren }
func (n snapshotExpression) LooksLikeFunction() bool       { return true }
func (n snapshotExpression) ValidateStoredProvider() error { return n.validate() }
func (n snapshotExpression) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(snapshotExpression)
	if !ok || n.kind != o.kind || len(n.args) != len(o.args) {
		return false
	}
	for i, arg := range n.args {
		if !same(arg, o.args[i]) {
			return false
		}
	}
	return true
}
func (n snapshotExpression) ClosedDefault(closed func(expr.Expr) bool) bool {
	for _, arg := range n.args {
		if !closed(arg) {
			return false
		}
	}
	return true
}
func (n snapshotExpression) Render() (expr.Rendering, error) {
	if err := n.validate(); err != nil {
		return expr.Rendering{}, err
	}
	args := make([]expr.RenderArg, len(n.args))
	for i, arg := range n.args {
		args[i] = expr.RenderArg{Expr: arg}
	}
	return expr.Rendering{Tree: "Snapshot " + snapshotNames[n.kind], Inputs: n.Children(), Form: expr.FormCall, Name: snapshotNames[n.kind], Quote: expr.QuoteOnRequest, Args: args, Replay: expr.Replay{Validate: n.validate}}, nil
}
func (n snapshotExpression) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	result := expr.CaptureOperands{Count: len(n.args)}
	copy(result.Inline[:], n.args)
	return result, true, nil
}
