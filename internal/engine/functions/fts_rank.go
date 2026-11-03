package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type FTSRank struct {
	expr.Info
	Plan sqltype.FTSRankPlan
	Args [4]expr.Expr
}

func NewFTSRank(info expr.Info, op sqltype.FTSRankFunction, args [4]expr.Expr) (FTSRank, error) {
	var usages [4]sqltype.Usage
	for i, arg := range args {
		if i >= op.Arity() {
			if arg != nil {
				return FTSRank{}, fmt.Errorf("extra rank operand")
			}
			continue
		}
		if arg == nil {
			return FTSRank{}, fmt.Errorf("missing rank operand")
		}
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindFTSRank(op, info.Usage, usages)
	return FTSRank{info, plan, args}, err
}
func (e FTSRank) Eval(c expr.Context) (sqltype.Value, error) {
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
	return e.Plan.Evaluate(ctx, values)
}
func (e FTSRank) Children() []expr.Expr { return e.Args[:e.Plan.Operation().Arity()] }
func (e FTSRank) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("FTSRank", children, e.Plan.Operation().Arity()); err != nil {
		return nil, err
	}
	var args [4]expr.Expr
	copy(args[:], children)
	return rebuilt(NewFTSRank(info, e.Plan.Operation(), args))
}
func (e FTSRank) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e FTSRank) Validate() error {
	next, err := NewFTSRank(e.Info, e.Plan.Operation(), e.Args)
	if err != nil || next.Plan != e.Plan {
		return fmt.Errorf("invalid stored rank provider")
	}
	return nil
}
func (e FTSRank) ValidateStoredProvider() error        { return e.Validate() }
func (e FTSRank) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e FTSRank) PreflightFold() expr.Preflight        { return expr.PreflightNullOrEvaluate }
func (e FTSRank) LooksLikeFunction() bool              { return true }
func (FTSRank) ClosureTransparent()                    {}
func (e FTSRank) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	spec := callRendering("FTS rank", e.Children(), e.Plan.Operation().Name(), e.Children())
	spec.Namespace = "pg_catalog"
	spec.Replay.QuoteNamespace = true
	spec.Replay.Validate = e.Validate
	return spec, nil
}
func (e FTSRank) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(FTSRank)
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
func (e FTSRank) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: e.Plan.Operation().Arity(), Indexed: true}, true, nil
}
func (e FTSRank) CaptureOperand(written int) (expr.Expr, error) {
	if written < 0 || written >= e.Plan.Operation().Arity() || e.Args[written] == nil {
		return nil, fmt.Errorf("invalid rank capture operand")
	}
	return e.Args[written], nil
}

var _ expr.Composite = FTSRank{}
var _ expr.Renderable = FTSRank{}
var _ expr.StatementJumbler = FTSRank{}
