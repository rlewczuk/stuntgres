package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// JSONBPathQueryArray keeps executable formals separate from written presentation.
// Defaults are real child expressions; all supplied children evaluate normally.
type JSONBPathQueryArray struct {
	expr.Info
	Plan         sqltype.JSONBPathQueryArrayPlan
	Args         [4]expr.Expr
	WrittenCount int
	WrittenSlots [4]uint8
	Labels       [4]string
	Variadic     bool // Written presentation only; the selected routine is nonvariadic.
}

func (e *JSONBPathQueryArray) Arguments() []expr.Expr { return e.Args[:] }

func (e JSONBPathQueryArray) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var values [4]sqltype.Value
	var supplied [4]bool
	for i := range e.WrittenCount {
		slot := e.WrittenSlots[i]
		value, err := e.Args[slot].Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[slot] = value
		supplied[slot] = true
	}
	for i := range 4 {
		if supplied[i] {
			continue
		}
		value, err := e.Args[i].Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	return e.Plan.Evaluate(ctx, values[:])
}

func (e JSONBPathQueryArray) Reconstruct(info expr.Info, args [4]expr.Expr) (JSONBPathQueryArray, error) {
	var usages [4]sqltype.Usage
	for i := range 4 {
		usages[i] = args[i].ResultUsage()
	}
	plan, err := sqltype.BindJSONBPathQueryArray(info.Usage, usages[:]...)
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}

// JSONBPathQueryArray implements expr.Composite.
var _ expr.Composite = JSONBPathQueryArray{}

func (e JSONBPathQueryArray) Children() []expr.Expr { return e.Args[:len(e.Args)] }
func (e JSONBPathQueryArray) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("JSONBPathQueryArray", children, len(e.Args)); err != nil {
		return nil, err
	}
	args := e.Args
	copy(args[:], children)
	return rebuilt(e.Reconstruct(info, args))
}

func (e JSONBPathQueryArray) IntrinsicVolatility() expr.Volatility { return expr.Immutable }

func (e JSONBPathQueryArray) ValidateStoredProvider() error { return nil }

var _ expr.Renderable = JSONBPathQueryArray{}

// Render implements expr.Renderable.
func (e JSONBPathQueryArray) Render() (expr.Rendering, error) {
	if e.WrittenCount < 2 || e.WrittenCount > 4 {
		return expr.Rendering{}, fmt.Errorf("invalid stored JSONPath array query")
	}
	args, err := writtenArgs(e.Args[:], e.WrittenSlots[:e.WrittenCount], e.Labels[:], 4, true, "invalid stored JSONPath array query argument")
	if err != nil {
		return expr.Rendering{}, err
	}
	return writtenCall("JSONPath array query", e.Args[:], "jsonb_path_query_array", args, e.Variadic), nil
}

// SameGroup implements expr.GroupIdentity.
func (e JSONBPathQueryArray) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(JSONBPathQueryArray)
	// Redundant VARIADIC is presentation only for this nonvariadic routine.
	if !ok || e.WrittenCount != o.WrittenCount || e.WrittenSlots != o.WrittenSlots || e.Labels != o.Labels {
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
func (e JSONBPathQueryArray) LooksLikeFunction() bool { return true }

// Substitute implements expr.Substitutable.
func (e JSONBPathQueryArray) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("JSONBPathQueryArray", children, len(e.Args)); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}

// ClosureTransparent implements expr.ClosureTransparent.
func (e JSONBPathQueryArray) ClosureTransparent() {}
