package functions

import "stuntgres/internal/engine/expr"
import "stuntgres/internal/sqltype"

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
	return expr.Rendering{Tree: "JSONPath array query", Inputs: e.Args[:]}, nil
}
