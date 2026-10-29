package functions

import (
	"stuntgres/internal/engine/expr"
)

func (e JSONBPathQuery) SourceAdmission() (bool, bool) { return true, true }

func (e JSONBPathQuery) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(JSONBPathQuery)
	return ok && e.Timezone == o.Timezone && e.ResultUsage() == o.ResultUsage()
}

func (e JSONBPathQuery) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	values, err := e.EvalSet(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(e.Usage.Type, values), nil
}
