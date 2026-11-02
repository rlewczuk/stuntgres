package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// Macaddr8Compare is the released scalar routine4119, not a B-tree capability.
// Its bound comparison retains the exact operand generations and unsigned kernel.
type Macaddr8Compare struct {
	expr.Info
	Args       [2]expr.Expr
	comparison sqltype.Comparison
}

func NewMacaddr8Compare(info expr.Info, args [2]expr.Expr) (Macaddr8Compare, error) {
	if info.Usage.Type == nil || info.Usage.Type.ID() != sqltype.Int4 || missingArgument(args[:]) {
		return Macaddr8Compare{}, fmt.Errorf("stuntgres: invalid macaddr8 comparison declaration")
	}
	left, right := args[0].ResultUsage(), args[1].ResultUsage()
	if left.Type == nil || right.Type == nil || left.Type.ID() != sqltype.Macaddr8 || right.Type.ID() != sqltype.Macaddr8 {
		return Macaddr8Compare{}, fmt.Errorf("stuntgres: invalid macaddr8 comparison operands")
	}
	comparison, err := sqltype.BindComparison(left.Type, right.Type, left.Collation, false)
	if err != nil {
		return Macaddr8Compare{}, err
	}
	return Macaddr8Compare{Info: info, Args: args, comparison: comparison}, nil
}

func (e Macaddr8Compare) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.Args {
		if c.FunctionLiteralNull(arg) {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	var values [2]sqltype.Value
	for i, arg := range e.Args {
		values[i], err = arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if values[0].IsNull() || values[1].IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	sign, err := e.comparison.Compare(ctx, values[0], values[1], sqltype.InputEnvironment{})
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Integer(e.Usage.Type, int64(sign))
}
func (e Macaddr8Compare) Children() []expr.Expr { return e.Args[:] }
func (e Macaddr8Compare) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("Macaddr8Compare", children, len(e.Args)); err != nil {
		return nil, err
	}
	args := e.Args
	copy(args[:], children)
	return rebuilt(NewMacaddr8Compare(info, args))
}
func (e Macaddr8Compare) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e Macaddr8Compare) ValidateStoredProvider() error {
	current, err := NewMacaddr8Compare(e.Info, e.Args)
	if err != nil || !current.comparison.Equal(e.comparison) {
		return fmt.Errorf("stuntgres: invalid stored macaddr8 comparison provider")
	}
	return nil
}
func (e Macaddr8Compare) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e Macaddr8Compare) PreflightFold() expr.Preflight        { return expr.PreflightNullOrEvaluate }
func (e Macaddr8Compare) Render() (expr.Rendering, error) {
	spec := callRendering("Macaddr8Compare", e.Args[:], "macaddr8_cmp", e.Args[:])
	spec.Namespace = "pg_catalog"
	spec.Replay.QuoteNamespace = true
	spec.Replay.Validate = e.ValidateStoredProvider
	return spec, nil
}
func (e Macaddr8Compare) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(Macaddr8Compare)
	return ok && e.comparison.Equal(o.comparison) && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
}
func (e Macaddr8Compare) LooksLikeFunction() bool { return true }
func (e Macaddr8Compare) ClosureTransparent()     {}
func (e Macaddr8Compare) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Inline: e.Args, Count: 2}, true, nil
}
