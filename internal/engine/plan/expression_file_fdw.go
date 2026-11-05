package plan

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// FileFDWExpr owns only typed children and the original installed declaration.
// No parsed options, effective role, session or invocation authority is cached.
type FileFDWExpr struct {
	expr.Info
	Ref ids.ObjectRef
	Namespace string
	Args []expr.Expr
}

func (n FileFDWExpr) ValidateStoredProvider() error {
	p, ok := n.RoutineReference.(*catalog.Procedure)
	if !ok || p == nil || !p.Origin.Valid(p.Address()) || p.Origin.Installation.Provider != "file_fdw" || p.Origin.Declaration > 1 || n.Ref == (ids.ObjectRef{}) || n.Ref.Address != p.Address() || n.Namespace == "" || n.Usage.Type == nil || ids.OID(n.Usage.Type.ID()) != p.Result || n.Properties().Volatility != expr.Volatile { return fmt.Errorf("stuntgres: invalid installed file_fdw call") }
	count := 0
	if p.Origin.Declaration == 1 { count = 2 }
	if len(n.Args) != count { return fmt.Errorf("stuntgres: invalid file_fdw call operands") }
	if count == 2 && (n.Args[0].ResultUsage().Type.ID() != 1009 || n.Args[1].ResultUsage().Type.ID() != 26) { return fmt.Errorf("stuntgres: invalid file_fdw operand types") }
	return nil
}
func (n FileFDWExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, ok := ctx.(interface { EvalFileFDW(FileFDWExpr) (sqltype.Value, error) })
	if !ok { return sqltype.Value{}, fmt.Errorf("stuntgres: missing file_fdw runtime") }
	return runtime.EvalFileFDW(n)
}
func (n FileFDWExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil { return expr.SourceCursor{}, err }
	return expr.ScalarSource(value), nil
}
func (FileFDWExpr) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (FileFDWExpr) PlanningFold() expr.Fold { return expr.FoldNever }
func (FileFDWExpr) PreflightFold() expr.Preflight { return expr.PreflightKeep }
func (FileFDWExpr) LooksLikeFunction() bool { return true }
func (FileFDWExpr) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (FileFDWExpr) SourceAdmission() (bool, bool) { return true, false }
func (n FileFDWExpr) Children() []expr.Expr { return n.Args }
func (n FileFDWExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(FileFDWExpr)
	if !ok || n.Ref != o.Ref || n.RoutineReference != o.RoutineReference || len(n.Args) != len(o.Args) { return false }
	for i, child := range n.Args { if !same(child, o.Args[i]) { return false } }
	return true
}
func (n FileFDWExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	n.Info, n.Args = info, children
	return n, n.ValidateStoredProvider()
}
func (n FileFDWExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) { return n.Rebuild(info, children) }
func (n FileFDWExpr) Render() (expr.Rendering, error) {
	if err := n.ValidateStoredProvider(); err != nil { return expr.Rendering{}, err }
	p := n.RoutineReference.(*catalog.Procedure)
	name := catalog.DependencyIdentifier(n.Namespace)+"."+catalog.DependencyIdentifier(p.Name())
	args := make([]expr.RenderArg, len(n.Args))
	pieces := []expr.Piece{expr.TextPiece(name+"(")}
	for i, child := range n.Args {
		args[i] = expr.RenderArg{Expr: child}
		if i != 0 { pieces = append(pieces, expr.TextPiece(", ")) }
		pieces = append(pieces, expr.ArgPiece(i))
	}
	pieces = append(pieces, expr.TextPiece(")"))
	return expr.Rendering{Tree: "FileFDW "+name, Inputs: n.Args, Form: expr.FormCall, Name: name, Quote: expr.QuoteVerbatim, Args: args, Replay: expr.Replay{Pieces: pieces}}, nil
}
func (n FileFDWExpr) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction { return expr.CaptureOperands{}, false, nil }
	return expr.CaptureOperands{Args: n.Args, Count: len(n.Args)}, true, n.ValidateStoredProvider()
}
func (FileFDWExpr) JumbleFields(s expr.StatementFields) { s.JumbleText("plan.FileFDWExpr") }
