package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type FTSHeadlineRuntime interface {
	HeadlineTextSearch(bool, sqltype.Value, sqltype.Value, sqltype.Value, sqltype.Value, *sqltype.Descriptor) (sqltype.Value, error)
}
type FTSHeadline struct {
	expr.Info
	Plan sqltype.FTSHeadlinePlan
	Args [4]expr.Expr
}

func NewFTSHeadline(info expr.Info, op sqltype.FTSHeadlineFunction, args [4]expr.Expr) (FTSHeadline, error) {
	var usages [4]sqltype.Usage
	for i, arg := range args {
		if i >= op.Arity() {
			if arg != nil {
				return FTSHeadline{}, fmt.Errorf("extra headline operand")
			}
			continue
		}
		if arg == nil {
			return FTSHeadline{}, fmt.Errorf("missing headline operand")
		}
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindFTSHeadline(op, info.Usage, usages)
	return FTSHeadline{info, plan, args}, err
}
func (e FTSHeadline) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.Children() {
		if c.FunctionLiteralNull(arg) {
			return sqltype.Null(e.Usage.Type), ctx.Err()
		}
	}
	var values [4]sqltype.Value
	for i, arg := range e.Children() {
		values[i], err = arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if err := e.Plan.ValidateValues(ctx, values); err != nil {
		return sqltype.Value{}, err
	}
	for _, v := range values[:e.Plan.Operation().Arity()] {
		if v.IsNull() {
			return sqltype.Null(e.Usage.Type), ctx.Err()
		}
	}
	runtime, ok := c.(FTSHeadlineRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: headline lacks statement authority")
	}
	index := 0
	var config sqltype.Value
	if e.Plan.Operation().Explicit {
		config = values[0]
		index = 1
	}
	options := sqltype.String(e.Usage.Type, "")
	if e.Plan.Operation().Options {
		options = values[index+2]
	}
	return runtime.HeadlineTextSearch(e.Plan.Operation().Explicit, config, values[index], values[index+1], options, e.Usage.Type)
}
func (e FTSHeadline) Children() []expr.Expr { return e.Args[:e.Plan.Operation().Arity()] }
func (e FTSHeadline) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("FTSHeadline", children, e.Plan.Operation().Arity()); err != nil {
		return nil, err
	}
	var args [4]expr.Expr
	copy(args[:], children)
	return rebuilt(NewFTSHeadline(info, e.Plan.Operation(), args))
}
func (e FTSHeadline) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e FTSHeadline) Validate() error {
	next, err := NewFTSHeadline(e.Info, e.Plan.Operation(), e.Args)
	if err != nil || next.Plan != e.Plan {
		return fmt.Errorf("invalid stored headline provider")
	}
	return nil
}
func (e FTSHeadline) ValidateStoredProvider() error { return e.Validate() }
func (e FTSHeadline) IntrinsicVolatility() expr.Volatility {
	if e.Plan.Operation().Explicit {
		return expr.Immutable
	}
	return expr.Stable
}
func (e FTSHeadline) PreflightFold() expr.Preflight { return expr.PreflightNullOrEvaluate }
func (e FTSHeadline) LooksLikeFunction() bool       { return true }
func (FTSHeadline) ClosureTransparent()             {}
func (e FTSHeadline) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	spec := callRendering("FTS headline", e.Children(), "ts_headline", e.Children())
	spec.Namespace = "pg_catalog"
	spec.Replay.QuoteNamespace = true
	spec.Replay.Validate = e.Validate
	return spec, nil
}
func (e FTSHeadline) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(FTSHeadline)
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
func (e FTSHeadline) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: e.Plan.Operation().Arity(), Indexed: true}, true, nil
}
func (e FTSHeadline) CaptureOperand(written int) (expr.Expr, error) {
	if written < 0 || written >= e.Plan.Operation().Arity() || e.Args[written] == nil {
		return nil, fmt.Errorf("invalid headline capture operand")
	}
	return e.Args[written], nil
}

var _ expr.Composite = FTSHeadline{}
var _ expr.Renderable = FTSHeadline{}
var _ expr.StatementJumbler = FTSHeadline{}
