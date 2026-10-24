package plan

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// CitextComparisonExpr captures the exact selected immutable installed routine.
type CitextComparisonExpr struct {
	expr.Info
	Ref           ids.ObjectRef
	Args          [2]expr.Expr
	Comparison    sqltype.CitextComparison
	Kind          uint8
	Namespace     string
	Collation     sqltype.CollationRef
	Indeterminate bool
}

func (e CitextComparisonExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	a, err := e.Args[0].Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	b, err := e.Args[1].Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Comparison.Evaluate(ctx.FunctionContext(), a, b)
}
func (e CitextComparisonExpr) Children() []expr.Expr { return e.Args[:] }
func (e CitextComparisonExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 2 || children[0] == nil || children[1] == nil {
		return nil, fmt.Errorf("stuntgres: invalid citext comparison arguments")
	}
	e.Info, e.Args = info, [2]expr.Expr{children[0], children[1]}
	if err := e.ValidateStoredProvider(); err != nil {
		return nil, err
	}
	comparison, err := sqltype.BindCitextComparison(sqltype.ComparisonOperator(e.Kind), children[0].ResultUsage().Type, children[1].ResultUsage().Type, info.Usage.Type, e.Collation, e.Indeterminate)
	e.Comparison = comparison
	return e, err
}
func (e CitextComparisonExpr) ValidateStoredProvider() error {
	p, ok := e.RoutineReference.(*catalog.Procedure)
	if !ok || p == nil || e.Kind >= 6 || !p.Strict || p.RoutineKind != catalog.FunctionRoutine || p.Volatility != expr.Immutable || p.Implementation == nil || p.Builtin != nil || e.Args[0] == nil || e.Args[1] == nil || e.Ref.Address != p.Address() || e.Ref.Address.ClassID != ids.ProcedureClassID || e.Ref.Address.SubID != 0 || p.Revision() == 0 {
		return fmt.Errorf("stuntgres: invalid captured citext routine")
	}
	left, right := e.Args[0].ResultUsage().Type, e.Args[1].ResultUsage().Type
	if !left.IsCitext() || left != right || !e.Usage.Type.Concrete() || e.Usage.Type.ID() != sqltype.Bool || p.Result != ids.OID(sqltype.Bool) {
		return fmt.Errorf("stuntgres: invalid citext routine types")
	}
	signature := strconv.FormatUint(uint64(left.ID()), 10)
	if string(p.Signature) != signature+","+signature {
		return fmt.Errorf("stuntgres: invalid citext routine signature")
	}
	return nil
}
func (e CitextComparisonExpr) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e CitextComparisonExpr) StrictNull() expr.Strict              { return expr.StrictAfter }
func (e CitextComparisonExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(CitextComparisonExpr)
	return ok && e.RoutineReference == o.RoutineReference && e.Ref == o.Ref && e.Kind == o.Kind && e.Collation == o.Collation && e.Indeterminate == o.Indeterminate && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
}
func (e CitextComparisonExpr) Render() (expr.Rendering, error) {
	if err := e.ValidateStoredProvider(); err != nil {
		return expr.Rendering{}, err
	}
	p := e.RoutineReference.(*catalog.Procedure)
	name := catalog.DependencyIdentifier(e.Namespace) + "." + catalog.DependencyIdentifier(p.Name())
	args := []expr.RenderArg{{Expr: e.Args[0], Transport: expr.TransportCast, TransportType: e.Args[0].ResultUsage()}, {Expr: e.Args[1], Transport: expr.TransportCast, TransportType: e.Args[1].ResultUsage()}}
	pieces := []expr.Piece{expr.TextPiece(name + "("), expr.ArgPiece(0), expr.TextPiece(", "), expr.ArgPiece(1), expr.TextPiece(")")}
	return expr.Rendering{Tree: "CitextComparison " + name, Inputs: e.Args[:], Form: expr.FormCall, Name: name, Quote: expr.QuoteVerbatim, Args: args, Replay: expr.Replay{Pieces: pieces}}, nil
}
func (e CitextComparisonExpr) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	if err := e.ValidateStoredProvider(); err != nil {
		return expr.CaptureOperands{}, false, err
	}
	return expr.CaptureOperands{Inline: e.Args, Count: 2, SelectedImplicit: true}, true, nil
}
