package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (e JSONBRecord) SourceAdmission() (bool, bool) { return true, e.ReturnsSet() }

func (e JSONBRecord) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(JSONBRecord)
	return ok && e.Name == o.Name && sqltype.SameRecordUsage(e.ResultUsage(), o.ResultUsage())
}

func (e JSONBRecord) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	if !e.ReturnsSet() {
		value, err := e.Eval(c)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		return expr.ScalarSource(value), nil
	}
	values, err := e.EvalSet(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(e.Usage.Type, values), nil
}
