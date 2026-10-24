package functions

import (
	"stuntgres/internal/engine/expr"
)

func (e RegexpSet) SourceAdmission() (bool, bool) { return e.Plan.ReturnsSet(), true }

func (e RegexpSet) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(RegexpSet)
	if !ok { return false }
	if e.Citext != nil || o.Citext != nil {
		return e.Citext != nil && o.Citext != nil && *e.Citext == *o.Citext && e.RoutineReference == o.RoutineReference && e.Plan == o.Plan
	}
	return true
}

func (e RegexpSet) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	values, err := e.EvalSet(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(e.Usage.Type, values), nil
}
