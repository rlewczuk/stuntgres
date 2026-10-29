package plan

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InsertUsernameExpr is an ordinary SQL call, never a borrowed trigger frame.
// RoutineReference and Ref retain the same immutable selected provider carrier
// used by shared routine admission, remapping and dependency validation.
type InsertUsernameExpr struct {
	expr.Info
	Ref ids.ObjectRef
	Namespace string
}

func (n InsertUsernameExpr) ValidateStoredProvider() error {
	p, ok := n.RoutineReference.(*catalog.Procedure)
	if !ok || p == nil || p.Builtin != nil || !catalog.NativeDMLTrigger(p) || !p.Origin.Valid(p.Address()) || n.Ref == (ids.ObjectRef{}) || n.Ref.Address != p.Address() || n.Usage.Type == nil || n.Usage.Type.ID() != 2279 || n.Properties().Volatility != expr.Volatile || n.Namespace == "" {
		return fmt.Errorf("stuntgres: invalid insert_username call recipe")
	}
	return nil
}
func (n InsertUsernameExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil { return sqltype.Value{}, err }
	return runtime.EvalInsertUsername(n)
}
func (n InsertUsernameExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	v, err := n.Eval(ctx)
	if err != nil { return expr.SourceCursor{}, err }
	return expr.ScalarSource(v), nil
}
func (InsertUsernameExpr) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (InsertUsernameExpr) PlanningFold() expr.Fold { return expr.FoldNever }
func (InsertUsernameExpr) PreflightFold() expr.Preflight { return expr.PreflightKeep }
func (InsertUsernameExpr) LooksLikeFunction() bool { return true }
func (InsertUsernameExpr) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (InsertUsernameExpr) SourceAdmission() (bool, bool) { return true, false }
func (InsertUsernameExpr) Children() []expr.Expr { return nil }
func (InsertUsernameExpr) GroupLeaf() {}
func (n InsertUsernameExpr) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(InsertUsernameExpr)
	return ok && n.RoutineReference == o.RoutineReference && n.Ref == o.Ref
}
func (n InsertUsernameExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 { return nil, fmt.Errorf("stuntgres: invalid insert_username call children") }
	n.Info = info
	return n, n.ValidateStoredProvider()
}
func (n InsertUsernameExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n InsertUsernameExpr) Render() (expr.Rendering, error) {
	if err := n.ValidateStoredProvider(); err != nil { return expr.Rendering{}, err }
	p := n.RoutineReference.(*catalog.Procedure)
	name := catalog.DependencyIdentifier(n.Namespace)+"."+catalog.DependencyIdentifier(p.Name())
	return expr.Rendering{Tree: "InsertUsername " + name, Form: expr.FormCall, Name: name, Quote: expr.QuoteVerbatim, Replay: expr.Replay{Pieces: []expr.Piece{expr.TextPiece(name+"()")}}}, nil
}
func (n InsertUsernameExpr) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction { return expr.CaptureOperands{}, false, nil }
	return expr.CaptureOperands{}, true, n.ValidateStoredProvider()
}
