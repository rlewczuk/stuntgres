package functions

import "stuntgres/internal/sqltype"

// JSONBPathQueryArray keeps executable formals separate from written presentation.
// Defaults are real child expressions; all supplied children evaluate normally.
type JSONBPathQueryArray[E Expression[C], C Context, P any] struct {
	Info[P]
	Plan         sqltype.JSONBPathQueryArrayPlan
	Args         [4]E
	WrittenCount int
	WrittenSlots [4]uint8
	Labels       [4]string
	Variadic     bool // Written presentation only; the selected routine is nonvariadic.
}

func (e *JSONBPathQueryArray[E, C, P]) Arguments() []E { return e.Args[:] }

func (e JSONBPathQueryArray[E, C, P]) Eval(c C) (sqltype.Value, error) {
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

func (e JSONBPathQueryArray[E, C, P]) Reconstruct(info Info[P], args [4]E) (JSONBPathQueryArray[E, C, P], error) {
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
