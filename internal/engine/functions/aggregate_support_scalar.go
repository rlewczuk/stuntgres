package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindInt4Larger(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands [2]Argument
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = arg
		}
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if !visible || len(raw.Args) != 2 || len(raw.ArgNames) != 0 || raw.Variadic {
		return nil, b.CallSignatureError(raw)
	}
	target := b.Type(sqltype.Int4).Usage()
	targets := [2]sqltype.Usage{target, target}
	if !knownCallEdges(b, operands[:], targets[:]) {
		return nil, b.CallSignatureError(raw)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	var args [2]expr.Expr
	properties := expr.IntrinsicProperties(expr.ExactImmutable)
	for i := range args {
		args[i], err = b.CoerceArgument(operands[i], CoercionRequest{Intent: ConditionalArgument, Target: target, Label: "int4larger"})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	info, err := b.BuiltinRoutineInfo(b.Info(target, raw.Pos, properties), EmbeddedRoutineProvider, "int4larger", sqltype.Int4, sqltype.Int4)
	if err != nil {
		return nil, err
	}
	return Int4Larger{Info: info, Args: args}, nil
}

// Int4Larger is the strict scalar support function, not an aggregate finalizer.
type Int4Larger struct {
	expr.Info
	Args [2]expr.Expr
}

func (e Int4Larger) Children() []expr.Expr { return e.Args[:] }
func (e Int4Larger) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("Int4Larger", children, 2); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, e.ValidateStoredProvider()
}
func (e Int4Larger) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e Int4Larger) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	for i, child := range e.Args {
		if c.FunctionLiteralNull(child) {
			return sqltype.Null(e.Usage.Type), nil
		}
		values[i], err = child.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	if values[0].IsNull() || values[1].IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	left, err := values[0].Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	right, err := values[1].Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Integer(e.Usage.Type, max(left, right))
}
func (e Int4Larger) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e Int4Larger) PreflightFold() expr.Preflight        { return expr.PreflightNullOrEvaluate }
func (e Int4Larger) ValidateStoredProvider() error {
	if e.Usage.Type == nil || e.Usage.Type.ID() != sqltype.Int4 {
		return fmt.Errorf("stuntgres: invalid int4larger result")
	}
	for _, arg := range e.Args {
		if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != sqltype.Int4 {
			return fmt.Errorf("stuntgres: invalid int4larger argument")
		}
	}
	return nil
}
func (e Int4Larger) Render() (expr.Rendering, error) {
	spec := callRendering("Int4Larger", e.Children(), "int4larger", e.Children())
	spec.Replay.Validate = e.ValidateStoredProvider
	return spec, nil
}
func (e Int4Larger) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(Int4Larger)
	return ok && e.Usage == o.Usage && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
}
