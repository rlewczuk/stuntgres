package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (e subscriptsExpression) SourceAdmission() (bool, bool) { return true, true }

func (e subscriptsExpression) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(subscriptsExpression)
	return ok && e.ResultUsage() == o.ResultUsage() && len(e.args) == len(o.args)
}

func (e subscriptsExpression) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	if _, err := beginEvaluation(ctx); err != nil {
		return expr.SourceCursor{}, err
	}
	if len(e.args) != 2 && len(e.args) != 3 {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: invalid subscripts arguments")
	}
	var values [3]sqltype.Value
	null := false
	for i, arg := range e.args {
		value, err := arg.Eval(ctx)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		values[i] = value
		null = null || value.IsNull()
	}
	cursor := expr.NewSourceCursor(expr.SourceCursor{
		Type: e.Usage.Type, Exhausted: true, Step: 1, Advance: nextIntegerSource,
	})
	if null {
		return cursor, nil
	}
	dimension, err := values[1].Integer()
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if dimension <= 0 || dimension > int64(values[0].ArrayRank()) {
		return cursor, nil
	}
	reverse := false
	if len(e.args) == 3 {
		reverse, err = values[2].Boolean()
		if err != nil {
			return expr.SourceCursor{}, err
		}
	}
	dim := values[0].ArrayDimension(int(dimension) - 1)
	lower := int64(dim.Lower)
	upper := lower + int64(dim.Length) - 1
	cursor.Current, cursor.Stop = lower, upper
	if reverse {
		cursor.Current, cursor.Stop, cursor.Step = upper, lower, -1
	}
	cursor.Exhausted = dim.Length == 0
	return cursor, nil
}
