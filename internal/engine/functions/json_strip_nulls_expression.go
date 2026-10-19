package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// JSONStripNulls separates written evaluation order from executable formals.
type JSONStripNulls struct {
	expr.Info
	Plan         sqltype.JSONStripNullsPlan
	Args         [2]expr.Expr
	WrittenCount int
	WrittenSlots [2]uint8
	Labels       [2]string
	Variadic     bool
}

func (e *JSONStripNulls) Arguments() []expr.Expr { return e.Args[:] }

func (e JSONStripNulls) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	var supplied [2]bool
	for i := range e.WrittenCount {
		slot := e.WrittenSlots[i]
		value, err := e.Args[slot].Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[slot], supplied[slot] = value, true
	}
	for i := range e.Args {
		if supplied[i] {
			continue
		}
		value, err := e.Args[i].Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	return e.Plan.Evaluate(ctx, values[0], values[1])
}

func (e JSONStripNulls) Reconstruct(info expr.Info, args [2]expr.Expr) (JSONStripNulls, error) {
	plan, err := sqltype.BindJSONStripNulls(info.Usage, args[0].ResultUsage(), args[1].ResultUsage())
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}

// JSONStripNulls implements expr.Composite.
var _ expr.Composite = JSONStripNulls{}

func (e JSONStripNulls) Children() []expr.Expr { return e.Args[:len(e.Args)] }
func (e JSONStripNulls) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("JSONStripNulls", children, len(e.Args)); err != nil {
		return nil, err
	}
	args := e.Args
	copy(args[:], children)
	return rebuilt(e.Reconstruct(info, args))
}

func (e JSONStripNulls) IntrinsicVolatility() expr.Volatility { return expr.Immutable }

func (e JSONStripNulls) ValidateStoredProvider() error { return nil }

var _ expr.Renderable = JSONStripNulls{}

// Render implements expr.Renderable.
func (e JSONStripNulls) Render() (expr.Rendering, error) {
	if e.WrittenCount < 1 || e.WrittenCount > 2 {
		return expr.Rendering{}, fmt.Errorf("invalid stored JSON stripping")
	}
	args, err := writtenArgs(e.Args[:], e.WrittenSlots[:e.WrittenCount], e.Labels[:], 2, true, "invalid stored JSON stripping argument")
	if err != nil {
		return expr.Rendering{}, err
	}
	return writtenCall("JSON stripping", e.Args[:], "json_strip_nulls", args, e.Variadic), nil
}

// SameGroup implements expr.GroupIdentity.
func (e JSONStripNulls) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(JSONStripNulls)
	if !ok {
		return false
	}
	for i, arg := range e.Arguments() {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}

// LooksLikeFunction implements expr.FunctionSyntax.
func (e JSONStripNulls) LooksLikeFunction() bool { return true }

// Substitute implements expr.Substitutable.
func (e JSONStripNulls) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("JSONStripNulls", children, len(e.Args)); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}
