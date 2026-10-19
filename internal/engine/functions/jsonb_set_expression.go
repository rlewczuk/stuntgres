package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// JSONBSet keeps mutation-family executable formals separate from written presentation.
// Defaults are real child expressions; all supplied children evaluate normally.
type JSONBSet struct {
	expr.Info
	Plan         sqltype.JSONBSetPlan
	Args         [5]expr.Expr
	Count        int
	WrittenCount int
	WrittenSlots [5]uint8
	Labels       [5]string
	Variadic     bool
}

func (e JSONBSet) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var values [5]sqltype.Value
	var supplied [5]bool
	for i := range e.WrittenCount {
		slot := e.WrittenSlots[i]
		value, err := e.Args[slot].Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[slot] = value
		supplied[slot] = true
	}
	for i := range e.Count {
		if supplied[i] {
			continue
		}
		value, err := e.Args[i].Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	return e.Plan.Evaluate(ctx, values[:e.Count])
}

func (e JSONBSet) Reconstruct(info expr.Info, args [5]expr.Expr) (JSONBSet, error) {
	var usages [5]sqltype.Usage
	for i := range e.Count {
		usages[i] = args[i].ResultUsage()
	}
	plan, err := sqltype.BindJSONBSet(e.Plan.Name(), info.Usage, usages[:e.Count]...)
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}

// JSONBSet implements expr.Composite.
var _ expr.Composite = JSONBSet{}

func (e JSONBSet) Children() []expr.Expr { return e.Args[:e.Count] }
func (e JSONBSet) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("JSONBSet", children, e.Count); err != nil {
		return nil, err
	}
	args := e.Args
	copy(args[:], children)
	return rebuilt(e.Reconstruct(info, args))
}

func (e JSONBSet) IntrinsicVolatility() expr.Volatility { return expr.Immutable }

func (e JSONBSet) ValidateStoredProvider() error { return nil }

var _ expr.Renderable = JSONBSet{}

// Render implements expr.Renderable.
func (e JSONBSet) Render() (expr.Rendering, error) {
	if e.Count < 4 || e.Count > 5 || e.WrittenCount < 3 || e.WrittenCount > e.Count {
		return expr.Rendering{}, fmt.Errorf("invalid stored JSONB replacement")
	}
	args, err := writtenArgs(e.Args[:], e.WrittenSlots[:e.WrittenCount], e.Labels[:], e.Count, true, "invalid stored JSONB replacement argument")
	if err != nil {
		return expr.Rendering{}, err
	}
	return writtenCall("JSONB replacement "+e.Plan.Name(), e.Args[:e.Count], e.Plan.Name(), args, e.Variadic), nil
}

// SameGroup implements expr.GroupIdentity.
func (e JSONBSet) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(JSONBSet)
	if !ok || e.Plan.Name() != o.Plan.Name() || e.Count != o.Count {
		return false
	}
	for i, arg := range e.Args[:e.Count] {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}

// LooksLikeFunction implements expr.FunctionSyntax.
func (e JSONBSet) LooksLikeFunction() bool { return true }

// Substitute implements expr.Substitutable.
func (e JSONBSet) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("JSONBSet", children, e.Count); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}

// ClosureTransparent implements expr.ClosureTransparent.
func (e JSONBSet) ClosureTransparent() {}

func (e JSONBSet) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return captureWritten(e.Args[:], e.WrittenSlots[:], e.WrittenCount, e.Count)
}

func (e JSONBSet) CaptureOperand(written int) (expr.Expr, error) {
	return captureWrittenAt(e.Args[:], e.WrittenSlots[:], e.WrittenCount, e.Count, written)
}
