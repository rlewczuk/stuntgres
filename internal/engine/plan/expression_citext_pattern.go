package plan

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// CitextPatternExpr captures the exact selected immutable installed routine.
type CitextPatternExpr struct {
	expr.Info
	Ref           ids.ObjectRef
	Args          [2]expr.Expr
	Pattern       sqltype.CitextPattern
	Text          sqltype.Usage
	Kind          uint8
	Namespace     string
	Collation     sqltype.CollationRef
	Indeterminate bool
}

func (e CitextPatternExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	a, err := e.Args[0].Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	b, err := e.Args[1].Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Pattern.Evaluate(ctx.FunctionContext(), a, b, ctx.FunctionRegexExecution())
}
func (e CitextPatternExpr) Children() []expr.Expr { return e.Args[:] }
func (e CitextPatternExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 2 || children[0] == nil || children[1] == nil {
		return nil, fmt.Errorf("stuntgres: invalid citext pattern arguments")
	}
	e.Info, e.Args = info, [2]expr.Expr{children[0], children[1]}
	if err := e.ValidateStoredProvider(); err != nil {
		return nil, err
	}
	comparison, err := sqltype.BindCitextPattern(e.Kind, children[0].ResultUsage(), children[1].ResultUsage(), info.Usage, e.Text, e.Collation, e.Indeterminate)
	e.Pattern = comparison
	return e, err
}
func (e CitextPatternExpr) ValidateStoredProvider() error {
	p, ok := e.RoutineReference.(*catalog.Procedure)
	if !ok || p == nil || e.Kind >= 4 || !p.Strict || p.RoutineKind != catalog.FunctionRoutine || p.Volatility != expr.Immutable || p.Implementation == nil || p.Builtin != nil || e.Args[0] == nil || e.Args[1] == nil || e.Ref.Address != p.Address() || e.Ref.Address.ClassID != ids.ProcedureClassID || e.Ref.Address.SubID != 0 || p.Revision() == 0 {
		return fmt.Errorf("stuntgres: invalid captured citext routine")
	}
	left, right := e.Args[0].ResultUsage().Type, e.Args[1].ResultUsage().Type
	if !left.IsCitext() || (left != right && (right == nil || !right.Concrete() || right.ID() != sqltype.Text)) || !e.Usage.Type.Concrete() || e.Usage.Type.ID() != sqltype.Bool || p.Result != ids.OID(sqltype.Bool) {
		return fmt.Errorf("stuntgres: invalid citext routine types")
	}
	signature := strconv.FormatUint(uint64(left.ID()), 10)
	if string(p.Signature) != signature+","+strconv.FormatUint(uint64(right.ID()), 10) {
		return fmt.Errorf("stuntgres: invalid citext routine signature")
	}
	return nil
}
func (e CitextPatternExpr) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e CitextPatternExpr) StrictNull() expr.Strict              { return expr.StrictAfter }
func (e CitextPatternExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(CitextPatternExpr)
	return ok && e.RoutineReference == o.RoutineReference && e.Ref == o.Ref && e.Kind == o.Kind && e.Collation == o.Collation && e.Indeterminate == o.Indeterminate && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
}
func (e CitextPatternExpr) Render() (expr.Rendering, error) {
	if err := e.ValidateStoredProvider(); err != nil {
		return expr.Rendering{}, err
	}
	p := e.RoutineReference.(*catalog.Procedure)
	name := catalog.DependencyIdentifier(e.Namespace) + "." + catalog.DependencyIdentifier(p.Name())
	args := []expr.RenderArg{{Expr: e.Args[0], Transport: expr.TransportCast, TransportType: e.Args[0].ResultUsage()}, {Expr: e.Args[1], Transport: expr.TransportCast, TransportType: e.Args[1].ResultUsage()}}
	pieces := []expr.Piece{expr.TextPiece(name + "("), expr.ArgPiece(0), expr.TextPiece(", "), expr.ArgPiece(1), expr.TextPiece(")")}
	return expr.Rendering{Tree: "CitextPattern " + name, Inputs: e.Args[:], Form: expr.FormCall, Name: name, Quote: expr.QuoteVerbatim, Args: args, Replay: expr.Replay{Pieces: pieces}}, nil
}
func (e CitextPatternExpr) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	if err := e.ValidateStoredProvider(); err != nil {
		return expr.CaptureOperands{}, false, err
	}
	return expr.CaptureOperands{Inline: e.Args, Count: 2, SelectedImplicit: true}, true, nil
}
