package functions

import (
	"fmt"
	"stuntgres/internal/sqltype"
)

// JSONExtract retains written argument order and labels for persistent replay.
// Its child slice is immutable; reconstruction accepts an owned replacement.
type JSONExtract[E Expression[C], C Context, P any] struct {
	Info[P]
	Plan     sqltype.JSONExtractPlan
	Args     []E
	Labels   []string
	Variadic bool
}

func (e JSONExtract[E, C, P]) Arguments() []E { return e.Args }
func (e JSONExtract[E, C, P]) Eval(c C) (sqltype.Value, error) {
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
func (e JSONExtract[E, C, P]) Reconstruct(info Info[P], args []E) (JSONExtract[E, C, P], error) {
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
func (e JSONExtract[E, C, P]) Validate() error {
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
