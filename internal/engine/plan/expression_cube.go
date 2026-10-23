package plan

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// CubeComparisonExpr retains the selected installed routine in Info. All seven
// declarations execute the same typed datum comparator; Kind selects its result.
// Namespace is presentation only, never dispatch authority.
type CubeComparisonExpr struct {
	expr.Info
	Ref        ids.ObjectRef
	Args       [2]expr.Expr
	Comparison sqltype.Comparison
	Kind       uint8
	Namespace  string
}

func (e CubeComparisonExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
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
	n, err := e.Comparison.Compare(ctx.FunctionContext(), a, b, ctx.FunctionInput())
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Kind == 6 {
		return sqltype.Integer(e.Usage.Type, int64(n))
	}
	var truth bool
	switch e.Kind {
	case 0:
		truth = n == 0
	case 1:
		truth = n != 0
	case 2:
		truth = n < 0
	case 3:
		truth = n <= 0
	case 4:
		truth = n > 0
	case 5:
		truth = n >= 0
	default:
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid cube routine kind")
	}
	return sqltype.Boolean(e.Usage.Type, truth), nil
}
func (e CubeComparisonExpr) Children() []expr.Expr { return e.Args[:] }
func (e CubeComparisonExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 2 || children[0] == nil || children[1] == nil {
		return nil, fmt.Errorf("stuntgres: invalid cube routine arguments")
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
func (e CubeComparisonExpr) ValidateStoredProvider() error {
	names := [7]string{"cube_eq", "cube_ne", "cube_lt", "cube_le", "cube_gt", "cube_ge", "cube_cmp"}
	p, ok := e.RoutineReference.(*catalog.Procedure)
	if !ok || p == nil || e.Kind >= 7 || p.Name() != names[e.Kind] || !p.Strict || p.Implementation == nil || p.Builtin != nil || e.Args[0] == nil || e.Args[1] == nil {
		return fmt.Errorf("stuntgres: invalid captured cube routine")
	}
	left, right := e.Args[0].ResultUsage().Type, e.Args[1].ResultUsage().Type
	if !left.IsCube() || left != right || sqltype.ID(p.Result) != e.Usage.Type.ID() {
		return fmt.Errorf("stuntgres: invalid cube routine types")
	}
	signature := strconv.FormatUint(uint64(left.ID()), 10)
	if string(p.Signature) != signature+","+signature {
		return fmt.Errorf("stuntgres: invalid cube routine signature")
	}
	return nil
}
func (e CubeComparisonExpr) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e CubeComparisonExpr) StrictNull() expr.Strict              { return expr.StrictAfter }
func (e CubeComparisonExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(CubeComparisonExpr)
	return ok && e.RoutineReference == o.RoutineReference && e.Kind == o.Kind && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
}
func (e CubeComparisonExpr) Render() (expr.Rendering, error) {
	if err := e.ValidateStoredProvider(); err != nil {
		return expr.Rendering{}, err
	}
	p := e.RoutineReference.(*catalog.Procedure)
	name := catalog.DependencyIdentifier(e.Namespace) + "." + catalog.DependencyIdentifier(p.Name())
	args := []expr.RenderArg{{Expr: e.Args[0], Transport: expr.TransportCast, TransportType: e.Args[0].ResultUsage()}, {Expr: e.Args[1], Transport: expr.TransportCast, TransportType: e.Args[1].ResultUsage()}}
	pieces := []expr.Piece{expr.TextPiece(name + "("), expr.ArgPiece(0), expr.TextPiece(", "), expr.ArgPiece(1), expr.TextPiece(")")}
	return expr.Rendering{Tree: "CubeComparison " + name, Inputs: e.Args[:], Form: expr.FormCall, Name: name, Quote: expr.QuoteVerbatim, Args: args, Replay: expr.Replay{Pieces: pieces}}, nil
}

// InstalledCubeExpr retains procedure authority independently of type identity.
type InstalledCubeExpr struct {
	functions.Cube
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (e InstalledCubeExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	node, err := e.Cube.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	e.Cube = node.(functions.Cube)
	return e, nil
}

func (e InstalledCubeExpr) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	cube := env.Type(e.CubeType)
	changed = changed || cube != e.CubeType
	e.CubeType = cube
	next, err := e.Rebuild(info, children)
	return next, changed, err
}

func (e InstalledCubeExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}

func (e InstalledCubeExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(InstalledCubeExpr)
	if !ok || e.Procedure != o.Procedure || e.Ref != o.Ref || e.Operation != o.Operation || e.CubeType != o.CubeType || len(e.Args) != len(o.Args) {
		return false
	}
	for i, arg := range e.Args {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}
