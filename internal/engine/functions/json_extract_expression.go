package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// JSONExtract retains written argument order and labels for persistent replay.
// Its child slice is immutable; reconstruction accepts an owned replacement.
type JSONExtract struct {
	expr.Info
	Plan     sqltype.JSONExtractPlan
	Args     []expr.Expr
	Labels   []string
	Variadic bool
}

func (e JSONExtract) Arguments() []expr.Expr { return e.Args }
func (e JSONExtract) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var local [16]sqltype.Value
	values := local[:min(len(e.Args), len(local))]
	if len(e.Args) > len(local) {
		values = make([]sqltype.Value, len(e.Args))
	}
	for i, arg := range e.Args {
		values[i], err = arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return e.Plan.Evaluate(ctx, values)
}
func (e JSONExtract) Reconstruct(info expr.Info, args []expr.Expr) (JSONExtract, error) {
	if len(args) < 2 || len(args) > 100 || e.Variadic && len(args) != 2 {
		return e, fmt.Errorf("invalid JSON extraction arity")
	}
	plan, err := sqltype.BindJSONExtract(e.Plan.Name(), info.Usage, args[0].ResultUsage(), args[1].ResultUsage(), e.Variadic)
	if err != nil {
		return e, err
	}
	for _, arg := range args[1:] {
		u := arg.ResultUsage()
		if u.Type != args[1].ResultUsage().Type || u.Typmod != -1 {
			return e, fmt.Errorf("inconsistent JSON extraction path usage")
		}
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}

// JSONExtract implements expr.Composite.
var _ expr.Composite = JSONExtract{}

func (e JSONExtract) Children() []expr.Expr { return e.Args }
func (e JSONExtract) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("JSONExtract", children, len(e.Args)); err != nil {
		return nil, err
	}
	return rebuilt(e.Reconstruct(info, children))
}
func (e JSONExtract) Validate() error {
	next, err := e.Reconstruct(e.Info, e.Args)
	if err != nil {
		return err
	}
	if next.Plan != e.Plan || len(e.Labels) != 0 && len(e.Labels) != len(e.Args) {
		return fmt.Errorf("invalid JSON extraction node")
	}
	for i, label := range e.Labels {
		if label == "" {
			continue
		}
		expected := "from_json"
		if i > 0 {
			expected = "path_elems"
		}
		if !e.Variadic || label != expected {
			return fmt.Errorf("invalid JSON extraction label")
		}
	}
	return nil
}

func (e JSONExtract) IntrinsicVolatility() expr.Volatility { return expr.Immutable }

func (e JSONExtract) ValidateStoredProvider() error { return nil }

var _ expr.Renderable = JSONExtract{}

// Render implements expr.Renderable.
func (e JSONExtract) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	spec := writtenCall("JSON extraction "+e.Plan.Name(), e.Arguments(), e.Plan.Name(), renderArgs(e.Args), e.Variadic)
	if len(e.Labels) > 0 {
		for i := range spec.Args {
			spec.Args[i].Label = e.Labels[i]
		}
	}
	return spec, nil
}
