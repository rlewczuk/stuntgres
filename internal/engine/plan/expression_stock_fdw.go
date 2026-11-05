package plan

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// StockFDWExpr owns typed children and an authentic stock FDW declaration.
// No parsed options, effective role or invocation authority is cached.
type StockFDWExpr struct {
	expr.Info
	Ref       ids.ObjectRef
	Namespace string
	Args      []expr.Expr
}

func (n StockFDWExpr) ValidateStoredProvider() error {
	p, ok := n.RoutineReference.(*catalog.Procedure)
	if !ok || p == nil || !p.Origin.Valid(p.Address()) || (p.Origin.Installation.Provider != "file_fdw" && p.Origin.Installation.Provider != "postgres_fdw") || p.Origin.Declaration > 1 || n.Ref == (ids.ObjectRef{}) || n.Ref.Address != p.Address() || n.Namespace == "" || n.Usage.Type == nil || ids.OID(n.Usage.Type.ID()) != p.Result || n.Properties().Volatility != expr.Volatile {
		return fmt.Errorf("stuntgres: invalid installed stock FDW call")
	}
	count := 0
	if p.Origin.Declaration == 1 {
		count = 2
	}
	if len(n.Args) != count {
		return fmt.Errorf("stuntgres: invalid stock FDW call operands")
	}
	if count == 2 && (n.Args[0].ResultUsage().Type.ID() != 1009 || n.Args[1].ResultUsage().Type.ID() != 26) {
		return fmt.Errorf("stuntgres: invalid stock FDW operand types")
	}
	return nil
}
func (n StockFDWExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, ok := ctx.(interface {
		EvalStockFDW(StockFDWExpr) (sqltype.Value, error)
	})
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing stock FDW runtime")
	}
	return runtime.EvalStockFDW(n)
}
func (n StockFDWExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
func (StockFDWExpr) IntrinsicVolatility() expr.Volatility    { return expr.Volatile }
func (StockFDWExpr) PlanningFold() expr.Fold                 { return expr.FoldNever }
func (StockFDWExpr) PreflightFold() expr.Preflight           { return expr.PreflightKeep }
func (StockFDWExpr) LooksLikeFunction() bool                 { return true }
func (StockFDWExpr) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (StockFDWExpr) SourceAdmission() (bool, bool)           { return true, false }
func (n StockFDWExpr) Children() []expr.Expr                 { return n.Args }
func (n StockFDWExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(StockFDWExpr)
	if !ok || n.Ref != o.Ref || n.RoutineReference != o.RoutineReference || len(n.Args) != len(o.Args) {
		return false
	}
	for i, child := range n.Args {
		if !same(child, o.Args[i]) {
			return false
		}
	}
	return true
}
func (n StockFDWExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	n.Info, n.Args = info, children
	return n, n.ValidateStoredProvider()
}
func (n StockFDWExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n StockFDWExpr) Render() (expr.Rendering, error) {
	if err := n.ValidateStoredProvider(); err != nil {
		return expr.Rendering{}, err
	}
	p := n.RoutineReference.(*catalog.Procedure)
	name := catalog.DependencyIdentifier(n.Namespace) + "." + catalog.DependencyIdentifier(p.Name())
	args := make([]expr.RenderArg, len(n.Args))
	pieces := []expr.Piece{expr.TextPiece(name + "(")}
	for i, child := range n.Args {
		args[i] = expr.RenderArg{Expr: child}
		if i != 0 {
			pieces = append(pieces, expr.TextPiece(", "))
		}
		pieces = append(pieces, expr.ArgPiece(i))
	}
	pieces = append(pieces, expr.TextPiece(")"))
	return expr.Rendering{Tree: "StockFDW " + name, Inputs: n.Args, Form: expr.FormCall, Name: name, Quote: expr.QuoteVerbatim, Args: args, Replay: expr.Replay{Pieces: pieces}}, nil
}
func (n StockFDWExpr) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Args: n.Args, Count: len(n.Args)}, true, n.ValidateStoredProvider()
}
func (StockFDWExpr) JumbleFields(s expr.StatementFields) { s.JumbleText("plan.StockFDWExpr") }
