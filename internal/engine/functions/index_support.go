package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// IntegerIndexSupport is a scalar declaration wrapper, not the index hot path.
type IntegerIndexSupport struct {
	expr.Info
	OID  uint32
	Args [2]expr.Expr
}

func (e IntegerIndexSupport) arity() int {
	if e.OID == 450 {
		return 1
	}
	return 2
}
func (e IntegerIndexSupport) Children() []expr.Expr { return e.Args[:e.arity()] }
func (e IntegerIndexSupport) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("IntegerIndexSupport", children, e.arity()); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, e.ValidateStoredProvider()
}
func (e IntegerIndexSupport) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e IntegerIndexSupport) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	for i, arg := range e.Children() {
		if c.FunctionLiteralNull(arg) {
			return sqltype.Null(e.Usage.Type), nil
		}
		values[i], err = arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.IntegerIndexSupport(ctx, e.OID, e.Usage.Type, values[0], values[1])
}
func (e IntegerIndexSupport) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e IntegerIndexSupport) PreflightFold() expr.Preflight        { return expr.PreflightNullOrEvaluate }
func (e IntegerIndexSupport) ValidateStoredProvider() error {
	if e.OID != 351 && e.OID != 450 && e.OID != 425 {
		return fmt.Errorf("stuntgres: invalid integer index support routine")
	}
	for i, arg := range e.Children() {
		expected := sqltype.Int4
		if e.OID == 425 && i == 1 {
			expected = sqltype.Int8
		}
		if arg == nil || arg.ResultUsage().Type.ID() != expected {
			return fmt.Errorf("stuntgres: invalid integer support argument")
		}
	}
	expected := sqltype.Int4
	if e.OID == 425 {
		expected = sqltype.Int8
	}
	if e.Usage.Type.ID() != expected {
		return fmt.Errorf("stuntgres: invalid integer support result")
	}
	return nil
}
func (e IntegerIndexSupport) Render() (expr.Rendering, error) {
	n, _ := sqltype.NativeIndexRoutineByOID(e.OID)
	spec := callRendering("IntegerIndexSupport", e.Children(), n.Name, e.Children())
	spec.Replay.Validate = e.ValidateStoredProvider
	return spec, nil
}
func (e IntegerIndexSupport) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(IntegerIndexSupport)
	if !ok || o.OID != e.OID {
		return false
	}
	for i, arg := range e.Children() {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}
func (e IntegerIndexSupport) LooksLikeFunction() bool { return true }
func (e IntegerIndexSupport) ClosureTransparent()     {}
func (e IntegerIndexSupport) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Inline: e.Args, Count: e.arity()}, true, nil
}
