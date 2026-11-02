package plan

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// GinEnumCompareExpr owns the selected installed declaration and concrete enum
// comparison. Namespace is presentation, never executable authority.
type GinEnumCompareExpr struct {
	expr.Info
	Ref        ids.ObjectRef
	Args       [2]expr.Expr
	Comparison sqltype.Comparison
	Namespace  string
}

func (e GinEnumCompareExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	a, err := e.Args[0].Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	b, err := e.Args[1].Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := ctx.FunctionContext().Err(); err != nil {
		return sqltype.Value{}, err
	}
	if a.IsNull() || b.IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	sign, err := e.Comparison.Compare(ctx.FunctionContext(), a, b, ctx.FunctionInput())
	if err != nil {
		return sqltype.Value{}, err
	}
	// Compare's contract is ordering, not a specific magnitude.
	if sign < 0 {
		sign = -1
	} else if sign > 0 {
		sign = 1
	}
	return sqltype.Integer(e.Usage.Type, int64(sign))
}
func (e GinEnumCompareExpr) Children() []expr.Expr { return e.Args[:] }
func (e GinEnumCompareExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 2 || children[0] == nil || children[1] == nil {
		return nil, fmt.Errorf("stuntgres: invalid enum comparator children")
	}
	e.Info = info
	e.Args = [2]expr.Expr{children[0], children[1]}
	if err := e.ValidateStoredProvider(); err != nil {
		return nil, err
	}
	comparison, err := sqltype.BindComparison(children[0].ResultUsage().Type, children[1].ResultUsage().Type, sqltype.CollationRef{}, false)
	e.Comparison = comparison
	return e, err
}
func (e GinEnumCompareExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e GinEnumCompareExpr) ValidateStoredProvider() error {
	p, ok := e.RoutineReference.(*catalog.Procedure)
	if !ok || p == nil || e.Args[0] == nil || e.Args[1] == nil || !p.Strict || p.RoutineKind != catalog.FunctionRoutine || p.Volatility != expr.Immutable || p.Implementation == nil || p.Builtin != nil || p.Signature != "3500,3500" || p.Result != 23 || !p.Origin.Valid(p.Address()) || p.Origin.Installation.Provider != "btree_gin" || p.Origin.Declaration != 1 || e.Ref.Domain() == nil || e.Ref.Epoch == 0 || e.Ref.Address != p.Address() || e.Ref.Address.SubID != 0 || p.Revision() == 0 {
		return fmt.Errorf("stuntgres: invalid captured enum comparator")
	}
	left, right := e.Args[0].ResultUsage().Type, e.Args[1].ResultUsage().Type
	if left == nil || !left.BaseType().IsEnum() || left != right || e.Usage.Type == nil || e.Usage.Type.ID() != sqltype.Int4 {
		return fmt.Errorf("stuntgres: invalid enum comparator types")
	}
	return nil
}
func (e GinEnumCompareExpr) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e GinEnumCompareExpr) StrictNull() expr.Strict              { return expr.StrictAfter }
func (e GinEnumCompareExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(GinEnumCompareExpr)
	return ok && e.RoutineReference == o.RoutineReference && e.Ref == o.Ref && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
}
func (e GinEnumCompareExpr) Render() (expr.Rendering, error) {
	if err := e.ValidateStoredProvider(); err != nil {
		return expr.Rendering{}, err
	}
	p := e.RoutineReference.(*catalog.Procedure)
	name := catalog.DependencyIdentifier(e.Namespace) + "." + catalog.DependencyIdentifier(p.Name())
	args := []expr.RenderArg{{Expr: e.Args[0], Transport: expr.TransportCast, TransportType: e.Args[0].ResultUsage()}, {Expr: e.Args[1], Transport: expr.TransportCast, TransportType: e.Args[1].ResultUsage()}}
	pieces := []expr.Piece{expr.TextPiece(name + "("), expr.ArgPiece(0), expr.TextPiece(", "), expr.ArgPiece(1), expr.TextPiece(")")}
	return expr.Rendering{Tree: "GinEnumCompare " + name, Inputs: e.Args[:], Form: expr.FormCall, Name: name, Quote: expr.QuoteVerbatim, Args: args, Replay: expr.Replay{Pieces: pieces}}, nil
}
func (e GinEnumCompareExpr) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	if err := e.ValidateStoredProvider(); err != nil {
		return expr.CaptureOperands{}, false, err
	}
	return expr.CaptureOperands{Inline: e.Args, Count: 2, SelectedImplicit: true}, true, nil
}
