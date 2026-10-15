package functions

import "stuntgres/internal/sqltype"

// JSONStripNulls separates written evaluation order from executable formals.
type JSONStripNulls[E Expression[C], C Context, P any] struct {
	Info[P]
	Plan         sqltype.JSONStripNullsPlan
	Args         [2]E
	WrittenCount int
	WrittenSlots [2]uint8
	Labels       [2]string
	Variadic     bool
}

func (e *JSONStripNulls[E, C, P]) Arguments() []E { return e.Args[:] }

func (e JSONStripNulls[E, C, P]) Eval(c C) (sqltype.Value, error) {
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

func (e JSONStripNulls[E, C, P]) Reconstruct(info Info[P], args [2]E) (JSONStripNulls[E, C, P], error) {
	plan, err := sqltype.BindJSONStripNulls(info.Usage, args[0].ResultUsage(), args[1].ResultUsage())
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}
