package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type FTSVector struct {
	expr.Info
	Plan sqltype.FTSVectorPlan
	Args [3]expr.Expr
}

func NewFTSVector(info expr.Info, operation sqltype.FTSVectorFunction, args [3]expr.Expr) (FTSVector, error) {
	if operation.Name() == "" {
		return FTSVector{}, fmt.Errorf("invalid vector function")
	}
	var usages [3]sqltype.Usage
	for i, arg := range args {
		if i >= operation.Arity() {
			if arg != nil {
				return FTSVector{}, fmt.Errorf("extra vector function operand")
			}
			continue
		}
		if arg == nil {
			return FTSVector{}, fmt.Errorf("missing vector function operand")
		}
		usages[i] = arg.ResultUsage()
	}
	p, err := sqltype.BindFTSVector(operation, info.Usage, usages)
	return FTSVector{info, p, args}, err
}
func (e FTSVector) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.Children() {
		if c.FunctionLiteralNull(arg) {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	var values [3]sqltype.Value
	for i, arg := range e.Children() {
		values[i], err = arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return e.Plan.Evaluate(ctx, values)
}
func (e FTSVector) Children() []expr.Expr { return e.Args[:e.Plan.Operation().Arity()] }
func (e FTSVector) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("FTSVector", children, e.Plan.Operation().Arity()); err != nil {
		return nil, err
	}
	var args [3]expr.Expr
	copy(args[:], children)
	return rebuilt(NewFTSVector(info, e.Plan.Operation(), args))
}
func (e FTSVector) Validate() error {
	next, err := NewFTSVector(e.Info, e.Plan.Operation(), e.Args)
	if err != nil || next.Plan != e.Plan {
		return fmt.Errorf("invalid stored vector function provider")
	}
	return nil
}
func (e FTSVector) ValidateStoredProvider() error { return e.Validate() }
func (e FTSVector) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e FTSVector) PreflightFold() expr.Preflight { return expr.PreflightNullOrEvaluate }
func (e FTSVector) LooksLikeFunction() bool { return true }
func (e FTSVector) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	spec := callRendering("FTS vector "+e.Plan.Operation().Name(), e.Children(), e.Plan.Operation().Name(), e.Children())
	spec.Namespace = "pg_catalog"
	spec.Replay.QuoteNamespace = true
	spec.Replay.Validate = e.Validate
	return spec, nil
}
func (e FTSVector) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(FTSVector)
	if !ok || e.Plan != o.Plan {
		return false
	}
	for i, arg := range e.Children() {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}
func (e FTSVector) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("FTSVector", children, e.Plan.Operation().Arity()); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}
func (FTSVector) ClosureTransparent() {}
func (e FTSVector) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: e.Plan.Operation().Arity(), Indexed: true}, true, nil
}
func (e FTSVector) CaptureOperand(written int) (expr.Expr, error) {
	if written < 0 || written >= e.Plan.Operation().Arity() || e.Args[written] == nil {
		return nil, fmt.Errorf("invalid vector function capture operand")
	}
	return e.Args[written], nil
}

var _ expr.Composite = FTSVector{}
var _ expr.Renderable = FTSVector{}
