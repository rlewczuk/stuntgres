package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type FTSQueryRuntime interface {
	RewriteQuerySQL(sqltype.Value, string, *sqltype.Descriptor) (sqltype.Value, error)
}

type FTSQuery struct {
	expr.Info
	Plan sqltype.FTSQueryPlan
	Args [3]expr.Expr
}

func NewFTSQuery(info expr.Info, operation sqltype.FTSQueryFunction, args [3]expr.Expr) (FTSQuery, error) {
	if operation.Name() == "" {
		return FTSQuery{}, fmt.Errorf("invalid query function")
	}
	var usages [3]sqltype.Usage
	for i, arg := range args {
		if i >= operation.Arity() {
			if arg != nil {
				return FTSQuery{}, fmt.Errorf("extra query function operand")
			}
			continue
		}
		if arg == nil {
			return FTSQuery{}, fmt.Errorf("missing query function operand")
		}
		usages[i] = arg.ResultUsage()
	}
	p, err := sqltype.BindFTSQuery(operation, info.Usage, usages)
	return FTSQuery{info, p, args}, err
}
func (e FTSQuery) Eval(c expr.Context) (sqltype.Value, error) {
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
	if e.Plan.Operation() != sqltype.FTSRewriteSQL {
		return e.Plan.Evaluate(ctx, values)
	}
	if err := e.Plan.ValidateValues(values); err != nil {
		return sqltype.Value{}, err
	}
	if values[0].IsNull() || values[1].IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	empty, err := values[0].EmptyTSQuery()
	if err != nil {
		return sqltype.Value{}, err
	}
	if empty {
		return values[0], nil
	}
	text, err := values[1].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	runtime, ok := c.(FTSQueryRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: ts_rewrite lacks statement authority")
	}
	return runtime.RewriteQuerySQL(values[0], text, e.Usage.Type)
}
func (e FTSQuery) Children() []expr.Expr { return e.Args[:e.Plan.Operation().Arity()] }
func (e FTSQuery) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("FTSQuery", children, e.Plan.Operation().Arity()); err != nil {
		return nil, err
	}
	var args [3]expr.Expr
	copy(args[:], children)
	return rebuilt(NewFTSQuery(info, e.Plan.Operation(), args))
}
func (e FTSQuery) Validate() error {
	next, err := NewFTSQuery(e.Info, e.Plan.Operation(), e.Args)
	if err != nil || next.Plan != e.Plan {
		return fmt.Errorf("invalid stored query function provider")
	}
	return nil
}
func (e FTSQuery) ValidateStoredProvider() error { return e.Validate() }
func (e FTSQuery) IntrinsicVolatility() expr.Volatility {
	if e.Plan.Operation() == sqltype.FTSRewriteSQL {
		return expr.Volatile
	}
	return expr.Immutable
}
func (e FTSQuery) PreflightFold() expr.Preflight {
	if e.Plan.Operation() == sqltype.FTSRewriteSQL {
		return expr.PreflightChildren
	}
	return expr.PreflightNullOrEvaluate
}
func (e FTSQuery) LooksLikeFunction() bool { return true }
func (e FTSQuery) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	spec := callRendering("FTS query "+e.Plan.Operation().Name(), e.Children(), e.Plan.Operation().Name(), e.Children())
	spec.Namespace = "pg_catalog"
	spec.Replay.QuoteNamespace = true
	spec.Replay.Validate = e.Validate
	return spec, nil
}
func (e FTSQuery) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(FTSQuery)
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
func (e FTSQuery) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("FTSQuery", children, e.Plan.Operation().Arity()); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}
func (FTSQuery) ClosureTransparent() {}
func (e FTSQuery) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: e.Plan.Operation().Arity(), Indexed: true}, true, nil
}
func (e FTSQuery) CaptureOperand(written int) (expr.Expr, error) {
	if written < 0 || written >= e.Plan.Operation().Arity() || e.Args[written] == nil {
		return nil, fmt.Errorf("invalid query function capture operand")
	}
	return e.Args[written], nil
}

var _ expr.Composite = FTSQuery{}
var _ expr.Renderable = FTSQuery{}
