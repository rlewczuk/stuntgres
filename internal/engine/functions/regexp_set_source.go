package functions

import (
	"stuntgres/internal/engine/expr"
)

func (e RegexpSet) SourceAdmission() (bool, bool) { return e.Plan.ReturnsSet(), true }

func (e RegexpSet) SameSourceOccurrence(other expr.Expr) bool {
	_, ok := other.(RegexpSet)
	return ok
}

func (e RegexpSet) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	values, err := e.EvalSet(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(e.Usage.Type, values), nil
}
