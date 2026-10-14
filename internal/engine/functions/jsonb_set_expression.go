package functions

import "stuntgres/internal/sqltype"

// JSONBSet keeps executable formal slots separate from written presentation.
// Defaults are real child expressions; all supplied children evaluate normally.
type JSONBSet[E Expression[C], C Context, P any] struct {
	Info[P]
	Plan         sqltype.JSONBSetPlan
	Args         [5]E
	Count        int
	WrittenCount int
	WrittenSlots [5]uint8
	Labels       [5]string
	Variadic     bool
}

func (e *JSONBSet[E, C, P]) Arguments() []E { return e.Args[:e.Count] }

func (e JSONBSet[E, C, P]) Eval(c C) (sqltype.Value, error) {
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

func (e JSONBSet[E, C, P]) Reconstruct(info Info[P], args [5]E) (JSONBSet[E, C, P], error) {
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
