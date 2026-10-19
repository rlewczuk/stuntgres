package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (e RangeInspect) SourceAdmission() (bool, bool) {
	return e.Plan.Operation() == sqltype.RangeConstruct, false
}

func (e RangeInspect) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	admitted, _ := e.SourceAdmission()
	if !admitted {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: invalid closed function source")
	}
	value, err := e.Eval(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
